package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Every list endpoint must refuse to answer when it cannot read. This covers
// the case where the connection is gone before the query starts; the harder
// case, where the query starts and fails partway through the rows, is covered
// by TestMidStreamFailureIsNotEmptyResult below.
func TestReadFailureIsNeverEmptySuccess(t *testing.T) {
	app := &App{db: unreachablePool(t)}
	principal := Principal{
		ID:          "00000000-0000-0000-0000-000000000001",
		Email:       "reader@vendra.test",
		UserType:    "internal",
		DataScope:   "company",
		Permissions: []string{"*"},
	}

	for _, tc := range []struct {
		name    string
		path    string
		handler http.HandlerFunc
	}{
		{"dashboard", "/api/v1/dashboard", app.dashboard},
		{"spend analysis", "/api/v1/spend", app.spendAnalysis},
		{"spend by category", "/api/v1/spend?groupBy=category", app.spendAnalysis},
		{"global search", "/api/v1/search?q=vendra", app.globalSearch},
		{"suppliers", "/api/v1/suppliers", app.listSuppliers},
		{"risks", "/api/v1/risks", app.listAllRisks},
		{"evaluations", "/api/v1/evaluations", app.listAllEvaluations},
		{"approvals", "/api/v1/approvals", app.listApprovals},
		{"workflows", "/api/v1/workflows", app.listWorkflows},
		{"documents", "/api/v1/documents", app.listDocuments},
		{"notifications", "/api/v1/me/notifications", app.listNotifications},
		{"sessions", "/api/v1/me/sessions", app.listMySessions},
		{"work inbox", "/api/v1/me/work-inbox", app.workInbox},
		{"saved views", "/api/v1/me/saved-views", app.listSavedViews},
		{"audit log", "/api/v1/admin/audit", app.listAudit},
		{"users", "/api/v1/admin/users", app.listUsers},
		{"roles", "/api/v1/admin/roles", app.listRoles},
		{"organizations", "/api/v1/admin/organizations", app.listOrganizations},
		{"access grants", "/api/v1/admin/access-grants", app.listAccessGrants},
		{"settings", "/api/v1/admin/settings", app.listSettings},
		{"screening templates", "/api/v1/screening-templates", app.listScreeningTemplates},
		{"scorecards", "/api/v1/scorecards", app.listScorecards},
		{"purchase orders", "/api/v1/purchase-orders", app.listObjects("purchase_order")},
		{"contracts", "/api/v1/contracts", app.listObjects("contract")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r = r.WithContext(context.WithValue(r.Context(), principalKey, principal))
			w := httptest.NewRecorder()
			tc.handler(w, r)

			if w.Code < 500 {
				t.Errorf("status = %d with an unreachable database, want 5xx — the caller cannot tell this from real data\n  body: %s", w.Code, w.Body.String())
			}
		})
	}
}

// A malformed filter is the caller's mistake and must be answered as one. It
// used to reach PostgreSQL, fail the date cast there, and come back as an
// empty result set — a spend report of zero for a mistyped date.
func TestMalformedDateFilterIsRejected(t *testing.T) {
	app := &App{db: unreachablePool(t)}
	principal := Principal{ID: "00000000-0000-0000-0000-000000000001", DataScope: "company", Permissions: []string{"*"}}

	for _, path := range []string{
		"/api/v1/spend?from=hello",
		"/api/v1/spend?to=2026-13-45",
		"/api/v1/spend?groupBy=month&from=2020-01-01&to=oops",
	} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r = r.WithContext(context.WithValue(r.Context(), principalKey, principal))
		w := httptest.NewRecorder()
		app.spendAnalysis(w, r)

		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, w.Code)
		}
		if got := errorCode(t, w.Body.String()); got != "validation_error" {
			t.Errorf("%s: code = %q, want validation_error", path, got)
		}
	}
}
