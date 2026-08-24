package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A panic in a background step used to take the goroutine, and with it every
// later pass: notifications and the retention sweep would stop for good,
// silently, until the process restarted. HTTP handlers have their own recover;
// this loop had none.
func TestBackgroundStepSurvivesAPanic(t *testing.T) {
	err := runBackgroundStep("probe", func() error {
		var missing map[string]string
		missing["key"] = "value" // assignment to a nil map
		return nil
	})
	if err == nil {
		t.Fatal("a panicking step reported success")
	}
	if !strings.Contains(err.Error(), "probe") {
		t.Errorf("err = %v, want it to name the step", err)
	}
}

func TestBackgroundStepPassesErrorsAndSuccessThrough(t *testing.T) {
	boom := errors.New("the sweep failed")
	if err := runBackgroundStep("probe", func() error { return boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the step's own error", err)
	}
	if err := runBackgroundStep("probe", func() error { return nil }); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// A stalled loop leaves /health/ready answering exactly as before, so the only
// way to notice is a timestamp that stops advancing.
func TestMetricsReportBackgroundLiveness(t *testing.T) {
	app := &App{}
	before := runtimeHTTPMetrics.backgroundPasses.Load()
	runtimeHTTPMetrics.backgroundPasses.Store(before + 1)
	runtimeHTTPMetrics.backgroundLastPassUnix.Store(1756000000)
	t.Cleanup(func() {
		runtimeHTTPMetrics.backgroundPasses.Store(before)
		runtimeHTTPMetrics.backgroundLastPassUnix.Store(0)
	})

	w := httptest.NewRecorder()
	app.metrics(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := w.Body.String()
	for _, name := range []string{
		"vendra_background_passes_total",
		"vendra_background_pass_failures_total",
		"vendra_background_last_pass_timestamp_seconds 1756000000",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("metrics do not report %q", name)
		}
	}
}

// End to end: a pass against a real database advances the counters and the
// retention sweep removes only rows that are already expired.
func TestBackgroundPassSweepsOnlyExpiredRows(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := t.Context()

	var userID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&userID); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE user_agent LIKE 'retention-probe:%'`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	t.Cleanup(func() {
		// t.Context() is already cancelled once cleanup runs.
		_, _ = pool.Exec(context.Background(), `DELETE FROM sessions WHERE user_agent LIKE 'retention-probe:%'`)
	})
	insert := func(label string, expiresAt string) {
		if _, err := pool.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,user_agent,expires_at) VALUES($1,$2,$3,`+expiresAt+`)`,
			userID, []byte("retention-probe-"+label), "retention-probe:"+label); err != nil {
			t.Fatalf("seed %s: %v", label, err)
		}
	}
	insert("live", `now()+interval '1 day'`)
	insert("just-expired", `now()-interval '1 hour'`)
	insert("long-expired", `now()-interval '90 days'`)

	before := runtimeHTTPMetrics.backgroundPasses.Load()
	app.runBackgroundOnce(ctx)
	// The application starts its own loop, so the counter only has to advance.
	if runtimeHTTPMetrics.backgroundPasses.Load() <= before {
		t.Error("the pass was not counted")
	}
	if runtimeHTTPMetrics.backgroundLastPassUnix.Load() == 0 {
		t.Error("the pass left no timestamp, so a stalled loop stays invisible")
	}

	var remaining []string
	rows, err := pool.Query(ctx, `SELECT user_agent FROM sessions WHERE user_agent LIKE 'retention-probe:%' ORDER BY user_agent`)
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			t.Fatalf("scan: %v", err)
		}
		remaining = append(remaining, label)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	// Default retention is seven days, so only the long-expired session goes.
	want := []string{"retention-probe:just-expired", "retention-probe:live"}
	if strings.Join(remaining, ",") != strings.Join(want, ",") {
		t.Errorf("remaining = %v, want %v", remaining, want)
	}
}
