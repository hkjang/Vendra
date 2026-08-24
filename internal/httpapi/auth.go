package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/hkjang/Vendra/internal/security"
)

const sessionCookie = "vendra_session"

type authService struct {
	db    *pgxpool.Pool
	audit auditor
}

type sessionSettings struct {
	TTLHours     int  `json:"ttlHours"`
	SecureCookie bool `json:"secureCookie"`
}

func (a authService) sessionSettings(ctx context.Context) sessionSettings {
	var value []byte
	s := sessionSettings{TTLHours: 12}
	if a.db.QueryRow(ctx, `SELECT value FROM settings WHERE key='security.session'`).Scan(&value) == nil {
		_ = json.Unmarshal(value, &s)
	}
	if s.TTLHours < 1 || s.TTLHours > 720 {
		s.TTLHours = 12
	}
	return s
}

func bootstrapAdmin(ctx context.Context, db *pgxpool.Pool, email, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO users(email,display_name,password_hash,is_bootstrap_admin,status)
		VALUES(lower($1),$1,$2,true,'active')
		ON CONFLICT(email) DO UPDATE SET is_bootstrap_admin=true
		RETURNING id`, email, string(hash)).Scan(&userID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='system_admin' ON CONFLICT DO NOTHING`, userID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// errNoCredentials means the caller presented nothing usable, as opposed to the
// check itself being impossible. The distinction matters: reporting a database
// outage as an authentication failure signs every user out and tells anyone
// trying to sign in that their password is wrong.
var errNoCredentials = errors.New("no credentials")

func (a authService) middleware(required bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if crossOriginWrite(r) {
			slog.Warn("refused a cross-origin write carrying the session cookie",
				"origin", r.Header.Get("Origin"), "sec_fetch_site", r.Header.Get("Sec-Fetch-Site"),
				"method", r.Method, "path", r.URL.Path, "request_id", requestID(r.Context()))
			writeError(w, http.StatusForbidden, "cross_origin", "다른 사이트에서 보낸 요청은 처리하지 않습니다")
			return
		}
		p, err := a.authenticate(r)
		switch {
		case err == nil:
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
		case errors.Is(err, errNoCredentials):
			if required {
				writeError(w, http.StatusUnauthorized, "unauthenticated", "로그인이 필요합니다")
				return
			}
			next.ServeHTTP(w, r)
		default:
			logDB(err)
			w.Header().Set("Retry-After", "5")
			writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "일시적으로 로그인 상태를 확인할 수 없습니다. 잠시 후 다시 시도하세요")
		}
	})
}

func (a authService) authenticate(r *http.Request) (Principal, error) {
	ctx := r.Context()
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer vnd_") {
		token := strings.TrimPrefix(auth, "Bearer ")
		return a.fromAPIKey(ctx, token)
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return Principal{}, errNoCredentials
	}
	return a.fromSession(ctx, cookie.Value)
}

// credentialError maps a lookup result: no row means the credential is simply
// not valid, anything else means the answer is unknown.
func credentialError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errNoCredentials
	}
	return err
}

func (a authService) scanPrincipal(row pgx.Row, p *Principal, permissions *[]byte, sessionID **string) error {
	return row.Scan(&p.ID, &p.Email, &p.DisplayName, &p.UserType, &p.SupplierID, &p.OrganizationID, permissions, &p.DataScope, sessionID)
}

func (a authService) fromSession(ctx context.Context, token string) (Principal, error) {
	var p Principal
	var perms []byte
	var sid *string
	err := a.scanPrincipal(a.db.QueryRow(ctx, `
		SELECT u.id,u.email,u.display_name,u.user_type,u.supplier_id,u.organization_id,
		       COALESCE(jsonb_agg(DISTINCT permission) FILTER (WHERE permission IS NOT NULL),'[]'),
		       CASE COALESCE(max(CASE r.data_scope WHEN 'company' THEN 4 WHEN 'division' THEN 3 WHEN 'department' THEN 2 ELSE 1 END),1) WHEN 4 THEN 'company' WHEN 3 THEN 'division' WHEN 2 THEN 'department' ELSE 'own' END,s.id
		FROM sessions s JOIN users u ON u.id=s.user_id
		LEFT JOIN user_roles ur ON ur.user_id=u.id LEFT JOIN roles r ON r.id=ur.role_id
		LEFT JOIN LATERAL jsonb_array_elements_text(r.permissions) permission ON true
		WHERE s.token_hash=$1 AND s.expires_at>now() AND u.status='active'
		GROUP BY u.id,s.id`, security.TokenHash(token)), &p, &perms, &sid)
	if err != nil {
		return Principal{}, credentialError(err)
	}
	_ = json.Unmarshal(perms, &p.Permissions)
	p.SessionID = sid
	grantRows, err := a.db.Query(ctx, `SELECT permission,resource_type,resource_id,conditions FROM access_grants WHERE user_id=$1 AND valid_from<=now() AND (valid_until IS NULL OR valid_until>now())`, p.ID)
	if err != nil {
		return Principal{}, err
	}
	defer grantRows.Close()
	for grantRows.Next() {
		var permission string
		var resourceType, resourceID *string
		var encodedConditions []byte
		if err := grantRows.Scan(&permission, &resourceType, &resourceID, &encodedConditions); err != nil {
			return Principal{}, err
		}
		conditions := map[string]any{}
		_ = json.Unmarshal(encodedConditions, &conditions)
		if resourceType == nil && resourceID == nil && len(conditions) == 0 {
			p.Permissions = append(p.Permissions, permission)
			continue
		}
		grant := AccessGrant{Permission: permission, ResourceID: resourceID, Conditions: conditions}
		if resourceType != nil {
			grant.ResourceType = *resourceType
		}
		p.AccessGrants = append(p.AccessGrants, grant)
	}
	if err := grantRows.Err(); err != nil {
		return Principal{}, err
	}
	if _, err := a.db.Exec(ctx, `UPDATE sessions SET last_seen_at=now() WHERE id=$1 AND last_seen_at < now()-interval '5 minutes'`, sid); err != nil {
		logDB(err)
	}
	return p, nil
}

func (a authService) fromAPIKey(ctx context.Context, token string) (Principal, error) {
	var p Principal
	var perms []byte
	var ignored *string
	err := a.scanPrincipal(a.db.QueryRow(ctx, `
		SELECT u.id,u.email,u.display_name,u.user_type,u.supplier_id,u.organization_id,k.scopes,
		       COALESCE((SELECT CASE max(CASE r.data_scope WHEN 'company' THEN 4 WHEN 'division' THEN 3 WHEN 'department' THEN 2 ELSE 1 END) WHEN 4 THEN 'company' WHEN 3 THEN 'division' WHEN 2 THEN 'department' ELSE 'own' END FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id),'own'),NULL::text
		FROM api_keys k JOIN users u ON u.id=k.user_id
		WHERE k.key_hash=$1 AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now()) AND u.status='active'`, security.TokenHash(token)), &p, &perms, &ignored)
	if err != nil {
		return Principal{}, credentialError(err)
	}
	_ = json.Unmarshal(perms, &p.Permissions)
	var rolePermissionsJSON []byte
	if a.db.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(DISTINCT permission) FILTER(WHERE permission IS NOT NULL),'[]') FROM user_roles ur JOIN roles r ON r.id=ur.role_id LEFT JOIN LATERAL jsonb_array_elements_text(r.permissions) permission ON true WHERE ur.user_id=$1`, p.ID).Scan(&rolePermissionsJSON) == nil {
		var rolePermissions []string
		_ = json.Unmarshal(rolePermissionsJSON, &rolePermissions)
		current := Principal{Permissions: rolePermissions}
		allowedScopes := make([]string, 0, len(p.Permissions))
		for _, scope := range p.Permissions {
			if hasPermission(current, scope) {
				allowedScopes = append(allowedScopes, scope)
			}
		}
		p.Permissions = allowedScopes
	}
	if _, err := a.db.Exec(ctx, `UPDATE api_keys SET last_used_at=now() WHERE key_hash=$1`, security.TokenHash(token)); err != nil {
		logDB(err)
	}
	return p, nil
}

func (a authService) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", "이메일과 비밀번호를 확인하세요")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	ip := clientIPValue(r)
	guard := a.loginProtection(r.Context())
	now := time.Now()
	account, address, err := recentFailures(r.Context(), a.db, email, ip, guard.window())
	if err != nil {
		logDB(err)
	} else if retryAfter, locked := guard.retryAfter(account.count, account.last, guard.MaxFailures, now); locked {
		a.denyLogin(r, email, "account", account.count, retryAfter)
		writeLoginLocked(w, retryAfter)
		return
	} else if retryAfter, locked := guard.retryAfter(address.count, address.last, guard.MaxAddressFailures, now); locked {
		a.denyLogin(r, email, "address", address.count, retryAfter)
		writeLoginLocked(w, retryAfter)
		return
	}

	var userID, hash string
	switch lookupErr := a.db.QueryRow(r.Context(), `SELECT id,password_hash FROM users WHERE email=$1 AND status='active'`, email).Scan(&userID, &hash); {
	case lookupErr == nil:
	case errors.Is(lookupErr, pgx.ErrNoRows):
		// Unknown account: fall through so bcrypt still runs and the response
		// time does not reveal that the address is unregistered.
		hash = ""
	default:
		logDB(lookupErr)
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "일시적으로 로그인을 처리할 수 없습니다. 잠시 후 다시 시도하세요")
		return
	}
	// Always spend a bcrypt comparison so response timing does not reveal
	// whether the account exists.
	if !passwordMatches(hash, in.Password) {
		recordLoginAttempt(r.Context(), a.db, email, ip, r.UserAgent(), false)
		runtimeHTTPMetrics.loginFailures.Add(1)
		a.audit.recordAnonymous(r, "login_failed", "user", userID, email, map[string]any{"reason": "invalid_credentials"})
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "이메일 또는 비밀번호가 올바르지 않습니다")
		return
	}
	token, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "internal_error", "세션을 만들지 못했습니다")
		return
	}
	var sessionID string
	sessionConfig := a.sessionSettings(r.Context())
	expiresAt := now.Add(time.Duration(sessionConfig.TTLHours) * time.Hour)
	err = a.db.QueryRow(r.Context(), `INSERT INTO sessions(user_id,token_hash,ip,user_agent,expires_at) VALUES($1,$2,$3,$4,$5) RETURNING id`, userID, security.TokenHash(token), ip, r.UserAgent(), expiresAt).Scan(&sessionID)
	if err != nil {
		logDB(err)
		writeError(w, 500, "internal_error", "세션을 만들지 못했습니다")
		return
	}
	recordLoginAttempt(r.Context(), a.db, email, ip, r.UserAgent(), true)
	// A successful sign-in clears the account lockout so a legitimate owner is
	// never held behind an attacker's failed attempts.
	if _, err := a.db.Exec(r.Context(), `DELETE FROM login_attempts WHERE email=$1 AND NOT succeeded`, email); err != nil {
		logDB(err)
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE users SET last_login_at=now() WHERE id=$1`, userID); err != nil {
		logDB(err)
	}
	// Rejected sign-ins were on the record and accepted ones were not, so the
	// trail showed who was turned away but never who got in.
	a.audit.write(r, userID, email, "login", "user", userID, sessionID, nil, map[string]any{
		"userAgent": r.UserAgent(), "expiresAt": expiresAt,
	})
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: sessionConfig.SecureCookie || requestIsHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: sessionConfig.TTLHours * 3600})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a authService) denyLogin(r *http.Request, email, subject string, failures int, retryAfter time.Duration) {
	runtimeHTTPMetrics.loginLockouts.Add(1)
	slog.Warn("login locked", "subject", subject, "email", email, "failures", failures,
		"retry_after_seconds", int(retryAfter.Seconds()), "request_id", requestID(r.Context()))
	a.audit.recordAnonymous(r, "login_locked", "user", "", email, map[string]any{
		"subject": subject, "failures": failures, "retryAfterSeconds": int(retryAfter.Seconds()),
	})
}

// passwordMatches runs bcrypt even for unknown accounts, using a hash of the
// same cost, so failures take a constant amount of work.
func passwordMatches(hash, password string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(timingDecoyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// timingDecoyHash is a bcrypt.DefaultCost hash of an unguessable value.
var timingDecoyHash = func() []byte {
	secret, err := security.RandomToken(32)
	if err != nil {
		secret = "vendra-timing-decoy"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")
	}
	return hash
}()

func (a authService) logout(w http.ResponseWriter, r *http.Request) {
	// Clearing the cookie only stops this browser from sending the token. If the
	// row survives, the token still authenticates anyone who kept a copy, so a
	// failed delete must not be reported as a completed sign-out.
	if c, err := r.Cookie(sessionCookie); err == nil {
		var userID, email string
		var sessionID string
		if err := a.db.QueryRow(r.Context(), `SELECT s.id::text,u.id::text,u.email FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1`,
			security.TokenHash(c.Value)).Scan(&sessionID, &userID, &email); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			logDB(err)
		}
		if _, err := a.db.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, security.TokenHash(c.Value)); err != nil {
			logDB(err)
			w.Header().Set("Retry-After", "5")
			writeError(w, http.StatusServiceUnavailable, "logout_failed", "로그아웃을 완료하지 못했습니다. 세션이 아직 유효하니 잠시 후 다시 시도하세요")
			return
		}
		if userID != "" {
			a.audit.write(r, userID, email, "logout", "user", userID, sessionID, nil, nil)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func require(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := principalFrom(r.Context())
		if !ok {
			writeError(w, 401, "unauthenticated", "로그인이 필요합니다")
			return
		}
		grantAccess, grantNamesRecord := hasGrantPermission(p, permission, r)
		if !hasPermission(p, permission) && !grantAccess {
			writeError(w, 403, "forbidden", fmt.Sprintf("%s 권한이 필요합니다", permission))
			return
		}
		if grantNamesRecord {
			r = r.WithContext(context.WithValue(r.Context(), grantAuthorizationKey, true))
		}
		next(w, r)
	}
}

func randomToken(n int) (string, error) { return security.RandomToken(n) }

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func expiry(days int) *time.Time {
	if days <= 0 {
		return nil
	}
	t := time.Now().Add(time.Duration(days) * 24 * time.Hour)
	return &t
}
