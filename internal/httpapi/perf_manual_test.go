package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"testing"
	"time"
)

// TestMeasureEndpointLatency is a measurement harness, not an assertion: it
// reports how long the read paths take against whatever data the target
// database holds. Run it with VENDRA_PERF=1 against a seeded database.
func TestMeasureEndpointLatency(t *testing.T) {
	if os.Getenv("VENDRA_PERF") == "" {
		t.Skip("set VENDRA_PERF=1 to measure endpoint latency")
	}
	app, pool := newTestApp(t)
	handler := app.Handler()
	ctx := context.Background()
	// Give the caller company scope and every read permission.
	var roleID string
	if err := pool.QueryRow(ctx, `INSERT INTO roles(code,name,permissions,data_scope,system) VALUES('perf_reader','성능 측정','["*"]','company',false)
		ON CONFLICT(code) DO UPDATE SET permissions='["*"]',data_scope='company' RETURNING id`).Scan(&roleID); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	var adminID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&adminID); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, adminID, roleID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	token := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.1:5000"))

	// Any supplier will do; the point is the per-supplier read paths.
	var sampleSupplier string
	if err := pool.QueryRow(ctx, `SELECT id FROM suppliers WHERE deleted_at IS NULL LIMIT 1`).Scan(&sampleSupplier); err != nil {
		t.Skipf("no supplier to measure against: %v", err)
	}
	paths := []string{
		"/api/v1/dashboard",
		"/api/v1/suppliers",
		"/api/v1/suppliers?q=" + url.QueryEscape("성능 공급사 4321"),
		"/api/v1/contracts",
		"/api/v1/contracts?q=" + url.QueryEscape("성능 업무 12345"),
		"/api/v1/documents",
		"/api/v1/risks",
		"/api/v1/spend",
		"/api/v1/spend?groupBy=category",
		"/api/v1/search?q=" + url.QueryEscape("성능"),
		"/api/v1/approvals",
		"/api/v1/admin/audit",
		"/api/v1/admin/users",
		"/api/v1/me/work-inbox",
		"/api/v1/me/notifications",
		"/api/v1/supplier-network",
		"/api/v1/suppliers/" + sampleSupplier + "/risks",
		"/api/v1/suppliers/" + sampleSupplier + "/evaluations",
		"/api/v1/suppliers/" + sampleSupplier + "/contacts",
		"/api/v1/suppliers/" + sampleSupplier + "/objects",
		"/api/v1/documents?supplierId=" + sampleSupplier,
	}
	const runs = 5
	type result struct {
		path   string
		median time.Duration
		worst  time.Duration
		status int
	}
	results := make([]result, 0, len(paths))
	for _, path := range paths {
		samples := make([]time.Duration, 0, runs)
		status := 0
		for i := 0; i < runs; i++ {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			w := httptest.NewRecorder()
			began := time.Now()
			handler.ServeHTTP(w, r)
			samples = append(samples, time.Since(began))
			status = w.Code
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		results = append(results, result{path, samples[len(samples)/2], samples[len(samples)-1], status})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].median > results[j].median })
	for _, r := range results {
		marker := "   "
		if r.median > 300*time.Millisecond {
			marker = "!! "
		} else if r.median > 100*time.Millisecond {
			marker = " ! "
		}
		fmt.Printf("%s%-42s median=%-9s worst=%-9s status=%d\n", marker, r.path, r.median.Round(time.Millisecond), r.worst.Round(time.Millisecond), r.status)
	}
}
