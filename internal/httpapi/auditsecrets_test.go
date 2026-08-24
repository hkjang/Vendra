package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A field name that announces a secret must never be written down in the clear,
// whichever handler it reached.
func TestAuditValueRedactsSecretsAndBoundsSize(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  any
		want   string
		absent string
	}{
		{"bank account", map[string]any{"bankAccount": "110-1234-567890"}, redactedMarker, "110-1234-567890"},
		{"password", map[string]any{"password": "hunter2"}, redactedMarker, "hunter2"},
		{"nested api key", map[string]any{"config": map[string]any{"apiKey": "sk-live-123"}}, redactedMarker, "sk-live-123"},
		{"inside a list", map[string]any{"items": []any{map[string]any{"clientSecret": "shh"}}}, redactedMarker, "shh"},
		{"snake case", map[string]any{"bank_account": "110-9999"}, redactedMarker, "110-9999"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := auditValue(tc.value)
			if !strings.Contains(got, tc.want) {
				t.Errorf("value = %s, want it redacted", got)
			}
			if strings.Contains(got, tc.absent) {
				t.Errorf("the secret survived into the audit value: %s", got)
			}
		})
	}

	// The flags that say a secret moved are booleans and must survive, or the
	// trail loses the one thing it is allowed to say.
	t.Run("change flags survive", func(t *testing.T) {
		got := auditValue(map[string]any{"bankAccountChanged": true, "secretChanged": false})
		if !strings.Contains(got, `"bankAccountChanged":true`) || !strings.Contains(got, `"secretChanged":false`) {
			t.Errorf("value = %s, want both flags intact", got)
		}
	})

	// Several handlers record a free-form object as submitted, so an unbounded
	// value would let any caller grow a table that is never purged.
	t.Run("oversized value is bounded", func(t *testing.T) {
		got := auditValue(map[string]any{"note": strings.Repeat("가", 20000)})
		if len(got) > auditValueLimit {
			t.Errorf("recorded %d bytes, want at most %d", len(got), auditValueLimit)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(got), &decoded); err != nil {
			t.Fatalf("the bounded value is not valid JSON: %v", err)
		}
		if decoded["truncated"] != true {
			t.Errorf("value = %s, want it to say it was truncated", got)
		}
	})
}

// End to end: a supplier posts a bank account to the portal endpoint that
// ignores it — an inviting mistake, since the endpoint's own reply talks about
// bank account changes. It used to be filed in the audit trail in the clear,
// unencrypted and readable by anyone holding audit.read, while never reaching
// the encrypted column.
func TestPortalInputDoesNotLeakSecretsIntoTheTrail(t *testing.T) {
	f, pool := newPortalFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM audit_logs WHERE action='portal_update'`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	t.Cleanup(func() {
		// t.Context() is already cancelled once cleanup runs.
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE action='portal_update'`)
	})

	const account = "국민은행 110-1234-567890"
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/portal/profile",
		strings.NewReader(fmt.Sprintf(`{"phone":"02-1234-5678","bankAccount":%q}`, account)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("portal profile update: %d %s", rec.Code, rec.Body.String())
	}

	var recorded string
	if err := pool.QueryRow(ctx, `SELECT new_value::text FROM audit_logs WHERE action='portal_update' ORDER BY occurred_at DESC LIMIT 1`).Scan(&recorded); err != nil {
		t.Fatalf("read audit record: %v", err)
	}
	if strings.Contains(recorded, account) {
		t.Errorf("the account number is in the audit trail in the clear: %s", recorded)
	}
	// The rest of the submission is still on the record.
	if !strings.Contains(recorded, "02-1234-5678") {
		t.Errorf("redaction removed more than the secret: %s", recorded)
	}
}
