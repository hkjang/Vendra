package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/hkjang/Vendra/internal/security"
)

const sessionCookie = "vendra_session"

type authService struct{ db *pgxpool.Pool }

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

func (a authService) middleware(required bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.authenticate(r)
		if !ok {
			if required {
				writeError(w, http.StatusUnauthorized, "unauthenticated", "로그인이 필요합니다")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

func (a authService) authenticate(r *http.Request) (Principal, bool) {
	ctx := r.Context()
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer vnd_") {
		token := strings.TrimPrefix(auth, "Bearer ")
		return a.fromAPIKey(ctx, token)
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return Principal{}, false
	}
	return a.fromSession(ctx, cookie.Value)
}

func (a authService) scanPrincipal(row pgx.Row, p *Principal, permissions *[]byte, sessionID **string) error {
	return row.Scan(&p.ID, &p.Email, &p.DisplayName, &p.UserType, &p.SupplierID, &p.OrganizationID, permissions, &p.DataScope, sessionID)
}

func (a authService) fromSession(ctx context.Context, token string) (Principal, bool) {
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
		return Principal{}, false
	}
	_ = json.Unmarshal(perms, &p.Permissions)
	p.SessionID = sid
	grantRows, err := a.db.Query(ctx, `SELECT permission,resource_type,resource_id,conditions FROM access_grants WHERE user_id=$1 AND valid_from<=now() AND (valid_until IS NULL OR valid_until>now())`, p.ID)
	if err == nil {
		for grantRows.Next() {
			var permission string
			var resourceType, resourceID *string
			var encodedConditions []byte
			if grantRows.Scan(&permission, &resourceType, &resourceID, &encodedConditions) == nil {
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
		}
		grantRows.Close()
	}
	_, _ = a.db.Exec(ctx, `UPDATE sessions SET last_seen_at=now() WHERE id=$1 AND last_seen_at < now()-interval '5 minutes'`, sid)
	return p, true
}

func (a authService) fromAPIKey(ctx context.Context, token string) (Principal, bool) {
	var p Principal
	var perms []byte
	var ignored *string
	err := a.scanPrincipal(a.db.QueryRow(ctx, `
		SELECT u.id,u.email,u.display_name,u.user_type,u.supplier_id,u.organization_id,k.scopes,
		       COALESCE((SELECT CASE max(CASE r.data_scope WHEN 'company' THEN 4 WHEN 'division' THEN 3 WHEN 'department' THEN 2 ELSE 1 END) WHEN 4 THEN 'company' WHEN 3 THEN 'division' WHEN 2 THEN 'department' ELSE 'own' END FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id),'own'),NULL::text
		FROM api_keys k JOIN users u ON u.id=k.user_id
		WHERE k.key_hash=$1 AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>now()) AND u.status='active'`, security.TokenHash(token)), &p, &perms, &ignored)
	if err != nil {
		return Principal{}, false
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
	_, _ = a.db.Exec(ctx, `UPDATE api_keys SET last_used_at=now() WHERE key_hash=$1`, security.TokenHash(token))
	return p, true
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
	var userID, hash string
	err := a.db.QueryRow(r.Context(), `SELECT id,password_hash FROM users WHERE email=lower($1) AND status='active'`, strings.TrimSpace(in.Email)).Scan(&userID, &hash)
	if err != nil || hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "이메일 또는 비밀번호가 올바르지 않습니다")
		return
	}
	token, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "internal_error", "세션을 만들지 못했습니다")
		return
	}
	ip := netip.Addr{}
	if host := strings.Split(r.RemoteAddr, ":")[0]; host != "" {
		ip, _ = netip.ParseAddr(host)
	}
	var sessionID string
	sessionConfig := a.sessionSettings(r.Context())
	expiresAt := time.Now().Add(time.Duration(sessionConfig.TTLHours) * time.Hour)
	err = a.db.QueryRow(r.Context(), `INSERT INTO sessions(user_id,token_hash,ip,user_agent,expires_at) VALUES($1,$2,$3,$4,$5) RETURNING id`, userID, security.TokenHash(token), ip, r.UserAgent(), expiresAt).Scan(&sessionID)
	if err != nil {
		writeError(w, 500, "internal_error", "세션을 만들지 못했습니다")
		return
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE users SET last_login_at=now() WHERE id=$1`, userID)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: sessionConfig.SecureCookie || requestIsHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: sessionConfig.TTLHours * 3600})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a authService) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_, _ = a.db.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash=$1`, security.TokenHash(c.Value))
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
		grantAccess := hasGrantPermission(p, permission, r)
		if !hasPermission(p, permission) && !grantAccess {
			writeError(w, 403, "forbidden", fmt.Sprintf("%s 권한이 필요합니다", permission))
			return
		}
		if grantAccess {
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
