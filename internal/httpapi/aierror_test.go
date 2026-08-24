package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A failed call to the configured model must not hand its endpoint back to the
// caller. The transport error names the host and port, which is routinely an
// internal address, and any account holding ai.use could read it out of a 502.
func TestAIFailureDoesNotRevealTheEndpoint(t *testing.T) {
	w := newScopeWorld(t)
	_, pool := newTestApp(t)
	ctx := context.Background()

	const host = "ai-gateway.internal.corp"
	if _, err := pool.Exec(ctx, `UPDATE settings SET value=jsonb_build_object('enabled',true,'baseUrl',$1::text,'model','gpt-4','timeoutSeconds',2) WHERE key='ai'`,
		"http://"+host+":8443/v1"); err != nil {
		t.Fatalf("configure ai: %v", err)
	}
	t.Cleanup(func() {
		// t.Context() is already cancelled once cleanup runs.
		_, _ = pool.Exec(context.Background(),
			`UPDATE settings SET value='{"enabled":false,"baseUrl":"","model":"","timeoutSeconds":60}'::jsonb WHERE key='ai'`)
	})
	if _, err := pool.Exec(ctx, `DELETE FROM login_attempts`); err != nil {
		t.Fatalf("reset attempts: %v", err)
	}
	token := signInToken(t, w.handler, "scope-dept@vendra.test")

	r := httptest.NewRequest(http.MethodPost, "/api/v1/ai/contracts/"+w.myContract+"/analyze", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, host) {
		t.Errorf("the response names the configured endpoint: %s", body)
	}
	if strings.Contains(body, "8443") {
		t.Errorf("the response names the endpoint's port: %s", body)
	}
	if !strings.Contains(body, "ai_unavailable") {
		t.Errorf("the caller cannot tell what went wrong: %s", body)
	}
}

// The portal boundary must not let a supplier tell a record that is not theirs
// from one that does not exist.
func TestPortalCannotTellMissingFromForbidden(t *testing.T) {
	f, _ := newPortalFixture(t)
	const ghost = "00000000-0000-0000-0000-0000000000ff"

	get := func(path string) (int, string) {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, r)
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}

	for _, tc := range []struct{ name, missing, forbidden string }{
		{"supplier", "/api/v1/suppliers/" + ghost, "/api/v1/suppliers/" + f.supplierB},
		{"contract", "/api/v1/contracts/" + ghost, "/api/v1/contracts/" + f.objectOfB},
		{"sourcing questions", "/api/v1/portal/sourcing/" + ghost + "/questions", "/api/v1/portal/sourcing/" + f.objectOfB + "/questions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			missingCode, missingBody := get(tc.missing)
			forbiddenCode, forbiddenBody := get(tc.forbidden)
			if missingCode != forbiddenCode || missingBody != forbiddenBody {
				t.Errorf("a portal account can tell these apart\n  missing:   %d %s\n  forbidden: %d %s",
					missingCode, missingBody, forbiddenCode, forbiddenBody)
			}
		})
	}
}
