package httpapi

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeIdP is the smallest identity provider go-oidc will accept: a discovery
// document, a JWKS, and a token endpoint that mints an id_token the test
// controls. It exists so the callback can be exercised for real rather than
// reasoned about.
type fakeIdP struct {
	server   *httptest.Server
	key      *rsa.PrivateKey
	clientID string
	claims   map[string]any
}

func newFakeIdP(t *testing.T, clientID string) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &fakeIdP{key: key, clientID: clientID}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{
			"issuer":                                idp.server.URL,
			"authorization_endpoint":                idp.server.URL + "/authorize",
			"token_endpoint":                        idp.server.URL + "/token",
			"jwks_uri":                              idp.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{
			"access_token": "fake", "token_type": "Bearer", "id_token": idp.idToken(t),
		})
	})
	idp.server = httptest.NewServer(mux)
	t.Cleanup(idp.server.Close)
	return idp
}

func (f *fakeIdP) idToken(t *testing.T) string {
	t.Helper()
	claims := map[string]any{
		"iss": f.server.URL, "aud": f.clientID,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	}
	for k, v := range f.claims {
		claims[k] = v
	}
	segment := func(v any) string {
		encoded, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(encoded)
	}
	signing := segment(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test"}) + "." + segment(claims)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// startFlow performs the redirect leg and returns the cookie plus the state and
// nonce the provider is expected to echo back.
func startFlow(t *testing.T, handler http.Handler) (cookie *http.Cookie, state, nonce string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/start", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("oidc start returned %d: %s", w.Code, w.Body.String())
	}
	redirect, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if redirect.Query().Get("code_challenge_method") != "S256" {
		t.Errorf("PKCE challenge method = %q, want S256", redirect.Query().Get("code_challenge_method"))
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "vendra_oidc_flow" {
			return c, redirect.Query().Get("state"), redirect.Query().Get("nonce")
		}
	}
	t.Fatal("oidc start set no flow cookie")
	return nil, "", ""
}

func configureOIDC(t *testing.T, app *App, issuer, clientID string, requireVerified bool) {
	t.Helper()
	value := fmt.Sprintf(`{"enabled":true,"issuer":%q,"clientId":%q,"scopes":["openid","email","profile"],"autoCreate":true,"defaultRole":"business_user","requireVerifiedEmail":%t}`, issuer, clientID, requireVerified)
	if _, err := app.db.Exec(t.Context(), `INSERT INTO settings(key,value,category) VALUES('oidc',$1,'identity')
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, value); err != nil {
		t.Fatalf("configure oidc: %v", err)
	}
}

func callback(t *testing.T, handler http.Handler, cookie *http.Cookie, state string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestOIDCCallbackRefusesAnUnverifiedEmail(t *testing.T) {
	app, pool := newTestApp(t)
	handler := app.Handler()
	idp := newFakeIdP(t, "vendra-test-client")
	configureOIDC(t, app, idp.server.URL, "vendra-test-client", true)
	t.Cleanup(func() {
		// Not t.Context(): it is already cancelled by the time cleanup runs, so
		// the deletes would fail silently and leak state into the next run.
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE email LIKE 'oidc-%@vendra.test'`)
		_, _ = pool.Exec(cleanup, `UPDATE settings SET value='{"enabled":false}' WHERE key='oidc'`)
	})

	// The attack v0.6.1 closed: a provider that lets a user pick an unverified
	// address must not hand over the account that owns it.
	cookie, state, nonce := startFlow(t, handler)
	idp.claims = map[string]any{"sub": "attacker-subject", "email": "oidc-victim@vendra.test", "email_verified": false, "name": "공격자", "nonce": nonce}
	w := callback(t, handler, cookie, state)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an unverified email signed in with %d: %s", w.Code, w.Body.String())
	}
	var created int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE email='oidc-victim@vendra.test'`).Scan(&created); err != nil {
		t.Fatalf("count: %v", err)
	}
	if created != 0 {
		t.Error("an account was created for an unverified address")
	}

	// A verified address completes the flow and issues a session.
	cookie, state, nonce = startFlow(t, handler)
	idp.claims = map[string]any{"sub": "honest-subject", "email": "oidc-honest@vendra.test", "email_verified": true, "name": "정상 사용자", "nonce": nonce}
	w = callback(t, handler, cookie, state)
	if w.Code != http.StatusFound {
		t.Fatalf("a verified sign-in returned %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), sessionCookie) {
		t.Error("a completed OIDC sign-in issued no session")
	}
	var subject string
	if err := pool.QueryRow(t.Context(), `SELECT oidc_subject FROM users WHERE email='oidc-honest@vendra.test'`).Scan(&subject); err != nil {
		t.Fatalf("read created user: %v", err)
	}
	if subject != "honest-subject" {
		t.Errorf("linked subject = %q", subject)
	}
}

// TestOIDCCannotTakeOverAnExistingAccount is the attack itself: a local account
// exists, and someone signs in through a provider claiming its address without
// having proved they own it.
func TestOIDCCannotTakeOverAnExistingAccount(t *testing.T) {
	app, pool := newTestApp(t)
	handler := app.Handler()
	ctx := t.Context()
	idp := newFakeIdP(t, "vendra-test-client")
	configureOIDC(t, app, idp.server.URL, "vendra-test-client", true)

	const victim = "oidc-existing-admin@vendra.test"
	hash, err := app.hashPassword(ctx, "VictimPassphrase!2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var victimID string
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,user_type,status) VALUES($1,'기존 관리자',$2,'internal','active')
		ON CONFLICT(email) DO UPDATE SET password_hash=excluded.password_hash,status='active',oidc_subject=NULL RETURNING id`, victim, hash).Scan(&victimID); err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM users WHERE email=$1`, victim)
		_, _ = pool.Exec(cleanup, `UPDATE settings SET value='{"enabled":false}' WHERE key='oidc'`)
	})

	cookie, state, nonce := startFlow(t, handler)
	idp.claims = map[string]any{"sub": "impostor", "email": victim, "email_verified": false, "name": "사칭", "nonce": nonce}
	if got := callback(t, handler, cookie, state).Code; got != http.StatusForbidden {
		t.Fatalf("an unverified claim to an existing address returned %d, want 403", got)
	}
	var linked *string
	if err := pool.QueryRow(ctx, `SELECT oidc_subject FROM users WHERE id=$1`, victimID).Scan(&linked); err != nil {
		t.Fatalf("read victim: %v", err)
	}
	if linked != nil {
		t.Errorf("the existing account was linked to %q", *linked)
	}

	// Once the provider vouches for the address, linking is legitimate.
	cookie, state, nonce = startFlow(t, handler)
	idp.claims = map[string]any{"sub": "rightful-owner", "email": victim, "email_verified": true, "name": "본인", "nonce": nonce}
	if got := callback(t, handler, cookie, state).Code; got != http.StatusFound {
		t.Fatalf("a verified claim returned %d, want a completed sign-in", got)
	}
	if err := pool.QueryRow(ctx, `SELECT oidc_subject FROM users WHERE id=$1`, victimID).Scan(&linked); err != nil {
		t.Fatalf("read victim: %v", err)
	}
	if linked == nil || *linked != "rightful-owner" {
		t.Errorf("linked subject = %v, want rightful-owner", linked)
	}

	// A subject already linked identifies the account on its own, so a later
	// sign-in does not need the email claim to be verified again.
	cookie, state, nonce = startFlow(t, handler)
	idp.claims = map[string]any{"sub": "rightful-owner", "email": victim, "email_verified": false, "name": "본인", "nonce": nonce}
	if got := callback(t, handler, cookie, state).Code; got != http.StatusFound {
		t.Errorf("an already-linked subject was refused with %d", got)
	}
}
