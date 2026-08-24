package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// stubModel stands in for the configured model and counts what the operator
// would be billed for.
func stubModel(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}],"usage":{"total_tokens":1234}}`))
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

// Every AI call is a billed request made on the operator's behalf, and nothing
// stopped one account from making them in a loop.
func TestAICallsAreBoundedPerHour(t *testing.T) {
	model, calls := stubModel(t)
	w := newScopeWorld(t)
	_, pool := newTestApp(t)
	ctx := context.Background()

	const limit = 5
	if _, err := pool.Exec(ctx, `UPDATE settings SET value=jsonb_build_object(
		'enabled',true,'baseUrl',$1::text,'model','stub','timeoutSeconds',10,'maxCallsPerHour',$2::int) WHERE key='ai'`, model.URL+"/v1", limit); err != nil {
		t.Fatalf("configure ai: %v", err)
	}
	t.Cleanup(func() {
		// t.Context() is already cancelled once cleanup runs.
		_, _ = pool.Exec(context.Background(),
			`UPDATE settings SET value='{"enabled":false,"baseUrl":"","model":"","timeoutSeconds":60}'::jsonb WHERE key='ai'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE action='contract_analysis'`)
	})
	if _, err := pool.Exec(ctx, `DELETE FROM audit_logs WHERE action='contract_analysis'`); err != nil {
		t.Fatalf("reset audit: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM login_attempts`); err != nil {
		t.Fatalf("reset attempts: %v", err)
	}
	token := signInToken(t, w.handler, "scope-dept@vendra.test")

	analyse := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/ai/contracts/"+w.myContract+"/analyze", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		w.handler.ServeHTTP(rec, r)
		return rec
	}

	accepted, refused := 0, 0
	var lastRefusal *httptest.ResponseRecorder
	for i := 0; i < limit*2+2; i++ {
		rec := analyse()
		switch rec.Code {
		case http.StatusOK:
			accepted++
		case http.StatusTooManyRequests:
			refused++
			lastRefusal = rec
		default:
			t.Fatalf("call %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}

	if accepted != limit {
		t.Errorf("%d calls were accepted, want %d", accepted, limit)
	}
	if refused == 0 {
		t.Fatal("nothing was refused, so one account can spend without bound")
	}
	if got := calls.Load(); got != int64(limit) {
		t.Errorf("the model was called %d times, want %d — refused calls must not reach it", got, limit)
	}
	if lastRefusal != nil {
		retryAfter := lastRefusal.Header().Get("Retry-After")
		if retryAfter == "" {
			t.Error("the refusal carries no Retry-After, so a client cannot pace itself")
		} else if seconds, err := strconv.Atoi(retryAfter); err != nil || seconds <= 0 || seconds > 3601 {
			t.Errorf("Retry-After = %q, want a delay within the hour", retryAfter)
		}
		if code := errorCode(t, lastRefusal.Body.String()); code != "ai_rate_limited" {
			t.Errorf("code = %q, want ai_rate_limited", code)
		}
	}
}

// An operator who wants no limit can say so, and installs whose settings row
// predates the field still get one.
func TestAIBudgetDefaultsAndOptOut(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE settings SET value='{"enabled":false,"baseUrl":"","model":"","timeoutSeconds":60}'::jsonb WHERE key='ai'`)
	})

	// A row written before the field existed.
	if _, err := pool.Exec(ctx, `UPDATE settings SET value='{"enabled":true,"baseUrl":"http://x/v1","model":"m","timeoutSeconds":60}'::jsonb WHERE key='ai'`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s, err := app.loadAI(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.MaxCallsPerHour != defaultAICallsPerHour {
		t.Errorf("MaxCallsPerHour = %d, want the default %d", s.MaxCallsPerHour, defaultAICallsPerHour)
	}

	if _, err := pool.Exec(ctx, `UPDATE settings SET value=value||'{"maxCallsPerHour":0}'::jsonb WHERE key='ai'`); err != nil {
		t.Fatalf("opt out: %v", err)
	}
	s, err = app.loadAI(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.MaxCallsPerHour != 0 {
		t.Errorf("MaxCallsPerHour = %d, want the limit switched off", s.MaxCallsPerHour)
	}
	allowed, _, err := app.withinAIBudget(ctx, "00000000-0000-0000-0000-000000000001", 0)
	if err != nil || !allowed {
		t.Errorf("allowed = %v (err %v), want an unlimited budget to allow", allowed, err)
	}
}
