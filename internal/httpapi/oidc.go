package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/hkjang/Vendra/internal/security"
)

type oidcSettings struct {
	Enabled     bool     `json:"enabled"`
	Issuer      string   `json:"issuer"`
	ClientID    string   `json:"clientId"`
	Scopes      []string `json:"scopes"`
	AutoCreate  bool     `json:"autoCreate"`
	DefaultRole string   `json:"defaultRole"`
	// RequireVerifiedEmail is a pointer so an absent key means "on". It gates
	// the two paths that trust the provider's email claim: attaching an identity
	// to an account that already exists, and creating a new one.
	RequireVerifiedEmail *bool  `json:"requireVerifiedEmail"`
	ClientSecret         string `json:"-"`
}

func (s oidcSettings) requiresVerifiedEmail() bool {
	return s.RequireVerifiedEmail == nil || *s.RequireVerifiedEmail
}

// oidcEmailClaimTrusted reports whether the email claim may be used to decide
// which account the caller gets. An already-linked subject identifies the
// account by itself, so its email needs no verification.
func oidcEmailClaimTrusted(s oidcSettings, alreadyLinked, emailVerified bool) bool {
	return alreadyLinked || emailVerified || !s.requiresVerifiedEmail()
}

type oidcFlow struct {
	State     string `json:"state"`
	Nonce     string `json:"nonce"`
	Verifier  string `json:"verifier"`
	ReturnTo  string `json:"returnTo"`
	ExpiresAt int64  `json:"expiresAt"`
}

func (a *App) loadOIDC(ctx context.Context) (oidcSettings, error) {
	var rawValue []byte
	var cipher *string
	if err := a.db.QueryRow(ctx, `SELECT value,secret_value FROM settings WHERE key='oidc'`).Scan(&rawValue, &cipher); err != nil {
		return oidcSettings{}, err
	}
	var s oidcSettings
	if err := json.Unmarshal(rawValue, &s); err != nil {
		return s, err
	}
	if cipher != nil {
		secret, err := a.vault.Decrypt(*cipher)
		if err != nil {
			return s, err
		}
		s.ClientSecret = secret
	}
	return s, nil
}

func (a *App) oidcPublicConfig(w http.ResponseWriter, r *http.Request) {
	s, err := a.loadOIDC(r.Context())
	if err != nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": s.Enabled && s.Issuer != "" && s.ClientID != "", "issuer": s.Issuer})
}

func (a *App) oidcStart(w http.ResponseWriter, r *http.Request) {
	s, err := a.loadOIDC(r.Context())
	if err != nil || !s.Enabled || s.Issuer == "" || s.ClientID == "" {
		writeError(w, 404, "oidc_disabled", "OIDC 로그인이 설정되지 않았습니다")
		return
	}
	provider, err := oidc.NewProvider(r.Context(), s.Issuer)
	if err != nil {
		writeError(w, 502, "oidc_unavailable", "OIDC 공급자에 연결할 수 없습니다")
		return
	}
	state, _ := randomToken(24)
	nonce, _ := randomToken(24)
	verifier, _ := randomToken(32)
	returnTo := r.URL.Query().Get("returnTo")
	if !strings.HasPrefix(returnTo, "/") {
		returnTo = "/"
	}
	flow := oidcFlow{State: state, Nonce: nonce, Verifier: verifier, ReturnTo: returnTo, ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	b, _ := json.Marshal(flow)
	encrypted, err := a.vault.Encrypt(string(b))
	if err != nil {
		writeError(w, 500, "oidc_error", "OIDC 요청을 시작하지 못했습니다")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "vendra_oidc_flow", Value: encrypted, Path: "/api/auth/oidc/callback", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	redirectURI := requestOrigin(r) + "/api/auth/oidc/callback"
	cfg := oauth2.Config{ClientID: s.ClientID, ClientSecret: s.ClientSecret, Endpoint: provider.Endpoint(), RedirectURL: redirectURI, Scopes: s.Scopes}
	challenge := sha256.Sum256([]byte(verifier))
	url := cfg.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])), oauth2.SetAuthURLParam("code_challenge_method", "S256"))
	http.Redirect(w, r, url, http.StatusFound)
}

func (a *App) oidcCallback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("vendra_oidc_flow")
	if err != nil {
		writeError(w, 400, "oidc_flow_missing", "OIDC 로그인 상태를 찾을 수 없습니다")
		return
	}
	plain, err := a.vault.Decrypt(cookie.Value)
	if err != nil {
		writeError(w, 400, "oidc_flow_invalid", "OIDC 로그인 상태가 올바르지 않습니다")
		return
	}
	var flow oidcFlow
	if json.Unmarshal([]byte(plain), &flow) != nil || flow.ExpiresAt < time.Now().Unix() || flow.State != r.URL.Query().Get("state") {
		writeError(w, 400, "oidc_flow_invalid", "OIDC 로그인 요청이 만료되었거나 올바르지 않습니다")
		return
	}
	if providerErr := r.URL.Query().Get("error"); providerErr != "" {
		writeError(w, 401, "oidc_rejected", providerErr)
		return
	}
	s, err := a.loadOIDC(r.Context())
	if err != nil || !s.Enabled {
		writeError(w, 401, "oidc_disabled", "OIDC 로그인이 비활성화되었습니다")
		return
	}
	provider, err := oidc.NewProvider(r.Context(), s.Issuer)
	if err != nil {
		writeError(w, 502, "oidc_unavailable", "OIDC 공급자에 연결할 수 없습니다")
		return
	}
	cfg := oauth2.Config{ClientID: s.ClientID, ClientSecret: s.ClientSecret, Endpoint: provider.Endpoint(), RedirectURL: requestOrigin(r) + "/api/auth/oidc/callback", Scopes: s.Scopes}
	token, err := cfg.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.SetAuthURLParam("code_verifier", flow.Verifier))
	if err != nil {
		writeError(w, 401, "oidc_exchange_failed", "OIDC 인증 코드를 교환하지 못했습니다")
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		writeError(w, 401, "oidc_token_missing", "ID 토큰이 없습니다")
		return
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: s.ClientID}).Verify(r.Context(), rawID)
	if err != nil {
		writeError(w, 401, "oidc_token_invalid", "ID 토큰 검증에 실패했습니다")
		return
	}
	if idToken.Nonce != flow.Nonce {
		writeError(w, 401, "oidc_nonce_invalid", "OIDC nonce가 올바르지 않습니다")
		return
	}
	var claims struct {
		Subject           string `json:"sub"`
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		EmailVerified     bool   `json:"email_verified"`
	}
	if idToken.Claims(&claims) != nil || claims.Subject == "" {
		writeError(w, 401, "oidc_claims_invalid", "OIDC 사용자 정보를 읽을 수 없습니다")
		return
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(claims.PreferredUsername))
	}
	if email == "" {
		writeError(w, 401, "oidc_email_missing", "OIDC 공급자가 이메일을 제공하지 않았습니다")
		return
	}
	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	// A subject that is already linked identifies the account on its own, so the
	// email claim carries no authority there and needs no verification.
	var userID string
	linked := a.db.QueryRow(r.Context(), `SELECT id FROM users WHERE oidc_subject=$1`, claims.Subject).Scan(&userID) == nil
	if !linked {
		// Everything below decides who the caller is from the email claim. An
		// identity provider that lets a user set an unverified address would
		// otherwise hand out any existing account, including an administrator's,
		// to whoever claims its address.
		if !oidcEmailClaimTrusted(s, linked, claims.EmailVerified) {
			slog.Warn("rejected OIDC sign-in with an unverified email",
				"email", email, "subject", claims.Subject, "request_id", requestID(r.Context()))
			a.audit.recordAnonymous(r, "oidc_unverified_email", "user", "", email, map[string]any{"subject": claims.Subject})
			writeError(w, 403, "oidc_email_unverified", "이메일이 검증되지 않은 계정으로는 로그인할 수 없습니다")
			return
		}
		if a.db.QueryRow(r.Context(), `SELECT id FROM users WHERE email=$1`, email).Scan(&userID) != nil && !s.AutoCreate {
			writeError(w, 403, "oidc_user_missing", "등록된 사용자만 로그인할 수 있습니다")
			return
		}
	}
	if userID == "" {
		tx, e := a.db.Begin(r.Context())
		if e != nil {
			writeError(w, 500, "database_error", "사용자를 만들지 못했습니다")
			return
		}
		defer tx.Rollback(r.Context())
		e = tx.QueryRow(r.Context(), `INSERT INTO users(email,display_name,user_type,status,oidc_subject) VALUES($1,$2,'internal','active',$3) ON CONFLICT(email) DO UPDATE SET oidc_subject=excluded.oidc_subject,updated_at=now() RETURNING id`, email, name, claims.Subject).Scan(&userID)
		if e == nil {
			_, e = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code=$2 ON CONFLICT DO NOTHING`, userID, s.DefaultRole)
		}
		if e != nil || tx.Commit(r.Context()) != nil {
			writeError(w, 500, "database_error", "사용자를 만들지 못했습니다")
			return
		}
	} else {
		_, _ = a.db.Exec(r.Context(), `UPDATE users SET oidc_subject=COALESCE(oidc_subject,$2),last_login_at=now() WHERE id=$1`, userID, claims.Subject)
	}
	sessionToken, _ := randomToken(32)
	sessionConfig := a.auth.sessionSettings(r.Context())
	expiresAt := time.Now().Add(time.Duration(sessionConfig.TTLHours) * time.Hour)
	_, err = a.db.Exec(r.Context(), `INSERT INTO sessions(user_id,token_hash,user_agent,expires_at) VALUES($1,$2,$3,$4)`, userID, security.TokenHash(sessionToken), r.UserAgent(), expiresAt)
	if err != nil {
		writeError(w, 500, "session_error", "세션을 만들지 못했습니다")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sessionToken, Path: "/", HttpOnly: true, Secure: sessionConfig.SecureCookie || requestIsHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: sessionConfig.TTLHours * 3600})
	http.SetCookie(w, &http.Cookie{Name: "vendra_oidc_flow", Value: "", Path: "/api/auth/oidc/callback", HttpOnly: true, MaxAge: -1})
	http.Redirect(w, r, flow.ReturnTo, http.StatusFound)
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}
