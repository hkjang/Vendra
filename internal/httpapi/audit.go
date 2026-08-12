package httpapi

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

type auditor struct{ db *pgxpool.Pool }

func (a auditor) record(r *http.Request, action, objectType, objectID string, previous, next any) {
	p, _ := principalFrom(r.Context())
	prevJSON, _ := json.Marshal(previous)
	nextJSON, _ := json.Marshal(next)
	var sid any
	if p.SessionID != nil {
		sid = *p.SessionID
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	_, err := a.db.Exec(r.Context(), `INSERT INTO audit_logs(actor_id,actor_email,action,object_type,object_id,previous_value,new_value,ip,session_id,request_id) VALUES(NULLIF($1,'')::uuid,$2,$3,$4,$5,$6::jsonb,$7::jsonb,NULLIF($8,'')::inet,$9,$10)`, p.ID, p.Email, action, objectType, objectID, string(prevJSON), string(nextJSON), ip, sid, requestID(r.Context()))
	if err != nil {
		slog.Error("audit record failed", "error", err, "action", action, "object_type", objectType, "object_id", objectID, "request_id", requestID(r.Context()))
	}
}
