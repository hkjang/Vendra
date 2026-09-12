package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestASilentSignInIsOnlyAttemptedWhenTheAdministratorAllowsIt walks the
// prompt=none flow end to end against the fake provider. The thing being held
// is that every redirect the silent attempt produces is tied to the autoLogin
// setting, and that a refusal lands somewhere the browser will not retry from.
func TestASilentSignInIsOnlyAttemptedWhenTheAdministratorAllowsIt(t *testing.T) {
	app, pool := newTestApp(t)
	handler := app.Handler()
	idp := newFakeIdP(t, "vendra-test-client")
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE email='oidc-silent@vendra.test'`)
		_, _ = pool.Exec(cleanup, `UPDATE settings SET value='{"enabled":false}' WHERE key='oidc'`)
	})
	configure := func(autoLogin bool) {
		configureOIDCValue(t, app, fmt.Sprintf(`{"enabled":true,"issuer":%q,"clientId":"vendra-test-client","scopes":["openid","email"],"autoCreate":true,"defaultRole":"business_user","autoLogin":%t}`, idp.server.URL, autoLogin))
	}
	publicConfig := func() map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/config", nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode config: %v", err)
		}
		return body
	}
	refuse := func(cookie *http.Cookie, state string) *httptest.ResponseRecorder {
		t.Helper()
		// Keycloak answers a prompt=none request with no session this way: an
		// error parameter instead of a code, and the state echoed back.
		r := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?error=login_required&state="+url.QueryEscape(state), nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// Off is the default, and the key being absent must read as off.
	configure(false)
	if got := publicConfig(); got["autoLogin"] != false {
		t.Errorf("autoLogin published as %v while off", got["autoLogin"])
	}
	// Anybody can paste ?prompt=none into an address. While the setting is off
	// that must quietly become an ordinary sign-in, or the refusal redirect
	// below could be provoked by a link rather than by the administrator.
	cookie, redirect := startFlowAt(t, handler, "/api/auth/oidc/start?prompt=none")
	if redirect.Query().Has("prompt") {
		t.Errorf("prompt=none reached the provider while autoLogin was off: %s", redirect)
	}
	if w := refuse(cookie, redirect.Query().Get("state")); w.Code != http.StatusUnauthorized {
		t.Errorf("a refused ordinary sign-in answered %d, want the 401 it always did: %s", w.Code, w.Body.String())
	}

	configure(true)
	if got := publicConfig(); got["autoLogin"] != true {
		t.Errorf("autoLogin published as %v while on", got["autoLogin"])
	}
	// The login-screen button never asks for prompt=none; only the browser's
	// silent attempt does.
	if _, redirect := startFlowAt(t, handler, "/api/auth/oidc/start"); redirect.Query().Has("prompt") {
		t.Errorf("an ordinary sign-in asked the provider for prompt=none: %s", redirect)
	}
	cookie, redirect = startFlowAt(t, handler, "/api/auth/oidc/start?prompt=none&returnTo=%2Fsuppliers%2F42")
	if redirect.Query().Get("prompt") != "none" {
		t.Fatalf("the silent attempt did not ask for prompt=none: %s", redirect)
	}

	// No provider session: the ordinary answer, not a failure. The browser is
	// mid-navigation with nothing on screen, so a JSON 401 would be what the
	// visitor saw; instead they land on the login screen with the marker that
	// stops a retry.
	w := refuse(cookie, redirect.Query().Get("state"))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login?sso=none" {
		t.Fatalf("a silent refusal answered %d %q, want 302 /login?sso=none: %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "vendra_oidc_flow=;") {
		t.Errorf("the spent flow cookie was left behind: %q", w.Header().Get("Set-Cookie"))
	}
	// A spent state is not a marker: a forged callback with no flow behind it
	// gets the same 400 every callback without one gets, and no redirect.
	if w := refuse(&http.Cookie{Name: "vendra_oidc_flow", Value: "forged"}, "x"); w.Code != http.StatusBadRequest {
		t.Errorf("a callback without a flow answered %d, want 400", w.Code)
	}

	// A provider session: the code comes straight back and the visitor ends
	// up where the deep link pointed, signed in.
	cookie, redirect = startFlowAt(t, handler, "/api/auth/oidc/start?prompt=none&returnTo=%2Fsuppliers%2F42")
	idp.claims = map[string]any{"sub": "silent-subject", "email": "oidc-silent@vendra.test", "email_verified": true, "name": "조용한 사용자", "nonce": redirect.Query().Get("nonce")}
	w = callback(t, handler, cookie, redirect.Query().Get("state"))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/suppliers/42" {
		t.Fatalf("a silent sign-in answered %d %q, want 302 /suppliers/42: %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), sessionCookie) {
		t.Error("a completed silent sign-in issued no session")
	}
	// And the return address is held to the same rule as the ordinary flow:
	// off-site destinations fall back to the front page.
	cookie, _ = startFlowAt(t, handler, "/api/auth/oidc/start?prompt=none&returnTo=%2F%2Fevil.example")
	if flow := decodeFlow(t, app, cookie); flow.ReturnTo != "/" || !flow.Silent {
		t.Errorf("flow = %+v, want a silent attempt returning to /", flow)
	}
}

func decodeFlow(t *testing.T, app *App, cookie *http.Cookie) oidcFlow {
	t.Helper()
	plain, err := app.vault.Decrypt(cookie.Value)
	if err != nil {
		t.Fatalf("decrypt flow: %v", err)
	}
	var flow oidcFlow
	if err := json.Unmarshal([]byte(plain), &flow); err != nil {
		t.Fatalf("decode flow: %v", err)
	}
	return flow
}
