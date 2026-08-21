package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPermissions(t *testing.T) {
	tests := []struct {
		permissions []string
		wanted      string
		ok          bool
	}{{[]string{"*"}, "contract.update", true}, {[]string{"supplier.*"}, "supplier.bank_account.read", true}, {[]string{"*.read"}, "contract.read", true}, {[]string{"purchase_request.read"}, "purchase_request.amount.read", false}}
	for _, tt := range tests {
		if got := hasPermission(Principal{Permissions: tt.permissions}, tt.wanted); got != tt.ok {
			t.Errorf("permissions=%v wanted=%s got=%v", tt.permissions, tt.wanted, got)
		}
	}
}

func TestWorkflowConditions(t *testing.T) {
	amount := 15_000_000.0
	risk := "HIGH"
	org := "org-1"
	o := businessObject{Amount: &amount, RiskLevel: &risk, OrganizationID: &org, Data: map[string]any{"contractType": "service", "category": "IT"}}
	min := 10_000_000.0
	if !workflowMatches(workflowConditions{MinAmount: &min, RiskLevels: []string{"HIGH", "CRITICAL"}, OrganizationID: "org-1", ContractType: "service", Category: "IT"}, o) {
		t.Fatal("matching workflow was rejected")
	}
	tooHigh := 20_000_000.0
	if workflowMatches(workflowConditions{MinAmount: &tooHigh}, o) {
		t.Fatal("amount condition was ignored")
	}
	if workflowMatches(workflowConditions{RiskLevel: "LOW"}, o) {
		t.Fatal("risk condition was ignored")
	}
}

func TestDataScopeAndFieldRedaction(t *testing.T) {
	owner := "user-1"
	org := "org-1"
	amount := 100.0
	o := businessObject{ObjectType: "purchase_request", OwnerID: &owner, OrganizationID: &org, Amount: &amount}
	if !canAccessObject(Principal{ID: owner, DataScope: "own"}, o) {
		t.Fatal("owner cannot access own object")
	}
	if canAccessObject(Principal{ID: "user-2", DataScope: "own"}, o) {
		t.Fatal("different owner accessed object")
	}
	redacted := redactObject(Principal{Permissions: []string{"purchase_request.read"}}, o)
	if redacted.Amount != nil {
		t.Fatal("amount was not redacted")
	}
	visible := redactObject(Principal{Permissions: []string{"purchase_request.*"}}, o)
	if visible.Amount == nil {
		t.Fatal("amount was incorrectly redacted")
	}
}

func TestConditionalResourceGrant(t *testing.T) {
	resourceID := "supplier-1"
	p := Principal{UserType: "internal", DataScope: "department", AccessGrants: []AccessGrant{{
		Permission:   "supplier.read",
		ResourceType: "supplier",
		ResourceID:   &resourceID,
		Conditions:   map[string]any{"method": "GET", "dataScope": "department"},
	}}}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/suppliers/"+resourceID, nil)
	r.SetPathValue("id", resourceID)
	if !hasGrantPermission(p, "supplier.read", r) {
		t.Fatal("matching conditional resource grant was rejected")
	}
	r.Method = http.MethodPost
	if hasGrantPermission(p, "supplier.read", r) {
		t.Fatal("method condition was ignored")
	}
	r.Method = http.MethodGet
	r.SetPathValue("id", "supplier-2")
	if hasGrantPermission(p, "supplier.read", r) {
		t.Fatal("resource id condition was ignored")
	}
	if grantConditionsValid(map[string]any{"unsupported": true}) {
		t.Fatal("unknown grant condition was accepted")
	}
}

func TestOpenAPICoversCoreSurfaces(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&App{}).openapi(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("openapi returned %d", recorder.Code)
	}
	var specification struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &specification); err != nil {
		t.Fatalf("invalid OpenAPI JSON: %v", err)
	}
	if specification.OpenAPI != "3.1.0" {
		t.Fatalf("unexpected OpenAPI version %q", specification.OpenAPI)
	}
	required := map[string]string{
		"/api/v1/suppliers":                           http.MethodPost,
		"/api/v1/contracts/{id}/submit":               http.MethodPost,
		"/api/v1/sourcing/{id}/comparison":            http.MethodGet,
		"/api/v1/admin/access-grants/{id}":            http.MethodDelete,
		"/api/v1/me/api-keys/{id}/rotate":             http.MethodPost,
		"/api/v1/portal/sourcing/{id}/response":       http.MethodPut,
		"/api/v1/portal/purchase-orders/{id}/confirm": http.MethodPost,
		"/api/v1/ai/contracts/{id}/analyze":           http.MethodPost,
		"/api/v1/me/work-inbox":                       http.MethodGet,
		"/api/v1/me/work-items/state":                 http.MethodPost,
		"/api/v1/me/saved-views":                      http.MethodPost,
		"/api/v1/me/drafts/{key}":                     http.MethodPut,
	}
	for path, method := range required {
		if specification.Paths[path][strings.ToLower(method)] == nil {
			t.Errorf("OpenAPI is missing %s %s", method, path)
		}
	}
}
