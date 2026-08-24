package httpapi

import (
	"net/http"
	"strings"
)

// changeMyPassword rotates the signed-in user's own password. The current
// password is re-checked so that a stolen session cannot lock the owner out,
// and every other session is revoked so a rotation actually evicts an intruder.
func (a *App) changeMyPassword(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	var currentHash *string
	if err := a.db.QueryRow(r.Context(), `SELECT password_hash FROM users WHERE id=$1 AND status='active'`, p.ID).Scan(&currentHash); err != nil {
		writeError(w, 500, "database_error", "비밀번호를 변경하지 못했습니다")
		return
	}
	if currentHash == nil || *currentHash == "" {
		writeError(w, 409, "no_local_password", "외부 인증(OIDC) 계정은 비밀번호를 변경할 수 없습니다")
		return
	}
	if !passwordMatches(*currentHash, in.CurrentPassword) {
		runtimeHTTPMetrics.loginFailures.Add(1)
		a.audit.record(r, "change_password_failed", "user", p.ID, nil, map[string]any{"reason": "invalid_current_password"})
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "현재 비밀번호가 올바르지 않습니다")
		return
	}
	if in.NewPassword == in.CurrentPassword {
		writeError(w, 400, "weak_password", "새 비밀번호는 현재 비밀번호와 달라야 합니다")
		return
	}
	hash, err := a.hashPassword(r.Context(), in.NewPassword)
	if err != nil {
		writePasswordError(w, err)
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, p.ID, hash); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "비밀번호를 변경하지 못했습니다")
		return
	}
	revoked := a.revokeSessions(r, p.ID, p.SessionID)
	// The password already changed; failing here would deny a change that
	// happened. An uncleared lockout expires on its own.
	if _, err := a.db.Exec(r.Context(), `DELETE FROM login_attempts WHERE email=$1 AND NOT succeeded`, strings.ToLower(p.Email)); err != nil {
		logDB(err)
	}
	a.audit.record(r, "change_password", "user", p.ID, nil, map[string]any{"revokedSessions": revoked})
	writeJSON(w, 200, map[string]any{"ok": true, "revokedSessions": revoked})
}

// resetUserPassword lets an administrator set a password for another account,
// which is the incident-response path when a credential is compromised. Every
// session of the target is revoked, including any the intruder holds.
func (a *App) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	actor, _ := principalFrom(r.Context())
	id := r.PathValue("id")
	var in struct {
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	var email string
	if err := a.db.QueryRow(r.Context(), `SELECT email FROM users WHERE id=$1`, id).Scan(&email); err != nil {
		writeError(w, 404, "not_found", "사용자를 찾을 수 없습니다")
		return
	}
	hash, err := a.hashPassword(r.Context(), in.NewPassword)
	if err != nil {
		writePasswordError(w, err)
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, id, hash); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "비밀번호를 변경하지 못했습니다")
		return
	}
	// The administrator's own session survives only when they reset themselves.
	var keep *string
	if actor.ID == id {
		keep = actor.SessionID
	}
	revoked := a.revokeSessions(r, id, keep)
	// The password already changed; failing here would deny a change that
	// happened. An uncleared lockout expires on its own.
	if _, err := a.db.Exec(r.Context(), `DELETE FROM login_attempts WHERE email=$1 AND NOT succeeded`, email); err != nil {
		logDB(err)
	}
	a.audit.record(r, "reset_password", "user", id, nil, map[string]any{"email": email, "revokedSessions": revoked})
	writeJSON(w, 200, map[string]any{"ok": true, "revokedSessions": revoked})
}

// revokeSessions deletes a user's sessions, optionally sparing the one making
// the request, and reports how many were removed.
func (a *App) revokeSessions(r *http.Request, userID string, keep *string) int64 {
	var keepID any
	if keep != nil {
		keepID = *keep
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM sessions WHERE user_id=$1 AND ($2::uuid IS NULL OR id<>$2::uuid)`, userID, keepID)
	if err != nil {
		logDB(err)
		return 0
	}
	return tag.RowsAffected()
}
