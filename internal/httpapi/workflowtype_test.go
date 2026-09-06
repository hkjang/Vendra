package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// notSubmittedThroughARoute names the approval target types that have no entry
// in objectRoutes, with the reason each one is still a type a rule may be
// written for. Anything else on the list is a rule filed against a word the
// routing never asks for.
var notSubmittedThroughARoute = map[string]string{
	// updateSupplier opens the business object and its approval in the same
	// statement rather than through a submit endpoint, and the definition
	// installed by 003_sourcing_and_spend.sql is written for this type.
	"supplier_bank_change": "opened and submitted by updateSupplier",
}

// TestEveryWorkflowObjectTypeIsOneTheApplicationSubmits holds the vocabulary to
// the types something actually submits, in both directions.
//
// A rule's object_type is what matchingWorkflow selects on. A type in the
// vocabulary that nothing submits is a rule that can be saved, shown as 활성 and
// never matched — which is exactly what 공급업체 was. A routed type missing from
// the vocabulary is the other half: a 승인 요청 button with no rule that could
// ever be put in front of it, so the submission is stamped 승인 by nobody.
func TestEveryWorkflowObjectTypeIsOneTheApplicationSubmits(t *testing.T) {
	vocabulary := workflowObjectTypes()
	known := map[string]bool{}
	for _, typeName := range vocabulary {
		if known[typeName] {
			t.Errorf("%q is listed twice", typeName)
		}
		known[typeName] = true
	}
	for _, route := range objectRoutes {
		if !known[route.objectType] {
			t.Errorf("%s has a submit endpoint at %s and no approval rule can be written for it, "+
				"so every submission falls through to no_matching_workflow and is approved on the spot",
				route.objectType, route.path)
		}
	}
	routed := map[string]bool{}
	for _, route := range objectRoutes {
		routed[route.objectType] = true
	}
	for _, typeName := range vocabulary {
		if routed[typeName] {
			continue
		}
		if _, exempt := notSubmittedThroughARoute[typeName]; !exempt {
			t.Errorf("a rule can be filed for %q and nothing in the application submits that type; "+
				"the rule is stored, listed as 활성 and never fires", typeName)
		}
	}
	// And the exemptions are still real: the type has to be one some statement
	// writes into business_objects, or the reason has gone stale.
	for typeName := range notSubmittedThroughARoute {
		if !known[typeName] {
			t.Errorf("%q is exempted from the routed types and is no longer in the vocabulary at all", typeName)
			continue
		}
		if !strings.Contains(repoFile(t, "internal/httpapi/suppliers.go"), "'"+typeName+"'") {
			t.Errorf("%q is exempted as %q and no statement writes it any more",
				typeName, notSubmittedThroughARoute[typeName])
		}
	}
}

// TestTheWorkflowTypesTheFormOffersAreTheOnesTheAPIAccepts holds the admin form
// and the routing to one list, the way the supplier statuses are held.
//
// This is the check that was missing. The form wrote out four types of its own:
// one of them ("supplier") is a word nothing submits, and eight real ones were
// missing, which is not a cosmetic gap — it is the only screen an approval rule
// can be created from.
func TestTheWorkflowTypesTheFormOffersAreTheOnesTheAPIAccepts(t *testing.T) {
	source := repoFile(t, "web/src/status.ts")
	offered := tsStringList(t, source, "workflowObjectTypes")
	if strings.Join(offered, ",") != strings.Join(workflowObjectTypes(), ",") {
		t.Errorf("the form offers %v and the routing matches %v; a type on one list and not the other "+
			"is either a rule nobody can write or a rule that can never fire", offered, workflowObjectTypes())
	}
	declaration := regexp.MustCompile(`(?s)\bworkflowObjectTypes[^=]*=\s*\[(.*?)\];`).FindStringSubmatch(source)
	if declaration == nil {
		t.Fatal("web/src/status.ts no longer declares workflowObjectTypes")
	}
	labelled := map[string]bool{}
	for _, m := range regexp.MustCompile(`value:\s*"([^"]*)",\s*label:\s*"([^"]*)"`).FindAllStringSubmatch(declaration[1], -1) {
		if strings.TrimSpace(m[2]) == "" {
			t.Errorf("%s has no label", m[1])
		}
		labelled[m[1]] = true
	}
	for _, typeName := range workflowObjectTypes() {
		if !labelled[typeName] {
			t.Errorf("%q has no Korean label, so the Workflow list shows the raw type name", typeName)
		}
	}
	// The form must take its options from that list rather than writing a second
	// copy, which is how the first one came apart.
	admin := repoFile(t, "web/src/pages/Admin.tsx")
	form := regexp.MustCompile(`(?s)\nfunction WorkflowForm\(.*?\n\}\n`).FindString(admin)
	if form == "" {
		t.Fatal("Admin.tsx no longer declares WorkflowForm; this test is no longer watching the form")
	}
	for _, typeName := range append(workflowObjectTypes(), "supplier") {
		if strings.Contains(form, `<option value="`+typeName+`"`) {
			t.Errorf("the 업무 유형 form writes the type list out again (%s)", typeName)
		}
	}
	if !strings.Contains(form, "workflowObjectTypes.map(") {
		t.Error("the 업무 유형 dropdown does not read workflowObjectTypes")
	}
	// And the Workflow list says the type in the words the form offered it in.
	// It printed the stored name, so a rule read "supplier_bank_change".
	if !strings.Contains(admin, "workflowObjectTypeLabel(w.objectType)") {
		t.Error("the Workflow list shows the raw object_type rather than the label the form named it by")
	}
}

// TestARejectedWorkflowTypeNamesTheBoxAndTheChoices holds the rejection to
// naming both the field and what it will take, the way the grade and status
// ones do.
func TestARejectedWorkflowTypeNamesTheBoxAndTheChoices(t *testing.T) {
	field := workflowObjectTypeField()
	for _, typeName := range workflowObjectTypes() {
		if w := httptest.NewRecorder(); !validEnum(w, typeName, field) {
			t.Errorf("%s is on the list and was refused: %s", typeName, w.Body.String())
		}
	}
	for _, wrong := range []string{"supplier", "purchase-request", "Contract", "계약"} {
		w := httptest.NewRecorder()
		if validEnum(w, wrong, field) {
			t.Fatalf("%q was accepted as a 업무 유형", wrong)
		}
		body := w.Body.String()
		if !strings.Contains(body, "업무 유형은") {
			t.Errorf("the rejection of %q does not name the box to fix: %s", wrong, body)
		}
		if !strings.Contains(body, "purchase_request") {
			t.Errorf("the rejection of %q does not offer the types it would take: %s", wrong, body)
		}
	}
}

// TestAnApprovalRuleReachesTheSubmissionItWasWrittenFor calls the door rather
// than reading it.
//
// Two things had to be true for a rule to be useless, and both were. A rule
// could be filed for 공급업체, which nothing submits — saved, listed and dead.
// And 지급, along with seven other types, could not be given a rule at all, so
// every payment sent for approval came back 승인 with `no_matching_workflow`
// and an empty inbox behind it.
func TestAnApprovalRuleReachesTheSubmissionItWasWrittenFor(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs and these would silently no-op.
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_instances WHERE object_id IN (SELECT id FROM business_objects WHERE number='PAY-WFTYPE')`)
		_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE number='PAY-WFTYPE'`)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_definitions WHERE name IN('지급 승인 검증','공급업체 승인 검증')`)
	})
	_, _ = pool.Exec(ctx, `DELETE FROM workflow_definitions WHERE name IN('지급 승인 검증','공급업체 승인 검증')`)

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.209:5000"))
	send := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	steps := `[{"name":"구매 관리자 승인","role":"procurement_manager"}]`

	// The option the form used to offer. It is refused, and the rejection names
	// the field and the types it would take instead of storing a dead rule.
	w := send(http.MethodPost, "/api/v1/workflows",
		`{"name":"공급업체 승인 검증","objectType":"supplier","enabled":true,"steps":`+steps+`}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a rule for a type nothing submits returned %d, want 400: %s", w.Code, w.Body.String())
	}
	code, msg := errorCodeAndMessage(t, w)
	if code != "validation_error" {
		t.Errorf("the refusal answered %q, want validation_error: %s", code, msg)
	}
	if !strings.Contains(msg, "업무 유형") {
		t.Errorf("the refusal does not name the box to fix: %s", msg)
	}
	var dead int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_definitions WHERE object_type='supplier'`).Scan(&dead); err != nil {
		t.Fatalf("count the rules: %v", err)
	}
	if dead != 0 {
		t.Errorf("%d rules are filed for a type nothing submits", dead)
	}

	// And a payment, one of the eight the form left out, can now be given one.
	if _, err := pool.Exec(ctx, `INSERT INTO settings(key,value,category) VALUES('workflow.approval_enabled','true','workflow')
		ON CONFLICT(key) DO UPDATE SET value='true'`); err != nil {
		t.Fatalf("turn the approval process on: %v", err)
	}
	if w := send(http.MethodPost, "/api/v1/workflows",
		`{"name":"지급 승인 검증","objectType":"payment","enabled":true,"conditions":{},"steps":`+steps+`}`); w.Code != http.StatusCreated {
		t.Fatalf("a rule for 지급 returned %d: %s", w.Code, w.Body.String())
	}

	var paymentID string
	if err := pool.QueryRow(ctx, `INSERT INTO business_objects(object_type,number,title,status,amount,owner_id,created_by)
		VALUES('payment','PAY-WFTYPE','지급 승인 경로 검증','draft',1000000,
		       (SELECT id FROM users WHERE email=$1),(SELECT id FROM users WHERE email=$1)) RETURNING id`,
		testAdminEmail).Scan(&paymentID); err != nil {
		t.Fatalf("seed the payment: %v", err)
	}
	submit := send(http.MethodPost, "/api/v1/payments/"+paymentID+"/submit", `{}`)
	if submit.Code != http.StatusOK {
		t.Fatalf("submitting the payment returned %d: %s", submit.Code, submit.Body.String())
	}
	var answer struct {
		Status          string `json:"status"`
		WorkflowApplied bool   `json:"workflowApplied"`
		Reason          string `json:"reason"`
	}
	if err := json.Unmarshal(submit.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the submission: %v", err)
	}
	if !answer.WorkflowApplied || answer.Status != "pending_approval" {
		t.Errorf("the payment came back %q (workflowApplied=%v, reason=%q); it was approved without an approver",
			answer.Status, answer.WorkflowApplied, answer.Reason)
	}
	var pending int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_instances WHERE object_id=$1 AND status='pending'`, paymentID).Scan(&pending); err != nil {
		t.Fatalf("count the approvals: %v", err)
	}
	if pending != 1 {
		t.Errorf("the payment has %d approvals waiting on it, want 1", pending)
	}
}
