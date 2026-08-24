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

// A control whose setting cannot be read must not take its permissive value.
// boolSetting used to be an ignored error and a zero value, which is the same
// as answering "no approval required" whenever the lookup failed.
func TestBoolSettingReportsFailureInsteadOfPermitting(t *testing.T) {
	app := &App{db: unreachablePool(t)}
	value, err := app.boolSetting(context.Background(), `SELECT true`, true)
	if err == nil {
		t.Fatalf("value = %v with no error; an unreadable control was answered instead of refused", value)
	}
	if value {
		t.Error("value = true alongside an error — a caller ignoring err would permit the action")
	}
}

// Reproduces the approval bypass end to end: with only the settings lookup
// failing, submitting a purchase order used to store it as approved, with no
// approval and an audit entry claiming approvals were switched off.
func TestSubmitRefusesWhenApprovalSettingUnreadable(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := t.Context()
	h := app.Handler()

	if _, err := pool.Exec(ctx, `REVOKE SELECT ON settings FROM CURRENT_USER`); err != nil {
		t.Skipf("the test role cannot revoke its own SELECT on settings: %v", err)
	}
	restored := false
	t.Cleanup(func() {
		// t.Context() is already cancelled once cleanup runs.
		if !restored {
			_, _ = pool.Exec(context.Background(), `GRANT SELECT ON settings TO CURRENT_USER`)
		}
	})
	var readable bool
	if err := pool.QueryRow(ctx, `SELECT true FROM settings LIMIT 1`).Scan(&readable); err == nil {
		_, _ = pool.Exec(context.Background(), `GRANT SELECT ON settings TO CURRENT_USER`)
		restored = true
		t.Skip("the test role reads settings regardless of the revoke (superuser?)")
	}

	_, _ = pool.Exec(context.Background(), `DELETE FROM login_attempts`)
	w := postLogin(t, h, testAdminEmail, testAdminPassword, "203.0.113.5:1000")
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var token string
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			token = c.Value
		}
	}

	call := func(method, path, body string) (int, string) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}

	number := fmt.Sprintf("PO-FAILCLOSED-%d", len(t.Name()))
	_, _ = pool.Exec(context.Background(), `DELETE FROM business_objects WHERE number=$1`, number)
	code, body := call("POST", "/api/v1/purchase-orders", fmt.Sprintf(`{"number":%q,"title":"결재 대상"}`, number))
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	id := created.Object.ID
	if id == "" {
		id = created.ID
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM business_objects WHERE id=$1`, id)
	})

	code, body = call("POST", "/api/v1/purchase-orders/"+id+"/submit", `{}`)
	if code != http.StatusServiceUnavailable {
		t.Errorf("submit = %d, want 503 — an unreadable approval setting decided the request\n  body: %s", code, body)
	}

	_, _ = pool.Exec(context.Background(), `GRANT SELECT ON settings TO CURRENT_USER`)
	restored = true

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM business_objects WHERE id=$1`, id).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status == "approved" {
		t.Error("the order was approved outright while the approval setting could not be read")
	}
}

// The separation-of-duties policy has no safe zero value: unread, it permits
// the requester to approve their own request. Every other policy reader starts
// from a safe default and only overwrites it on a successful read; this one
// started from the permissive value, so a failed lookup switched the control
// off exactly when an administrator had switched it on.
func TestSeparationOfDutiesReportsFailure(t *testing.T) {
	app := &App{db: unreachablePool(t)}
	policy, err := app.separationOfDuties(context.Background())
	if err == nil {
		t.Fatalf("policy = %+v with no error; an unreadable control was answered instead of refused", policy)
	}
	if policy.BlockSelfApproval {
		t.Error("BlockSelfApproval = true alongside an error; the zero value is what a caller ignoring err would use")
	}
}

// Approving one's own request must be refused, not permitted, when the policy
// cannot be read.
func TestSelfApprovalRefusedWhenPolicyUnreadable(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := t.Context()

	if _, err := pool.Exec(ctx, `REVOKE SELECT ON settings FROM CURRENT_USER`); err != nil {
		t.Skipf("the test role cannot revoke its own SELECT on settings: %v", err)
	}
	restored := false
	t.Cleanup(func() {
		// t.Context() is already cancelled once cleanup runs.
		if !restored {
			_, _ = pool.Exec(context.Background(), `GRANT SELECT ON settings TO CURRENT_USER`)
		}
	})
	var readable bool
	if err := pool.QueryRow(ctx, `SELECT true FROM settings LIMIT 1`).Scan(&readable); err == nil {
		_, _ = pool.Exec(context.Background(), `GRANT SELECT ON settings TO CURRENT_USER`)
		restored = true
		t.Skip("the test role reads settings regardless of the revoke (superuser?)")
	}

	policy, err := app.separationOfDuties(ctx)
	if err == nil {
		t.Fatalf("policy = %+v, want a failure while settings is unreadable", policy)
	}
}
