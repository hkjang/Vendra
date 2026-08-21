package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

type auditor struct{ db *pgxpool.Pool }

func (a auditor) record(r *http.Request, action, objectType, objectID string, previous, next any) {
	p, _ := principalFrom(r.Context())
	var sid any
	if p.SessionID != nil {
		sid = *p.SessionID
	}
	a.write(r, p.ID, p.Email, action, objectType, objectID, sid, previous, next)
}

// recordAnonymous captures an event that happens before a principal exists,
// such as a rejected sign-in, attributing it to the attempted identity.
func (a auditor) recordAnonymous(r *http.Request, action, objectType, objectID, actorEmail string, detail any) {
	a.write(r, "", actorEmail, action, objectType, objectID, nil, nil, detail)
}

func (a auditor) write(r *http.Request, actorID, actorEmail, action, objectType, objectID string, sessionID, previous, next any) {
	if a.db == nil {
		return
	}
	prevJSON, _ := json.Marshal(previous)
	nextJSON, _ := json.Marshal(next)
	_, err := a.db.Exec(r.Context(), `INSERT INTO audit_logs(actor_id,actor_email,action,object_type,object_id,previous_value,new_value,ip,session_id,request_id) VALUES(NULLIF($1,'')::uuid,$2,$3,$4,NULLIF($5,''),$6::jsonb,$7::jsonb,$8,$9,$10)`,
		actorID, actorEmail, action, objectType, objectID, string(prevJSON), string(nextJSON), clientIPValue(r), sessionID, requestID(r.Context()))
	if err != nil {
		slog.Error("audit record failed", "error", err, "action", action, "object_type", objectType, "object_id", objectID, "request_id", requestID(r.Context()))
	}
}
