package httpapi

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// MCP 를 개인 키 없이 Keycloak 토큰으로.
//
// The authorization flow itself — PKCE, the redirect, the code exchange — is
// Keycloak's and the client's. What is this server's is the resource-server
// half of the specification, and that is what these tests hold it to: it says
// where the authorization server is, it turns a 401 into a pointer there, and
// it accepts exactly the tokens that server issued for this resource, for a
// person Vendra already knows, with the powers a key would have and no more.

const (
	mcpSSOEmail   = "mcp-sso@vendra.test"
	mcpSSOSubject = "sso-subject-mcp"
	listTools     = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
)

// signJWT signs a token with the fake provider's realm key, or with any
// header the test names — an HS256 header with the public key as the secret
// is the classic forgery, and the test wants to present it.
func signJWT(t *testing.T, idp *fakeIdP, header, claims map[string]any) string {
	t.Helper()
	segment := func(v any) string {
		encoded, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(encoded)
	}
	signing := segment(header) + "." + segment(claims)
	digest := sha256.Sum256([]byte(signing))
	var signature []byte
	if header["alg"] == "HS256" {
		mac := hmac.New(sha256.New, idp.key.PublicKey.N.Bytes())
		mac.Write([]byte(signing))
		signature = mac.Sum(nil)
	} else {
		var err error
		signature, err = rsa.SignPKCS1v15(rand.Reader, idp.key, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// accessToken is what Keycloak hands an MCP client after the person signed
// in: signed by the realm key, issued by the issuer, for an audience, typ
// Bearer, the client in azp.
func accessToken(t *testing.T, idp *fakeIdP, audience any, extra map[string]any) string {
	t.Helper()
	claims := map[string]any{
		"iss": idp.server.URL, "aud": audience, "sub": mcpSSOSubject, "azp": "claude-mcp",
		"exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(),
		"typ": "Bearer", "preferred_username": "mcp-sso", "email": mcpSSOEmail, "email_verified": true,
		"scope": "openid profile email",
	}
	for key, value := range extra {
		claims[key] = value
	}
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test"}
	if alg, ok := extra["__alg"]; ok {
		header["alg"] = alg
		delete(claims, "__alg")
	}
	return signJWT(t, idp, header, claims)
}

type mcpOAuthWorld struct {
	t       *testing.T
	app     *App
	handler http.Handler
	idp     *fakeIdP
	admin   string
}

func newMCPOAuthWorld(t *testing.T) *mcpOAuthWorld {
	t.Helper()
	app, pool := newTestApp(t)
	ctx := context.Background()
	reset := func() {
		for _, q := range []string{
			`DELETE FROM audit_logs WHERE actor_id IN (SELECT id FROM users WHERE email LIKE 'mcp-sso%')`,
			`DELETE FROM api_keys WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'mcp-sso%')`,
			`DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'mcp-sso%')`,
			`DELETE FROM user_roles WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'mcp-sso%')`,
			`DELETE FROM users WHERE email LIKE 'mcp-sso%'`,
			`UPDATE settings SET value='false' WHERE key='mcp.oauth.enabled'`,
			`UPDATE settings SET value='""' WHERE key IN ('mcp.oauth.resource','mcp.oauth.audience')`,
			`UPDATE settings SET value='{"enabled":false}' WHERE key='oidc'`,
		} {
			if _, err := pool.Exec(ctx, q); err != nil {
				t.Fatalf("reset: %v\n  %s", err, q)
			}
		}
		if _, err := pool.Exec(ctx, `UPDATE settings SET value=$1 WHERE key='mcp.oauth.scopes'`, raw(defaultMCPOAuthScopes)); err != nil {
			t.Fatalf("reset scopes: %v", err)
		}
	}
	reset()
	t.Cleanup(reset)
	if _, err := pool.Exec(ctx, `INSERT INTO users(email,display_name,user_type,status,oidc_subject) VALUES($1,'SSO 사용자','internal','active',$2)`, mcpSSOEmail, mcpSSOSubject); err != nil {
		t.Fatalf("create sso user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT u.id,r.id FROM users u, roles r WHERE u.email=$1 AND r.code='finance'`, mcpSSOEmail); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	handler := app.Handler()
	w := &mcpOAuthWorld{t: t, app: app, handler: handler, idp: newFakeIdP(t, "vendra-web")}
	w.admin = sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "10.9.0.2:4000"))
	return w
}

// mcp sends one MCP call with the given bearer, as a client at the public
// address would.
func (w *mcpOAuthWorld) mcp(bearer, body string) *httptest.ResponseRecorder {
	w.t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Host = "vendra.example.test"
	r.Header.Set("X-Forwarded-Proto", "https")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

func (w *mcpOAuthWorld) get(path, bearer string) *httptest.ResponseRecorder {
	w.t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Host = "vendra.example.test"
	r.Header.Set("X-Forwarded-Proto", "https")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

// put writes one setting as the administrator does from the screen.
func (w *mcpOAuthWorld) put(key string, value any) *httptest.ResponseRecorder {
	w.t.Helper()
	body, _ := json.Marshal(map[string]any{"value": value, "category": "identity"})
	r := httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/"+key, strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: w.admin})
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

func (w *mcpOAuthWorld) mustPut(key string, value any) {
	w.t.Helper()
	if rec := w.put(key, value); rec.Code != http.StatusOK {
		w.t.Fatalf("PUT %s=%v answered %d %s", key, value, rec.Code, rec.Body.String())
	}
}

func (w *mcpOAuthWorld) toolsListed(rec *httptest.ResponseRecorder) bool {
	w.t.Helper()
	var reply struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	return rec.Code == http.StatusOK && json.Unmarshal(rec.Body.Bytes(), &reply) == nil && len(reply.Result.Tools) == len(mcpTools)
}

func errorMessage(rec *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code + ": " + body.Error.Message
}

// guards: protectedResourceMetadata, mcpChallenge, authenticate, validMCPOAuthSetting
func TestARefusedMCPClientIsToldWhereToSignIn(t *testing.T) {
	w := newMCPOAuthWorld(t)

	// Off by default, and the four rows are installed so: a deployment
	// without SSO advertises nothing, and a refusal is the refusal it always
	// was — a token-shaped bearer is answered exactly like no credential.
	for _, path := range []string{protectedResourcePath, protectedResourcePath + "/mcp"} {
		if rec := w.get(path, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s is served with SSO off: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := w.mcp("", listTools); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("a 401 with SSO off: %d WWW-Authenticate=%q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if rec := w.mcp(accessToken(t, w.idp, "https://vendra.example.test/mcp", nil), listTools); rec.Code != http.StatusUnauthorized || errorMessage(rec) != "unauthenticated: 로그인이 필요합니다" {
		t.Fatalf("a token with SSO off was answered %d %s, want the plain refusal", rec.Code, rec.Body.String())
	}

	// The switch cannot be turned on with nothing to verify against.
	if rec := w.put(mcpOAuthEnabledKey, true); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Issuer") {
		t.Fatalf("enabling without an issuer answered %d %s", rec.Code, rec.Body.String())
	}
	configureOIDCValue(t, w.app, fmt.Sprintf(`{"enabled":true,"issuer":%q,"clientId":"vendra-web","publicUrl":"https://vendra.example.test","scopes":["openid","email"]}`, w.idp.server.URL))
	for name, row := range map[string][2]any{
		"a resource that is not an address": {mcpOAuthResourceKey, "vendra/mcp"},
		"a scope nothing checks":            {mcpOAuthScopesKey, "supplier.read mcp:read"},
		"an empty scope list":               {mcpOAuthScopesKey, "  "},
		"a switch that is not a boolean":    {mcpOAuthEnabledKey, "yes"},
	} {
		if rec := w.put(row[0].(string), row[1]); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), row[0].(string)) {
			t.Errorf("%s was answered %d %s, want 400 naming the row", name, rec.Code, rec.Body.String())
		}
	}
	w.mustPut(mcpOAuthEnabledKey, true)

	// RFC 9728: the resource names itself and its authorization server, bare,
	// readable from anywhere. The resource comes from the public address the
	// web sign-in is configured with, not from whatever Host was sent.
	for _, path := range []string{protectedResourcePath, protectedResourcePath + "/mcp"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "10.0.0.7:8080"
		rec := httptest.NewRecorder()
		w.handler.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s: %d ACAO=%q %s", path, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"), rec.Body.String())
		}
		var metadata struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
			BearerMethods        []string `json:"bearer_methods_supported"`
			Scopes               []string `json:"scopes_supported"`
			Envelope             any      `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &metadata); err != nil {
			t.Fatalf("%s is not JSON: %v", path, err)
		}
		if metadata.Resource != "https://vendra.example.test/mcp" || len(metadata.AuthorizationServers) != 1 || metadata.AuthorizationServers[0] != w.idp.server.URL ||
			len(metadata.BearerMethods) != 1 || metadata.BearerMethods[0] != "header" || strings.Join(metadata.Scopes, " ") != defaultMCPOAuthScopes {
			t.Errorf("%s: %s", path, rec.Body.String())
		}
	}

	// The 401 on the MCP path points at that document; the REST 401 does not,
	// because a browser or a REST client sent there has nowhere to go.
	rec := w.mcp("", listTools)
	challenge := rec.Header().Get("WWW-Authenticate")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(challenge, `resource_metadata="https://vendra.example.test/.well-known/oauth-protected-resource/mcp"`) || strings.Contains(challenge, "invalid_token") {
		t.Fatalf("the MCP 401 does not point at the metadata: %d %q", rec.Code, challenge)
	}
	if rec := w.mcp("vnd_not_a_real_key", listTools); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("a refused key on /mcp did not say invalid_token: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if rec := w.get("/api/v1/me", ""); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "" {
		t.Errorf("the REST 401 carries a challenge: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}

// guards: fromOAuthToken, limitToRolePermissions, authenticate
func TestOnlyATokenMintedForThisServerOpensMCP(t *testing.T) {
	w := newMCPOAuthWorld(t)
	configureOIDCValue(t, w.app, fmt.Sprintf(`{"enabled":true,"issuer":%q,"clientId":"vendra-web","publicUrl":"https://vendra.example.test","scopes":["openid","email"]}`, w.idp.server.URL))
	w.mustPut(mcpOAuthEnabledKey, true)
	const resource = "https://vendra.example.test/mcp"

	// The proper path: an Audience mapper put the resource in aud.
	if rec := w.mcp(accessToken(t, w.idp, []string{"account", resource}, nil), listTools); !w.toolsListed(rec) {
		t.Fatalf("a token for this resource did not open tools/list: %d %s", rec.Code, rec.Body.String())
	}
	// The same token is not a REST credential.
	if rec := w.get("/api/v1/me", accessToken(t, w.idp, resource, nil)); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "" {
		t.Errorf("a valid SSO token opened REST: %d %s", rec.Code, rec.Body.String())
	}

	// What a real Keycloak 26 issues without a mapper: aud is "account" and
	// the client is in azp. Refused — and the refusal says what was seen and
	// both ways to fix it.
	rec := w.mcp(accessToken(t, w.idp, "account", nil), listTools)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("a token for another audience was answered %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	for _, want := range []string{"aud=[account]", `azp="claude-mcp"`, mcpOAuthAudienceKey, `"claude-mcp"`, `"` + resource + `"`} {
		if !strings.Contains(errorMessage(rec), want) {
			t.Errorf("the audience refusal does not say %s: %s", want, errorMessage(rec))
		}
	}
	// The administrator writes the client id down; no mapper needed.
	w.mustPut(mcpOAuthAudienceKey, " claude-mcp  cursor-mcp ")
	if rec := w.mcp(accessToken(t, w.idp, "account", nil), listTools); !w.toolsListed(rec) {
		t.Fatalf("a token whose azp is on the list was refused: %d %s", rec.Code, rec.Body.String())
	}
	// An audience on the list is accepted too; a stranger's client is not.
	if rec := w.mcp(accessToken(t, w.idp, "cursor-mcp", map[string]any{"azp": "other-app"}), listTools); !w.toolsListed(rec) {
		t.Errorf("a token whose aud is on the list was refused: %d %s", rec.Code, rec.Body.String())
	}
	if rec := w.mcp(accessToken(t, w.idp, "account", map[string]any{"azp": "other-app"}), listTools); rec.Code != http.StatusUnauthorized {
		t.Errorf("another application's token opened MCP: %d %s", rec.Code, rec.Body.String())
	}

	// Each of these is a token the realm key signed, or looks like one, and
	// none may pass. Another realm's token fails on its signature before its
	// issuer is read; the issuer check is shown with this realm's key under a
	// foreign iss.
	other := newFakeIdP(t, "vendra-web")
	for name, tc := range map[string]struct {
		token string
		says  string
	}{
		"an expired token":          {accessToken(t, w.idp, resource, map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}), "만료"},
		"another realm's key":       {accessToken(t, other, resource, nil), "서명"},
		"another issuer":            {accessToken(t, w.idp, resource, map[string]any{"iss": other.server.URL}), "발급자"},
		"an ID token":               {accessToken(t, w.idp, resource, map[string]any{"typ": "ID"}), "ID 토큰"},
		"a refresh token":           {accessToken(t, w.idp, resource, map[string]any{"typ": "Refresh"}), "typ"},
		"an HS256 signature":        {accessToken(t, w.idp, resource, map[string]any{"__alg": "HS256"}), "서명"},
		"a token bound to a key":    {accessToken(t, w.idp, resource, map[string]any{"cnf": map[string]any{"jkt": "abc"}}), "cnf"},
		"a token not yet valid":     {accessToken(t, w.idp, resource, map[string]any{"nbf": time.Now().Add(time.Hour).Unix()}), "nbf"},
		"a token without a subject": {accessToken(t, w.idp, resource, map[string]any{"sub": ""}), "sub"},
	} {
		rec := w.mcp(tc.token, listTools)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), tc.says) {
			t.Errorf("%s was answered %d %s, want 401 mentioning %q", name, rec.Code, rec.Body.String(), tc.says)
		}
	}

	// Nobody is registered by presenting a token: an unknown subject is sent
	// to the web, a deactivated account stays deactivated, and no row appears.
	stranger := accessToken(t, w.idp, resource, map[string]any{"sub": "sso-subject-stranger", "email": "mcp-sso-stranger@vendra.test", "preferred_username": "stranger"})
	if rec := w.mcp(stranger, listTools); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "웹으로") {
		t.Errorf("an unregistered subject was answered %d %s", rec.Code, rec.Body.String())
	}
	var strangers int
	if err := w.app.db.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE email='mcp-sso-stranger@vendra.test' OR oidc_subject='sso-subject-stranger'`).Scan(&strangers); err != nil || strangers != 0 {
		t.Errorf("presenting a token created an account: %d %v", strangers, err)
	}
	if _, err := w.app.db.Exec(context.Background(), `UPDATE users SET status='disabled' WHERE email=$1`, mcpSSOEmail); err != nil {
		t.Fatal(err)
	}
	if rec := w.mcp(accessToken(t, w.idp, resource, nil), listTools); rec.Code != http.StatusUnauthorized {
		t.Errorf("a deactivated account came back through MCP: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := w.app.db.Exec(context.Background(), `UPDATE users SET status='active' WHERE email=$1`, mcpSSOEmail); err != nil {
		t.Fatal(err)
	}

	// An account linked by address rather than subject is found the way the
	// web sign-in finds it — and only with a verified address.
	if _, err := w.app.db.Exec(context.Background(), `UPDATE users SET oidc_subject=NULL WHERE email=$1`, mcpSSOEmail); err != nil {
		t.Fatal(err)
	}
	if rec := w.mcp(accessToken(t, w.idp, resource, map[string]any{"email_verified": false}), listTools); rec.Code != http.StatusUnauthorized {
		t.Errorf("an unverified address matched an account: %d %s", rec.Code, rec.Body.String())
	}
	if rec := w.mcp(accessToken(t, w.idp, resource, nil), listTools); !w.toolsListed(rec) {
		t.Errorf("a verified address did not match the account: %d %s", rec.Code, rec.Body.String())
	}
	var linked *string
	if err := w.app.db.QueryRow(context.Background(), `SELECT oidc_subject FROM users WHERE email=$1`, mcpSSOEmail).Scan(&linked); err != nil || linked != nil {
		t.Errorf("presenting a token wrote to the account: subject=%v err=%v", linked, err)
	}
}

// guards: fromOAuthToken, mcpCall
func TestAnSSOTokenHasThePowersAKeyWouldAndNoMore(t *testing.T) {
	w := newMCPOAuthWorld(t)
	configureOIDCValue(t, w.app, fmt.Sprintf(`{"enabled":true,"issuer":%q,"clientId":"vendra-web","publicUrl":"https://vendra.example.test","scopes":["openid","email"]}`, w.idp.server.URL))
	w.mustPut(mcpOAuthEnabledKey, true)
	const resource = "https://vendra.example.test/mcp"
	call := func(bearer, tool string) *httptest.ResponseRecorder {
		return w.mcp(bearer, fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":{"query":"x","supplierId":"00000000-0000-0000-0000-000000000000"}}}`, tool))
	}
	refused := func(rec *httptest.ResponseRecorder) bool {
		return rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "Insufficient")
	}

	// A 재무 담당자 holds supplier.read and contract.read but no
	// evaluation.read; the administrator's scope list names evaluation.read,
	// but the role does not grant it, so the token does not either — the
	// same cut a key gets.
	token := accessToken(t, w.idp, resource, nil)
	for _, tool := range []string{"search_suppliers", "search_contracts"} {
		if rec := call(token, tool); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Insufficient") {
			t.Errorf("%s with the role's own permission: %d %s", tool, rec.Code, rec.Body.String())
		}
	}
	if rec := call(token, "get_supplier_score"); !refused(rec) {
		t.Errorf("a token reached past the holder's role: %d %s", rec.Code, rec.Body.String())
	}
	// A role claim in the token raises nothing.
	if rec := call(accessToken(t, w.idp, resource, map[string]any{"realm_access": map[string]any{"roles": []string{"system_admin"}}, "role": "system_admin"}), "get_supplier_score"); !refused(rec) {
		t.Errorf("a role claim in the token opened a door: %d %s", rec.Code, rec.Body.String())
	}
	// The administrator's list is the ceiling: without contract.read on it,
	// the role's contract.read does not reach search_contracts.
	w.mustPut(mcpOAuthScopesKey, "supplier.read")
	if rec := call(token, "search_contracts"); !refused(rec) {
		t.Errorf("a token reached past the administrator's scope list: %d %s", rec.Code, rec.Body.String())
	}
	w.mustPut(mcpOAuthScopesKey, defaultMCPOAuthScopes)
	// A token that carries this application's own permission words narrows
	// the list further; Keycloak's usual scopes leave it alone.
	if rec := call(accessToken(t, w.idp, resource, map[string]any{"scope": "openid supplier.read"}), "search_contracts"); !refused(rec) {
		t.Errorf("a token scoped to supplier.read reached contract.read: %d %s", rec.Code, rec.Body.String())
	}

	// The personal key goes on working beside it.
	var key struct {
		Token string `json:"key"`
	}
	body, _ := json.Marshal(map[string]any{"name": "sso-test", "scopes": []string{"supplier.read"}, "expiresInDays": 1})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/me/api-keys", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: w.admin})
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("issue key: %d %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &key)
	if !strings.HasPrefix(key.Token, "vnd_") {
		t.Fatalf("key = %q", key.Token)
	}
	if rec := w.mcp(key.Token, listTools); !w.toolsListed(rec) {
		t.Errorf("the personal key stopped working with SSO on: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAdminGuideNamesEveryMCPOAuthSettingAndTheDefaultIsOff holds the guide
// to the names an operator will look for: the four rows as the migration
// installs them, the default scope list, and the well-known path.
func TestAdminGuideNamesEveryMCPOAuthSettingAndTheDefaultIsOff(t *testing.T) {
	guide := repoFile(t, "docs/ADMIN_GUIDE.md")
	migration := repoFile(t, "internal/db/migrations/018_mcp_oauth.sql")
	for _, name := range []string{mcpOAuthEnabledKey, mcpOAuthResourceKey, mcpOAuthAudienceKey, mcpOAuthScopesKey, defaultMCPOAuthScopes, protectedResourcePath + mcpPath} {
		if !strings.Contains(guide, "`"+name+"`") && !strings.Contains(guide, name) {
			t.Errorf("the admin guide does not mention %s", name)
		}
		if !strings.Contains(migration, name) && name != protectedResourcePath+mcpPath {
			t.Errorf("migration 018 does not install %s", name)
		}
	}
	if !strings.Contains(migration, "('"+mcpOAuthEnabledKey+"','false'") {
		t.Error("migration 018 does not install the switch off")
	}
	if !strings.Contains(repoFile(t, "docs/USER_GUIDE.md"), "키 없이 SSO 로 연결") {
		t.Error("the user guide has no section on connecting without a key")
	}
}

// guards: mcpOAuthSettings.resource, mcpOAuthSettings.metadataURL
func TestTheResourceIdentifierComesFromConfigurationBeforeTheRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	r.Host = "10.0.0.7:8080"
	for name, tc := range map[string]struct {
		settings           mcpOAuthSettings
		resource, metadata string
	}{
		"written by the administrator": {mcpOAuthSettings{Resource: "https://mcp.vendra.example/mcp"}, "https://mcp.vendra.example/mcp", "https://mcp.vendra.example/.well-known/oauth-protected-resource/mcp"},
		"the public address":           {mcpOAuthSettings{oidc: oidcSettings{PublicURL: "https://vendra.example/"}}, "https://vendra.example/mcp", "https://vendra.example/.well-known/oauth-protected-resource/mcp"},
		"the request, last":            {mcpOAuthSettings{}, "http://10.0.0.7:8080/mcp", "http://10.0.0.7:8080/.well-known/oauth-protected-resource/mcp"},
	} {
		if got := tc.settings.resource(r); got != tc.resource {
			t.Errorf("%s: resource = %q, want %q", name, got, tc.resource)
		}
		if got := tc.settings.metadataURL(r); got != tc.metadata {
			t.Errorf("%s: metadata = %q, want %q", name, got, tc.metadata)
		}
	}
	if on, _ := (mcpOAuthSettings{Enabled: true}).active(); on {
		t.Error("the switch alone, without an issuer, counts as on")
	}
	if on, _ := (mcpOAuthSettings{}).active(); on {
		t.Error("absent rows count as on")
	}
	for token, want := range map[string]bool{"a.b.c": true, "vnd_x": false, "a..c": false, "a.b": false, "a.b.c.d": false} {
		if looksLikeJWT(token) != want {
			t.Errorf("looksLikeJWT(%q) = %v", token, !want)
		}
	}
}
