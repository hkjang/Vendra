package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// tsStringList answers the quoted strings of a `export const name = [...]`
// declaration in a TypeScript source, whether the entries are bare strings or
// objects carrying a value.
func tsStringList(t *testing.T, source, name string) []string {
	t.Helper()
	declaration := regexp.MustCompile(`(?s)\b` + name + `[^=]*=\s*\[(.*?)\];`).FindStringSubmatch(source)
	if declaration == nil {
		t.Fatalf("web/src/status.ts no longer declares %s; the two surfaces can drift again", name)
	}
	if strings.Contains(declaration[1], "value:") {
		var values []string
		for _, m := range regexp.MustCompile(`value:\s*"([^"]*)"`).FindAllStringSubmatch(declaration[1], -1) {
			values = append(values, m[1])
		}
		return values
	}
	var values []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(declaration[1], -1) {
		values = append(values, m[1])
	}
	return values
}

// TestTheApprovalStatusesTheListShowsAreTheOnesTheWorkflowAwards holds the
// screen and the workflow to one vocabulary, the way the supplier statuses are.
//
// workflowOwnedStatus is the closed set of statuses only the approval path may
// write. The object list has to be able to show every one of them and to say
// which of them a request can still be sent from — and it wrote neither list
// down, comparing against the single word "draft" instead. So 보완 요청, the
// decision whose entire purpose is that the request comes back and goes round
// again, produced an object the list would not submit, would not select and had
// no filter option to find.
func TestTheApprovalStatusesTheListShowsAreTheOnesTheWorkflowAwards(t *testing.T) {
	source := repoFile(t, "web/src/status.ts")
	offered := tsStringList(t, source, "approvalStatuses")

	// Every status the workflow awards is on the list, plus the draft they are
	// all submitted from.
	shown := map[string]bool{}
	for _, status := range offered {
		shown[status] = true
	}
	for status := range workflowOwnedStatus {
		if !shown[status] {
			t.Errorf("the workflow awards %q and approvalStatuses does not list it, so an object in that "+
				"state shows the raw English word and has no filter option to be found by", status)
		}
	}
	if !shown["draft"] {
		t.Error("approvalStatuses does not list draft, which is the state every request is submitted from")
	}
	for _, status := range offered {
		if status != "draft" && !workflowOwnedStatus[status] {
			t.Errorf("approvalStatuses offers %q, which the approval path never writes", status)
		}
	}
	// Every one of them carries a Korean label, or the badge falls back to the
	// raw word in the middle of the list.
	for _, m := range regexp.MustCompile(`value:\s*"([^"]*)",\s*label:\s*"([^"]*)"`).FindAllStringSubmatch(
		regexp.MustCompile(`(?s)\bapprovalStatuses[^=]*=\s*\[(.*?)\];`).FindStringSubmatch(source)[1], -1) {
		if strings.TrimSpace(m[2]) == "" {
			t.Errorf("%s has no label", m[1])
		}
	}

	// The filter offers the approval lifecycle whole. 보완 요청 was the one it
	// left out, and it is the one state whose owner has to go looking for it.
	filters := tsStringList(t, source, "objectStatusFilters")
	filtered := map[string]bool{}
	for _, status := range filters {
		filtered[status] = true
	}
	for _, status := range offered {
		if !filtered[status] {
			t.Errorf("the 상태 filter has no option for %q, so nobody can list the objects sitting in it", status)
		}
	}

	// And the states a request may be filed from are real ones. Anything the
	// workflow has already settled is not among them: submitting an object that
	// is with the approvers is answered as already submitted, and one already
	// approved would start a second approval for a decided request.
	submittable := tsStringList(t, source, "submittableStatuses")
	if len(submittable) == 0 {
		t.Fatal("submittableStatuses is empty; nothing could be sent for approval at all")
	}
	sent := map[string]bool{}
	for _, status := range submittable {
		sent[status] = true
		if !shown[status] {
			t.Errorf("submittableStatuses names %q, which is not a status the approval lifecycle has", status)
		}
	}
	if !sent["draft"] || !sent["returned"] {
		t.Errorf("submittableStatuses is %v; draft and returned are the two states a request is filed from", submittable)
	}
	for _, settled := range []string{"pending_approval", "approved", "rejected"} {
		if sent[settled] {
			t.Errorf("submittableStatuses offers to file an object that is already %s", settled)
		}
	}
}

// TestTheObjectListDoesNotWriteItsStatusesOutAgain is the check that was missing
// when the supplier statuses came apart, applied before the same thing happens
// here: a page that spells the vocabulary out for itself is a second list, and
// two lists drift.
func TestTheObjectListDoesNotWriteItsStatusesOutAgain(t *testing.T) {
	page := repoFile(t, "web/src/pages/Objects.tsx")
	source := repoFile(t, "web/src/status.ts")
	for _, status := range tsStringList(t, source, "objectStatusFilters") {
		if strings.Contains(page, `<option value="`+status+`"`) {
			t.Errorf("Objects.tsx writes the status filter out again (%s); that is how the two lists came apart", status)
		}
	}
	// Comparing against the bare word is the shape the defect had: the submit
	// button, the row checkbox and the bulk action each tested status ==
	// "draft", so a returned request was excluded from all three at once.
	if regexp.MustCompile(`status\s*[=!]==?\s*"draft"`).MatchString(page) {
		t.Error(`Objects.tsx compares a status against "draft" directly; use canSubmitForApproval, ` +
			`or 보완 요청 falls out of the submit button, the checkbox and the bulk action again`)
	}
}

// TestAReturnedRequestCanBeSubmittedAgain covers the loop 보완 요청 exists to
// close, end to end.
//
// An approver has three decisions. 승인 advances the request, 반려 ends it, and
// 보완 요청 hands it back so the author can fix what was flagged and file it
// again — that last one is the whole difference between the second and the
// third. The API has always allowed the resubmission; the object list was what
// did not offer it, because the submit button, the row's checkbox and the bulk
// action all asked whether the status was "draft". A returned request was
// therefore unreachable from the screen it lives on, and the only way forward
// was to type the whole thing in again as a new request — which is 반려, with
// nobody told.
func TestAReturnedRequestCanBeSubmittedAgain(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	var adminID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&adminID); err != nil {
		t.Fatalf("read the admin: %v", err)
	}
	// The setting is shared, so it is restored rather than deleted.
	var originalApprovals []byte
	if err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key='workflow.approval_enabled'`).Scan(&originalApprovals); err != nil {
		t.Fatalf("read the approval setting: %v", err)
	}
	var definitionID string
	if err := pool.QueryRow(ctx, `INSERT INTO workflow_definitions(name,object_type,enabled,conditions,steps,created_by) VALUES('보완 요청 검증','contract',true,'{}',$1,$2) RETURNING id`,
		`[{"name":"재무 승인","role":"","order":0}]`, adminID).Scan(&definitionID); err != nil {
		t.Fatalf("seed the workflow: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs and these would silently no-op.
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_actions WHERE instance_id IN(SELECT id FROM workflow_instances WHERE definition_id=$1)`, definitionID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_instances WHERE definition_id=$1`, definitionID)
		_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE title LIKE 'RETURNLOOP %'`)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_definitions WHERE id=$1`, definitionID)
		_, _ = pool.Exec(ctx, `UPDATE settings SET value=$1 WHERE key='workflow.approval_enabled'`, originalApprovals)
	})
	if _, err := pool.Exec(ctx, `UPDATE settings SET value='true'::jsonb WHERE key='workflow.approval_enabled'`); err != nil {
		t.Fatalf("enable approvals: %v", err)
	}

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "198.51.100.61:5000"))
	send := func(method, path string, payload any) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(payload)
		r := httptest.NewRequest(method, path, strings.NewReader(string(body)))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	statusOf := func(id string) string {
		t.Helper()
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM business_objects WHERE id=$1`, id).Scan(&status); err != nil {
			t.Fatalf("read the status: %v", err)
		}
		return status
	}
	submit := func(id string) string {
		t.Helper()
		w := send(http.MethodPost, "/api/v1/contracts/"+id+"/submit", map[string]any{})
		if w.Code != http.StatusOK {
			t.Fatalf("submitting returned %d: %s", w.Code, w.Body.String())
		}
		var out struct {
			Status           string `json:"status"`
			InstanceID       string `json:"instanceId"`
			AlreadySubmitted bool   `json:"alreadySubmitted"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode the submission: %v", err)
		}
		if out.Status != "pending_approval" || out.InstanceID == "" {
			t.Fatalf("submitting answered %s", w.Body.String())
		}
		if out.AlreadySubmitted {
			t.Fatalf("submitting was answered as a duplicate: %s", w.Body.String())
		}
		return out.InstanceID
	}

	w := send(http.MethodPost, "/api/v1/contracts", map[string]any{"title": "RETURNLOOP 단가계약", "amount": 900_000_000, "status": "draft"})
	if w.Code != http.StatusCreated {
		t.Fatalf("seeding the contract returned %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	first := submit(created.ID)
	if now := statusOf(created.ID); now != "pending_approval" {
		t.Fatalf("a submitted contract is %s, want pending_approval", now)
	}

	// The approver hands it back. Returning one's own request stays allowed —
	// it advances nothing — which is why this is testable from one account.
	if w := send(http.MethodPost, "/api/v1/approvals/"+first+"/actions",
		map[string]any{"action": "return", "comment": "정산 조건을 보완해 주세요"}); w.Code != http.StatusOK {
		t.Fatalf("returning the request answered %d: %s", w.Code, w.Body.String())
	}
	if now := statusOf(created.ID); now != "returned" {
		t.Fatalf("a returned request is %s, want returned", now)
	}

	// The author fixes what was flagged. A returned object is not one the
	// workflow still owns, so the ordinary edit has to go through.
	if w := send(http.MethodPatch, "/api/v1/contracts/"+created.ID,
		map[string]any{"title": "RETURNLOOP 단가계약 보완"}); w.Code != http.StatusOK {
		t.Fatalf("editing a returned contract answered %d: %s", w.Code, w.Body.String())
	}

	// And files it again. This is the step the screen gave no way to take.
	second := submit(created.ID)
	if second == first {
		t.Error("resubmitting reported the approval that had already been returned")
	}
	if now := statusOf(created.ID); now != "pending_approval" {
		t.Errorf("a resubmitted contract is %s, want pending_approval", now)
	}

	// Exactly one approval is open for it: the returned one is settled and the
	// new one is the only thing in anybody's inbox.
	var open, total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='pending'),count(*) FROM workflow_instances WHERE object_id=$1`, created.ID).Scan(&open, &total); err != nil {
		t.Fatalf("count the approvals: %v", err)
	}
	if open != 1 || total != 2 {
		t.Errorf("the contract has %d open approvals out of %d, want 1 of 2", open, total)
	}
	var statuses []string
	rows, err := pool.Query(ctx, `SELECT status FROM workflow_instances WHERE object_id=$1`, created.ID)
	if err != nil {
		t.Fatalf("read the approvals: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatalf("scan an approval: %v", err)
		}
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	if strings.Join(statuses, ",") != "pending,returned" {
		t.Errorf("the approvals read %v, want the returned one and one pending", statuses)
	}
}
