package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The trail recorded rejected sign-ins but not accepted ones, and nothing at
// all for signing out. An investigator could see who was turned away and never
// who got in, or when they left.
func TestSignInAndSignOutAreOnTheRecord(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	h := app.Handler()
	if _, err := pool.Exec(ctx, `DELETE FROM login_attempts`); err != nil {
		t.Fatalf("reset attempts: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_logs`); err != nil {
		t.Fatalf("reset audit: %v", err)
	}

	_ = postLogin(t, h, testAdminEmail, "wrong-password", "203.0.113.30:1000")
	w := postLogin(t, h, testAdminEmail, testAdminPassword, "203.0.113.30:1000")
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in: %d %s", w.Code, w.Body.String())
	}
	var token string
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			token = c.Value
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("sign-out: %d %s", rec.Code, rec.Body.String())
	}

	for _, action := range []string{"login_failed", "login", "logout"} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action=$1 AND actor_email=$2`, action, testAdminEmail).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", action, err)
		}
		if count == 0 {
			t.Errorf("%q left no audit record", action)
		}
	}
	// The accepted sign-in must name the session it opened, so a later action
	// can be tied back to it.
	var sessionID *string
	if err := pool.QueryRow(ctx, `SELECT session_id::text FROM audit_logs WHERE action='login' AND actor_email=$1`, testAdminEmail).Scan(&sessionID); err != nil {
		t.Fatalf("read login record: %v", err)
	}
	if sessionID == nil || *sessionID == "" {
		t.Error("the sign-in record names no session")
	}
}

// The action an audit record describes has already happened. Cancelling the
// record along with the caller leaves the change on file with nothing saying
// who made it.
func TestAuditRecordSurvivesTheCallerLeaving(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM audit_logs WHERE action='caller-left'`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	var actorID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&actorID); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	t.Cleanup(func() {
		// t.Context() is already cancelled once cleanup runs.
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE action='caller-left'`)
	})

	gone, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodPost, "/api/v1/suppliers", nil)
	r = r.WithContext(context.WithValue(gone, principalKey, Principal{ID: actorID, Email: testAdminEmail}))
	cancel()

	app.audit.record(r, "caller-left", "supplier", actorID, nil, map[string]any{"note": "the caller disconnected"})

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='caller-left'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("%d records survived the caller leaving, want 1 — the change stands with nobody named against it", count)
	}
}
