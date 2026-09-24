package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
)

// MCP 를 SSO 로 — 개인 키 없이, Keycloak 이 발급한 토큰으로.
//
// The MCP authorization specification (2025-06-18 and later) is OAuth 2.1:
// the MCP server is a resource server that publishes where its authorization
// server is (RFC 9728, /.well-known/oauth-protected-resource), and a client
// refused with 401 reads that document, sends the person through the
// authorization server with PKCE, and comes back with an access token whose
// audience (RFC 8707) is this server. Nothing about issuing tokens happens
// here — Keycloak does that, with the realm the web sign-in already uses —
// and this file only answers two questions: where is the authorization
// server, and is this token one it issued for us.
//
// The personal key stays. It is what an automation with nobody behind it
// uses, and what a deployment without Keycloak uses. A token from SSO is a
// second door into the same room, and only on /mcp: it authenticates an
// existing, active Vendra account, carries the read scopes the administrator
// chose, and is held to that account's role at every request exactly as a
// key is. It never creates an account — signing in to the web once is what
// registers one, and a program presenting a token is not the moment to decide
// who somebody is — and it never reads a role out of the token.

const (
	mcpPath = "/mcp"
	// protectedResourcePath is the RFC 9728 document. It is served bare, with
	// and without the /mcp suffix, because clients try both.
	protectedResourcePath = "/.well-known/oauth-protected-resource"

	mcpOAuthEnabledKey  = "mcp.oauth.enabled"
	mcpOAuthResourceKey = "mcp.oauth.resource"
	mcpOAuthAudienceKey = "mcp.oauth.audience"
	mcpOAuthScopesKey   = "mcp.oauth.scopes"
	// defaultMCPOAuthScopes is what migration 018 installs: the seven read
	// permissions the MCP tools check and nothing that uncovers an amount.
	defaultMCPOAuthScopes = "supplier.read contract.read purchase_order.read issue.read risk.read evaluation.read spend.read"
)

// isMCPPath is the one place OAuth tokens are read and 401s carry a pointer
// to the authorization server. REST, the portal and the administration API
// stay on keys and sessions: a token that opened the whole application would
// make this "a new door into the API", not "MCP without a key".
func isMCPPath(path string) bool {
	return path == mcpPath || strings.HasPrefix(path, mcpPath+"/")
}

// looksLikeJWT is the cheap shape test that separates a bearer that could be
// an access token from one that is nothing this server accepts. Three
// non-empty dot-separated segments; anything else is answered as before.
func looksLikeJWT(token string) bool {
	parts := strings.Split(token, ".")
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}

// asymmetricAlgorithms are the signatures a Keycloak realm key produces. An
// HS* token is signed with a shared secret this server does not hold — a
// refresh token, or a forgery using the public key as the secret — and none
// is not a signature at all.
var asymmetricAlgorithms = []string{oidc.RS256, oidc.RS384, oidc.RS512, oidc.ES256, oidc.ES384, oidc.ES512, oidc.PS256, oidc.PS384, oidc.PS512}

type mcpOAuthSettings struct {
	Enabled bool
	// The authorization server and the address browsers reach this service
	// at are the web sign-in's (the oidc row); they are read, never repeated.
	oidc oidcSettings
	// Resource is the identifier this server claims (RFC 8707): what the
	// metadata advertises and what a token's aud may name. Empty means it is
	// built from the public address, or as a last resort from the request.
	Resource string
	// Audiences are the aud or azp values an administrator accepts beside
	// the resource — in practice the MCP client's id, because Keycloak 26
	// puts the client in azp and only "account" in aud unless a mapper says
	// otherwise.
	Audiences []string
	// Scopes are the permissions a token holder gets, stated once here rather
	// than taught to Keycloak. Held to the holder's role like a key's scopes.
	Scopes []string
}

// mcpOAuthSettings reads the four rows and the oidc row they lean on. A row
// that is missing reads as its installed default, so a database migrated
// without 018 is a database with SSO for MCP off.
func (a authService) mcpOAuthSettings(ctx context.Context) (mcpOAuthSettings, error) {
	rows, err := a.db.Query(ctx, `SELECT key,value FROM settings WHERE key='oidc' OR key LIKE 'mcp.oauth.%'`)
	if err != nil {
		return mcpOAuthSettings{}, err
	}
	defer rows.Close()
	s := mcpOAuthSettings{Scopes: strings.Fields(defaultMCPOAuthScopes)}
	for rows.Next() {
		var key string
		var value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return mcpOAuthSettings{}, err
		}
		switch key {
		case "oidc":
			_ = json.Unmarshal(value, &s.oidc)
		case mcpOAuthEnabledKey:
			_ = json.Unmarshal(value, &s.Enabled)
		case mcpOAuthResourceKey:
			s.Resource = strings.TrimSpace(settingString(value))
		case mcpOAuthAudienceKey:
			s.Audiences = strings.Fields(settingString(value))
		case mcpOAuthScopesKey:
			if scopes := strings.Fields(settingString(value)); len(scopes) > 0 {
				s.Scopes = scopes
			}
		}
	}
	return s, rows.Err()
}

// settingString reads a row whose value is a JSON string. Anything else —
// the row edited by hand into a number or an object — reads as empty rather
// than as the JSON text.
func settingString(value []byte) string {
	var s string
	_ = json.Unmarshal(value, &s)
	return s
}

// issuer is the authorization server, as the metadata names it and as a
// token's iss must spell it.
func (s mcpOAuthSettings) issuer() string {
	return strings.TrimRight(strings.TrimSpace(s.oidc.Issuer), "/")
}

// active reports whether SSO tokens are accepted at all, and if not, why.
// "On" needs the switch and an issuer; without the issuer there is nothing to
// verify against, so the switch alone leaves everything as it was and says so
// in the log rather than advertising an authorization server that is not
// there — a client that reads metadata and then has its token refused loops.
func (s mcpOAuthSettings) active() (bool, string) {
	if !s.Enabled {
		return false, "mcp.oauth.enabled is off"
	}
	if s.issuer() == "" {
		return false, "mcp.oauth.enabled is on but oidc.issuer is empty"
	}
	return true, ""
}

// resource is the identifier this deployment claims for its MCP endpoint. In
// order of trust: what the administrator wrote, the public address the web
// sign-in is configured with plus /mcp, and only when both are empty the
// request — anybody can set a Host header, which is why that is last.
func (s mcpOAuthSettings) resource(r *http.Request) string {
	if s.Resource != "" {
		return s.Resource
	}
	if base := strings.TrimRight(strings.TrimSpace(s.oidc.PublicURL), "/"); base != "" {
		if parsed, err := url.Parse(base); err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			return parsed.Scheme + "://" + parsed.Host + parsed.Path + mcpPath
		}
	}
	return requestOrigin(r) + mcpPath
}

// metadataURL is where a refused client is sent to learn the above: the
// well-known document on the resource's own origin.
func (s mcpOAuthSettings) metadataURL(r *http.Request) string {
	resource := s.resource(r)
	origin := strings.TrimSuffix(resource, mcpPath)
	if parsed, err := url.Parse(resource); err == nil && parsed.Host != "" {
		origin = parsed.Scheme + "://" + parsed.Host
	}
	return origin + protectedResourcePath + mcpPath
}

// oauthProviders caches discovery per issuer. Discovery is a round trip to
// Keycloak and the key set behind it verifies every token; doing that once
// per request would put Keycloak's latency in front of every MCP call.
// go-oidc refetches the key set on an unknown key id, so rotation needs no
// invalidation here. The provider outlives the request that first built it,
// so the context it keeps must too.
type oauthProviders struct {
	mu       sync.Mutex
	byIssuer map[string]*oidc.Provider
}

func (c *oauthProviders) provider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	discover := func() (*oidc.Provider, error) {
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		return oidc.NewProvider(oidc.ClientContext(context.WithoutCancel(ctx), client), issuer)
	}
	if c == nil {
		return discover()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if provider := c.byIssuer[issuer]; provider != nil {
		return provider, nil
	}
	provider, err := discover()
	if err != nil {
		return nil, err
	}
	if c.byIssuer == nil {
		c.byIssuer = map[string]*oidc.Provider{}
	}
	c.byIssuer[issuer] = provider
	return provider, nil
}

// oauthRefusal is why a bearer that had the shape of a token was turned away.
// It is an errNoCredentials — the middleware answers 401 — that also carries
// the sentence the client is shown, because an operator finishes the Keycloak
// side of this from that sentence alone.
type oauthRefusal struct {
	message string
	detail  string
}

func (e oauthRefusal) Error() string      { return "mcp oauth: " + e.detail }
func (e oauthRefusal) Unwrap() error      { return errNoCredentials }
func refuse(message, detail string) error { return oauthRefusal{message: message, detail: detail} }

// fromOAuthToken turns a bearer access token into a principal, or says
// exactly why it will not. While SSO for MCP is off the answer is the one a
// token-shaped bearer always got — no new words leak from an installation
// that has not turned this on.
func (a authService) fromOAuthToken(ctx context.Context, r *http.Request, token string) (Principal, error) {
	s, err := a.mcpOAuthSettings(ctx)
	if err != nil {
		return Principal{}, err
	}
	if on, reason := s.active(); !on {
		if s.Enabled {
			slog.Warn("mcp oauth token refused", "reason", reason, "request_id", requestID(ctx))
		}
		return Principal{}, errNoCredentials
	}
	provider, err := a.oauth.provider(ctx, s.issuer())
	if err != nil {
		// Not a refusal: the token may be perfectly good and the answer is
		// unknown. Reporting that as 401 would send every client back to
		// sign in, which is the same mistake as reporting a database outage
		// as a wrong password.
		return Principal{}, fmt.Errorf("mcp oauth discovery at %s: %w", s.issuer(), err)
	}
	// Signature, issuer and expiry. The audience is checked by hand below
	// because more than one value is acceptable and the library compares one,
	// and it does not know azp at all.
	verified, err := provider.Verifier(&oidc.Config{SkipClientIDCheck: true, SupportedSigningAlgs: asymmetricAlgorithms}).Verify(ctx, token)
	if err != nil {
		return Principal{}, refuse(tokenRefusalMessage(err, s.issuer()), "token rejected: "+err.Error())
	}
	var claims struct {
		Type              string          `json:"typ"`
		ClientID          string          `json:"azp"`
		Scope             string          `json:"scope"`
		NotBefore         int64           `json:"nbf"`
		Confirmation      json.RawMessage `json:"cnf"`
		Email             string          `json:"email"`
		EmailVerified     bool            `json:"email_verified"`
		PreferredUsername string          `json:"preferred_username"`
	}
	if err := verified.Claims(&claims); err != nil {
		return Principal{}, refuse("SSO 토큰의 내용을 읽을 수 없습니다.", "claims unreadable: "+err.Error())
	}
	switch {
	case claims.Type == "ID":
		// An ID token proves a sign-in happened; it is not a credential for
		// an API, and Keycloak marks the two apart for exactly this check.
		return Principal{}, refuse("ID 토큰은 로그인 증거이지 API 자격이 아닙니다. 액세스 토큰을 보내세요.", "typ=ID")
	case claims.Type != "" && claims.Type != "Bearer":
		return Principal{}, refuse(fmt.Sprintf("typ=%q 토큰은 받지 않습니다. 액세스 토큰(typ=Bearer)을 보내세요.", claims.Type), "typ="+claims.Type)
	case claims.NotBefore > time.Now().Unix():
		return Principal{}, refuse("아직 유효하지 않은 토큰입니다(nbf). 시계를 확인하세요.", "nbf in the future")
	case len(claims.Confirmation) > 0 && string(claims.Confirmation) != "null":
		// cnf binds the token to a key (DPoP, mTLS) this server cannot check.
		// Accepting it would be accepting a token its own issuer said not to
		// accept from a mere bearer.
		return Principal{}, refuse("소지자 증명(cnf)이 묶인 토큰은 이 서버가 검증할 수 없습니다. 일반 Bearer 액세스 토큰을 보내세요.", "cnf present")
	case strings.TrimSpace(verified.Subject) == "":
		return Principal{}, refuse("SSO 토큰에 사용자 식별자(sub)가 없습니다.", "sub empty")
	}
	// Whom the token was minted for. Measured against a real Keycloak 26: an
	// access token issued to a client carries that client in azp and
	// aud:["account"] — the client id is not in aud, whatever an ID token
	// does. So the binding is "aud names this resource, or aud/azp is on the
	// administrator's list". Either is the token being for this deployment
	// rather than one somebody obtained from another application in the same
	// realm, which is what RFC 8707 and the MCP specification guard against.
	resource := s.resource(r)
	bound := slices.Clone(verified.Audience)
	if claims.ClientID != "" {
		bound = append(bound, claims.ClientID)
	}
	accepted := slices.Contains(verified.Audience, resource) ||
		slices.ContainsFunc(bound, func(value string) bool { return slices.Contains(s.Audiences, value) })
	if !accepted {
		want := claims.ClientID
		if want == "" {
			want = "MCP 클라이언트 ID"
		}
		return Principal{}, refuse(fmt.Sprintf("SSO 토큰이 이 서버를 위해 발급된 것이 아닙니다(aud=%v, azp=%q). 관리자가 허용 대상(%s)에 %q 를 적거나, Keycloak 클라이언트의 Audience 매퍼에 %q 를 넣어야 합니다.",
			verified.Audience, claims.ClientID, mcpOAuthAudienceKey, want, resource),
			fmt.Sprintf("audience %v / azp %q not accepted", verified.Audience, claims.ClientID))
	}
	// The same lookup the web sign-in makes, without the provisioning half:
	// the linked subject first, else the address — and the address only under
	// the same rule the callback applies to it, because an identity provider
	// that lets a person set an unverified address would otherwise hand out
	// any existing account to whoever claims its address.
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if !oidcEmailClaimTrusted(s.oidc, false, claims.EmailVerified) {
		email = ""
	}
	var p Principal
	var perms []byte
	var ignored *string
	err = a.scanPrincipal(a.db.QueryRow(ctx, `
		SELECT u.id,u.email,u.display_name,u.user_type,u.supplier_id,u.organization_id,$3::jsonb,
		       COALESCE((SELECT CASE max(CASE r.data_scope WHEN 'company' THEN 4 WHEN 'division' THEN 3 WHEN 'department' THEN 2 ELSE 1 END) WHEN 4 THEN 'company' WHEN 3 THEN 'division' WHEN 2 THEN 'department' ELSE 'own' END FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id),'own'),NULL::text
		FROM users u
		WHERE (u.oidc_subject=$1 OR ($2<>'' AND u.email=$2)) AND u.status='active'
		ORDER BY u.oidc_subject=$1 DESC LIMIT 1`, verified.Subject, email, raw(s.Scopes)), &p, &perms, &ignored)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, refuse("이 SSO 계정은 Vendra 에 등록되지 않았거나 비활성입니다. 먼저 웹으로 한 번 로그인하세요.", "no active account for subject")
	}
	if err != nil {
		return Principal{}, err
	}
	_ = json.Unmarshal(perms, &p.Permissions)
	// A token that carries this application's own permission words narrows
	// the administrator's list further; the usual "openid profile email"
	// carries none and leaves it alone.
	if words := vendraScopeWords(claims.Scope); len(words) > 0 {
		p.Permissions = intersectPermissions(p.Permissions, words)
	}
	if err := a.limitToRolePermissions(ctx, &p); err != nil {
		logDB(err)
	}
	return p, nil
}

// tokenRefusalMessage turns the library's reason into the sentence an operator
// acts on: which of signature, issuer and expiry it was.
func tokenRefusalMessage(err error, issuer string) string {
	var expired *oidc.TokenExpiredError
	text := err.Error()
	switch {
	case errors.As(err, &expired):
		return "SSO 액세스 토큰이 만료되었습니다. 클라이언트에서 다시 로그인하세요."
	case strings.Contains(text, "different provider"):
		return fmt.Sprintf("SSO 토큰의 발급자(iss)가 이 서버의 OIDC Issuer(%s)와 다릅니다.", issuer)
	case strings.Contains(text, "unexpected signature algorithm"):
		return "SSO 토큰의 서명 알고리즘을 받지 않습니다. Keycloak 이 RS/ES/PS 계열로 서명한 액세스 토큰이어야 합니다."
	case strings.Contains(text, "nbf"):
		return "아직 유효하지 않은 토큰입니다(nbf). 시계를 확인하세요."
	default:
		return "SSO 액세스 토큰의 서명을 확인할 수 없습니다. 클라이언트에서 다시 로그인하세요."
	}
}

// vendraScopeWords picks out of a token's scope claim the words that are
// permissions this application checks. Keycloak's own scopes are not.
func vendraScopeWords(scope string) []string {
	var words []string
	for _, word := range strings.Fields(scope) {
		if permissionGrantsSomething(word) {
			words = append(words, word)
		}
	}
	return words
}

// intersectPermissions keeps the permissions of the first list that the
// second list also grants.
func intersectPermissions(permissions, granted []string) []string {
	holder := Principal{Permissions: granted}
	kept := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		if hasPermission(holder, permission) {
			kept = append(kept, permission)
		}
	}
	return kept
}

// limitToRolePermissions cuts a principal's permissions down to what the
// account's roles grant today. A key's scopes and an SSO token's scopes were
// both fixed at a moment before now; the role is what is true now.
func (a authService) limitToRolePermissions(ctx context.Context, p *Principal) error {
	var rolePermissionsJSON []byte
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(DISTINCT permission) FILTER(WHERE permission IS NOT NULL),'[]') FROM user_roles ur JOIN roles r ON r.id=ur.role_id LEFT JOIN LATERAL jsonb_array_elements_text(r.permissions) permission ON true WHERE ur.user_id=$1`, p.ID).Scan(&rolePermissionsJSON); err != nil {
		return err
	}
	var rolePermissions []string
	_ = json.Unmarshal(rolePermissionsJSON, &rolePermissions)
	p.Permissions = intersectPermissions(p.Permissions, rolePermissions)
	return nil
}

// mcpChallenge is the 401 on the MCP path. With SSO for MCP on it carries the
// WWW-Authenticate header that turns a refusal into an invitation — the MCP
// client reads resource_metadata and starts the OAuth flow from there; without
// the header a refusal is a dead end. It is set on this path only: a REST 401
// carrying it sends browsers and other clients somewhere they cannot go.
func (a authService) mcpChallenge(w http.ResponseWriter, r *http.Request, err error) {
	presented := r.Header.Get("Authorization") != ""
	if s, loadErr := a.mcpOAuthSettings(r.Context()); loadErr == nil {
		if on, _ := s.active(); on {
			header := fmt.Sprintf(`Bearer realm="Vendra", resource_metadata=%q`, s.metadataURL(r))
			if presented {
				header += `, error="invalid_token"`
			}
			w.Header().Set("WWW-Authenticate", header)
		}
	}
	var refusal oauthRefusal
	if errors.As(err, &refusal) {
		slog.Warn("mcp oauth token refused", "detail", refusal.detail, "request_id", requestID(r.Context()))
		writeError(w, http.StatusUnauthorized, "invalid_token", refusal.message)
		return
	}
	writeError(w, http.StatusUnauthorized, "unauthenticated", "로그인이 필요합니다")
}

// protectedResourceMetadata is RFC 9728: the document a refused MCP client
// reads to find the authorization server. Public by design — it says where
// to sign in, not who is signed in — and bare, because the reader is an
// OAuth client library that knows nothing about this product's envelope.
func (a *App) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	s, err := a.auth.mcpOAuthSettings(r.Context())
	if err != nil {
		logDB(err)
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "일시적으로 설정을 읽을 수 없습니다")
		return
	}
	if on, _ := s.active(); !on {
		writeError(w, http.StatusNotFound, "mcp_oauth_disabled", "이 서버의 MCP 는 SSO 토큰을 받지 않습니다. 개인 API 키(vnd_)를 사용하세요.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":                 s.resource(r),
		"authorization_servers":    []string{s.issuer()},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         s.Scopes,
		"resource_name":            "Vendra MCP",
	})
}

// validMCPOAuthSetting checks one of the four rows as it is written and
// answers it in the shape the reader expects. A rejected row names itself and
// what it would take; a stored mistake would surface as a refused token
// hours later, in a client that shows nothing.
func (a *App) validMCPOAuthSetting(ctx context.Context, key string, value any) (any, error) {
	text := func() (string, bool) { s, ok := value.(string); return s, ok }
	switch key {
	case mcpOAuthEnabledKey:
		on, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%s 는 true 또는 false 여야 합니다", key)
		}
		if on {
			s, err := a.loadOIDC(ctx)
			if err != nil || strings.TrimSpace(s.Issuer) == "" {
				return nil, fmt.Errorf("%s 를 켜려면 먼저 OIDC 의 Issuer URL 을 설정하세요. 토큰을 검증할 발급자가 없습니다", key)
			}
		}
		return on, nil
	case mcpOAuthResourceKey:
		s, ok := text()
		if !ok {
			return nil, fmt.Errorf("%s 는 문자열이어야 합니다", key)
		}
		s = strings.TrimSpace(s)
		if s != "" {
			parsed, err := url.Parse(s)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return nil, fmt.Errorf("%s 는 https://host/mcp 모양의 주소여야 합니다", key)
			}
		}
		return s, nil
	case mcpOAuthAudienceKey:
		s, ok := text()
		if !ok {
			return nil, fmt.Errorf("%s 는 공백으로 구분한 문자열이어야 합니다", key)
		}
		return strings.Join(strings.Fields(s), " "), nil
	case mcpOAuthScopesKey:
		s, ok := text()
		if !ok {
			return nil, fmt.Errorf("%s 는 공백으로 구분한 권한 목록이어야 합니다", key)
		}
		scopes := strings.Fields(s)
		if len(scopes) == 0 {
			return nil, fmt.Errorf("%s 에는 권한이 하나 이상 있어야 합니다", key)
		}
		for _, scope := range scopes {
			if !permissionGrantsSomething(scope) {
				return nil, fmt.Errorf("%s 의 %q%s 이 시스템이 확인하지 않는 권한입니다. %s", key, scope, topicParticle(scope), permissionSuggestion(scope))
			}
		}
		return strings.Join(scopes, " "), nil
	}
	return nil, fmt.Errorf("%s 는 알 수 없는 설정 키입니다", key)
}
