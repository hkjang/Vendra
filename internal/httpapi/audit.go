package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
	// The action this describes has already happened. If the caller has gone
	// away — a closed tab, an aborted request — cancelling the record with them
	// would leave the change on file with nothing saying who made it. Keep the
	// request's values, drop its cancellation, and bound the write so it cannot
	// hang.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	prevJSON := auditValue(previous)
	nextJSON := auditValue(next)
	_, err := a.db.Exec(ctx, `INSERT INTO audit_logs(actor_id,actor_email,action,object_type,object_id,previous_value,new_value,ip,session_id,request_id) VALUES(NULLIF($1,'')::uuid,$2,$3,$4,NULLIF($5,''),$6::jsonb,$7::jsonb,$8,$9,$10)`,
		actorID, actorEmail, action, objectType, objectID, prevJSON, nextJSON, clientIPValue(r), sessionID, requestID(r.Context()))
	if err != nil {
		slog.Error("audit record failed", "error", err, "action", action, "object_type", objectType, "object_id", objectID, "request_id", requestID(r.Context()))
	}
}

// auditValueLimit bounds a single recorded value. Several handlers take a
// free-form object and record it as submitted, so without a ceiling any caller
// can grow a table that is never purged.
const auditValueLimit = 16 << 10

const redactedMarker = "[redacted]"

// secretName reports whether a field name announces a value that must not be
// written down in the clear.
func secretName(name string) bool {
	lower := strings.ToLower(name)
	for _, needle := range []string{
		"password", "passphrase", "secret", "token", "apikey", "api_key",
		"credential", "bankaccount", "bank_account", "privatekey", "private_key",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// redactSecrets replaces values whose field name announces a secret. The
// free-form handlers accept any key a caller sends, so a bank account posted to
// an endpoint that ignores it still reached the audit trail in the clear —
// unencrypted, and readable by anyone holding audit.read.
func redactSecrets(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, inner := range typed {
			// A boolean holds no secret, and several handlers deliberately
			// record one — bankAccountChanged, secretChanged — to say that a
			// secret moved without saying what it is.
			if _, isBool := inner.(bool); secretName(key) && !isBool {
				out[key] = redactedMarker
				continue
			}
			out[key] = redactSecrets(inner)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, inner := range typed {
			out[i] = redactSecrets(inner)
		}
		return out
	}
	return value
}

func auditValue(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	// Round-trip so a typed struct is walked by field name too.
	var generic any
	if json.Unmarshal(encoded, &generic) == nil {
		if redacted, err := json.Marshal(redactSecrets(generic)); err == nil {
			encoded = redacted
		}
	}
	if len(encoded) > auditValueLimit {
		marker, _ := json.Marshal(map[string]any{"truncated": true, "bytes": len(encoded)})
		return string(marker)
	}
	return string(encoded)
}
