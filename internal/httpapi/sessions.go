package httpapi

import (
	"net/http"
)

// listMySessions shows where the account is currently signed in. Without it a
// user who loses a laptop can change their password but cannot see whether the
// eviction actually covered the device they were worried about.
func (a *App) listMySessions(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := a.db.Query(r.Context(), `
		SELECT id,host(ip),COALESCE(user_agent,''),created_at,last_seen_at,expires_at
		FROM sessions WHERE user_id=$1 AND expires_at>now()
		ORDER BY last_seen_at DESC`, p.ID)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "세션을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id string
		var ip *string
		var userAgent string
		var created, lastSeen, expires any
		if err := rows.Scan(&id, &ip, &userAgent, &created, &lastSeen, &expires); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "세션을 조회하지 못했습니다")
			return
		}
		items = append(items, map[string]any{
			"id": id, "ip": ip, "userAgent": userAgent,
			"createdAt": created, "lastSeenAt": lastSeen, "expiresAt": expires,
			"current": p.SessionID != nil && *p.SessionID == id,
		})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "세션을 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// revokeMySession signs one other device out. The current session is refused so
// that "sign out everywhere else" and "log out" stay distinct actions.
func (a *App) revokeMySession(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id := r.PathValue("id")
	if p.SessionID != nil && *p.SessionID == id {
		writeError(w, 400, "current_session", "현재 세션은 로그아웃으로 종료하세요")
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM sessions WHERE id=$1 AND user_id=$2`, id, p.ID)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "세션을 종료하지 못했습니다")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "세션을 찾을 수 없습니다")
		return
	}
	a.audit.record(r, "revoke_session", "session", id, nil, map[string]any{"scope": "single"})
	writeJSON(w, 200, map[string]any{"ok": true, "revokedSessions": tag.RowsAffected()})
}

// revokeMyOtherSessions is the "I lost a device" button.
func (a *App) revokeMyOtherSessions(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	revoked := a.revokeSessions(r, p.ID, p.SessionID)
	a.audit.record(r, "revoke_session", "user", p.ID, nil, map[string]any{"scope": "others", "revokedSessions": revoked})
	writeJSON(w, 200, map[string]any{"ok": true, "revokedSessions": revoked})
}
