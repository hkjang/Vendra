package httpapi

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEveryStatementThatWritesApprovalStepsChecksThem sweeps by the operation
// rather than by the spelling, the way the date, number, label, vocabulary,
// record id and address sweeps do.
//
// The operation is a caller's value written into workflow_definitions.steps.
// There are two doors into it — createWorkflow and updateWorkflow — and only
// one was watched. The creation refused an empty list and a step with no name
// and numbered what it stored; the edit stored whatever arrived, including a
// list with nothing in it and a value that is not a list at all.
//
// What that costs is not visible where it is written. It is visible at the
// next submission that matches the rule: the object is moved to 결재중 and the
// approval is opened with no step to stand on, so listApprovals leaves it out
// of every 승인함 and workflowAction answers 409. The request cannot be
// approved, rejected, returned or sent again.
func TestEveryStatementThatWritesApprovalStepsChecksThem(t *testing.T) {
	writesSteps := regexp.MustCompile(`INSERT INTO workflow_definitions\([^)]*\bsteps\b|UPDATE workflow_definitions SET [^` + "`" + `]*\bsteps=`)

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(f os.FileInfo) bool {
		return !strings.HasSuffix(f.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	checked := 0
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			source, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			base := fset.File(file.Pos()).Base()
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				body := string(source[int(fn.Body.Pos())-base : int(fn.Body.End())-base]) //nolint:gosec
				if !writesSteps.MatchString(body) {
					continue
				}
				checked++
				if !strings.Contains(body, "validWorkflowSteps(") {
					t.Errorf("%s writes workflow_definitions.steps without checking them; "+
						"a rule stored with no steps opens approvals that reach nobody and can never be closed",
						fn.Name.Name)
				}
			}
		}
	}
	if checked < 2 {
		t.Fatalf("the sweep found %d statements writing the steps column, want the two that write it; "+
			"the pattern no longer matches the handlers it was written for", checked)
	}
}

// TestAnApprovalRuleWithNoStepsIsRefused checks the answer, not just that
// there is one: a refusal that does not name the box leaves the caller with a
// form and no idea which field to fix.
func TestAnApprovalRuleWithNoStepsIsRefused(t *testing.T) {
	for _, badly := range []any{
		[]map[string]any{},
		"구매 관리자 승인",
		map[string]any{"name": "구매 관리자 승인"},
		[]map[string]any{{"role": "finance"}},
		[]map[string]any{{"name": "  "}},
	} {
		w := httptest.NewRecorder()
		if _, ok := validWorkflowSteps(w, badly); ok {
			t.Errorf("%#v was accepted as the steps of an approval rule", badly)
			continue
		}
		if code, msg := errorCodeAndMessage(t, w); code != "validation_error" || !strings.Contains(msg, "승인 단계") {
			t.Errorf("%#v was refused as %q/%q, which does not name the box to fix", badly, code, msg)
		}
	}
	// And a real list keeps its order, which is what the approval walks.
	steps, ok := validWorkflowSteps(httptest.NewRecorder(), []map[string]any{
		{"name": "팀장 승인", "role": "procurement_manager"},
		{"name": "재무 승인", "role": "finance"},
	})
	if !ok {
		t.Fatal("a two-step rule was refused")
	}
	for i, step := range steps {
		if step["order"] != i {
			t.Errorf("step %d is numbered %v", i, step["order"])
		}
	}
}

// TestAnApprovalRuleCanBeCorrectedWhereItWasWritten calls the door.
//
// Two things are checked here, and they are the two halves of the same panel.
// An edit cannot empty the steps a rule routes by — that was accepted, and the
// next matching submission was stranded 결재중 with an approval nobody could
// see. And an edit that turns a rule off actually stops it: a rule written
// with a wrong 최소 금액 used to be permanent, because 활성 could not be
// changed from anywhere.
func TestAnApprovalRuleCanBeCorrectedWhereItWasWritten(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs and these would silently no-op.
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_instances WHERE object_id IN (SELECT id FROM business_objects WHERE number IN('PAY-WFEDIT1','PAY-WFEDIT2'))`)
		_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE number IN('PAY-WFEDIT1','PAY-WFEDIT2')`)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_definitions WHERE object_type='payment'`)
	})
	// Nothing installs a 지급 rule with the schema, so any that is here is a
	// leftover, and a second enabled rule would route the submission this test
	// expects the switched-off one to let past.
	_, _ = pool.Exec(ctx, `DELETE FROM workflow_definitions WHERE object_type='payment'`)
	if _, err := pool.Exec(ctx, `INSERT INTO settings(key,value,category) VALUES('workflow.approval_enabled','true','workflow')
		ON CONFLICT(key) DO UPDATE SET value='true'`); err != nil {
		t.Fatalf("turn the approval process on: %v", err)
	}

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.211:5000"))
	send := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	created := send(http.MethodPost, "/api/v1/workflows",
		`{"name":"지급 승인 수정 검증","objectType":"payment","enabled":true,"conditions":{"minAmount":1},
		  "steps":[{"name":"구매 관리자 승인","role":"procurement_manager"}]}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("creating the rule returned %d: %s", created.Code, created.Body.String())
	}
	var rule struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &rule); err != nil {
		t.Fatalf("decode the rule: %v", err)
	}

	// An edit is held to the checks the creation passed.
	for _, badly := range []string{`[]`, `"구매 관리자 승인"`, `[{"role":"finance"}]`} {
		w := send(http.MethodPatch, "/api/v1/workflows/"+rule.ID, `{"steps":`+badly+`}`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("editing the steps to %s returned %d, want 400: %s", badly, w.Code, w.Body.String())
		}
		var stored []map[string]any
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT steps FROM workflow_definitions WHERE id=$1`, rule.ID).Scan(&raw); err != nil {
			t.Fatalf("read the stored steps: %v", err)
		}
		if err := json.Unmarshal(raw, &stored); err != nil || len(stored) == 0 {
			t.Fatalf("the rule now routes by %s, so anything it matches is stranded 결재중", raw)
		}
	}

	// The correction the panel exists for: a second step, and the rule still
	// routes the submission it was written for.
	fixed := send(http.MethodPatch, "/api/v1/workflows/"+rule.ID,
		`{"name":"지급 승인 수정 검증","enabled":true,"conditions":{"minAmount":1},
		  "steps":[{"name":"구매 관리자 승인","role":"procurement_manager"},{"name":"재무 승인","role":"finance"}]}`)
	if fixed.Code != http.StatusOK {
		t.Fatalf("correcting the rule returned %d: %s", fixed.Code, fixed.Body.String())
	}
	var paymentID string
	if err := pool.QueryRow(ctx, `INSERT INTO business_objects(object_type,number,title,status,amount,owner_id,created_by)
		VALUES('payment','PAY-WFEDIT1','수정된 승인 경로 검증','draft',1000000,
		       (SELECT id FROM users WHERE email=$1),(SELECT id FROM users WHERE email=$1)) RETURNING id`,
		testAdminEmail).Scan(&paymentID); err != nil {
		t.Fatalf("seed the payment: %v", err)
	}
	submitted := send(http.MethodPost, "/api/v1/payments/"+paymentID+"/submit", `{}`)
	if submitted.Code != http.StatusOK {
		t.Fatalf("submitting the payment returned %d: %s", submitted.Code, submitted.Body.String())
	}
	var answer struct {
		Status     string `json:"status"`
		InstanceID string `json:"instanceId"`
	}
	if err := json.Unmarshal(submitted.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the submission: %v", err)
	}
	if answer.Status != "pending_approval" {
		t.Fatalf("the payment came back %q rather than waiting for the corrected rule", answer.Status)
	}
	// The corrected steps are what the approver is shown, and the request is in
	// the 승인함 rather than nowhere.
	inbox := send(http.MethodGet, "/api/v1/approvals", "")
	if inbox.Code != http.StatusOK {
		t.Fatalf("reading the 승인함 returned %d: %s", inbox.Code, inbox.Body.String())
	}
	if !strings.Contains(inbox.Body.String(), answer.InstanceID) {
		t.Errorf("the approval the corrected rule opened is in nobody's 승인함: %s", inbox.Body.String())
	}

	// And the rule can be stopped. A wrong rule used to be permanent.
	if w := send(http.MethodPatch, "/api/v1/workflows/"+rule.ID,
		`{"name":"지급 승인 수정 검증 (중지)","enabled":false}`); w.Code != http.StatusOK {
		t.Fatalf("switching the rule off returned %d: %s", w.Code, w.Body.String())
	}
	var stoppedID string
	if err := pool.QueryRow(ctx, `INSERT INTO business_objects(object_type,number,title,status,amount,owner_id,created_by)
		VALUES('payment','PAY-WFEDIT2','중지된 승인 경로 검증','draft',1000000,
		       (SELECT id FROM users WHERE email=$1),(SELECT id FROM users WHERE email=$1)) RETURNING id`,
		testAdminEmail).Scan(&stoppedID); err != nil {
		t.Fatalf("seed the second payment: %v", err)
	}
	after := send(http.MethodPost, "/api/v1/payments/"+stoppedID+"/submit", `{}`)
	if after.Code != http.StatusOK {
		t.Fatalf("submitting the second payment returned %d: %s", after.Code, after.Body.String())
	}
	var second struct {
		Status          string `json:"status"`
		WorkflowApplied bool   `json:"workflowApplied"`
	}
	if err := json.Unmarshal(after.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode the second submission: %v", err)
	}
	if second.WorkflowApplied {
		t.Errorf("a rule switched off still routed a submission (status %q)", second.Status)
	}
	// The steps the edit did not mention are still the ones it was corrected to.
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT steps FROM workflow_definitions WHERE id=$1`, rule.ID).Scan(&raw); err != nil {
		t.Fatalf("read the stored steps: %v", err)
	}
	var stored []map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil || len(stored) != 2 {
		t.Errorf("an edit that only named 활성 rewrote the steps to %s", raw)
	}
}
