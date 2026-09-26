package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// The two tools that read a supplier id through stringValue were the last ones
// that answered an argument mistake with something the model cannot act on.
//
// compare_suppliers' list went through stringSlice, which dropped every element
// that was not a string without a word: {"supplierIds":["<uuid>",42]} compared
// one supplier and returned it as a comparison, so the model reported the
// missing company to the person who asked as "that supplier does not exist" or
// "I cannot see it". A list of nothing but numbers landed on "compare_suppliers
// requires supplierIds" — the caller had supplied the argument, so that message
// sends it looking for the wrong mistake — and get_supplier said the same about
// {"supplierId":42}, and "supplier not found" about a name.
//
// seedSecondOwnSupplier adds a second supplier in the caller's own department so
// a comparison that has silently lost one of its two subjects is visibly
// different from one that has not. The number carries the fixture's SC- prefix
// so wipe removes it.
func seedSecondOwnSupplier(t *testing.T, w *scopeWorld) string {
	t.Helper()
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO suppliers(supplier_number,business_number,name,status,organization_id,owner_id)
		 SELECT 'SC-MINE-2','SC-MINE-2','SC-MINE-2','active',organization_id,owner_id
		 FROM suppliers WHERE supplier_number='SC-MINE' RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("seed second supplier: %v", err)
	}
	return id
}

// toolObject decodes the single object a tool such as get_supplier answers with.
// toolRows cannot: structuredContent is an object there, not an array.
func toolObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Result struct {
			StructuredContent map[string]any `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v\n  body: %s", err, rec.Body.String())
	}
	if envelope.Error != nil {
		t.Fatalf("the call was refused: %s", envelope.Error.Message)
	}
	if envelope.Result.IsError {
		text := ""
		if len(envelope.Result.Content) > 0 {
			text = envelope.Result.Content[0].Text
		}
		t.Fatalf("the tool failed: %s", text)
	}
	return envelope.Result.StructuredContent
}

// compared reads the number of every row compare_suppliers answered with,
// sorted so the assertion does not depend on the tool's ordering.
func compared(t *testing.T, w *scopeWorld, args string) []string {
	t.Helper()
	rec := callMCPTool(t, w, w.deptToken, "compare_suppliers", args)
	if rec.Code != http.StatusOK {
		t.Fatalf("compare_suppliers %s: HTTP %d: %s", args, rec.Code, rec.Body.String())
	}
	out := []string{}
	for _, row := range toolRows(t, rec) {
		m, _ := row.(map[string]any)
		num, _ := m["number"].(string)
		out = append(out, num)
	}
	sort.Strings(out)
	return out
}

func TestMCPCompareSuppliersRefusesIDsThatAreNotText(t *testing.T) {
	w := newScopeWorld(t)
	second := seedSecondOwnSupplier(t, w)

	// A list with one bad element used to answer with the elements that did
	// parse, and the schema publishes minItems 2, so a "comparison" of one was
	// returned as a normal answer. The refusal has to quote the value the
	// caller got wrong.
	for _, tc := range []struct {
		args string
		want []string
	}{
		{fmt.Sprintf(`{"supplierIds":[%q,42]}`, w.mySupplier), []string{"compare_suppliers", "supplierIds", "42"}},
		{fmt.Sprintf(`{"supplierIds":[42,%q]}`, w.mySupplier), []string{"supplierIds", "42"}},
		{fmt.Sprintf(`{"supplierIds":[%q,null]}`, w.mySupplier), []string{"supplierIds", "null"}},
		{fmt.Sprintf(`{"supplierIds":[%q,[%q]]}`, w.mySupplier, second), []string{"supplierIds"}},
		{`{"supplierIds":[1,2]}`, []string{"supplierIds", "1"}},
		// A single value where a list belongs is the same class of mistake: the
		// argument was supplied, so "requires supplierIds" sends the model
		// looking for the wrong thing.
		{fmt.Sprintf(`{"supplierIds":%q}`, w.mySupplier), []string{"supplierIds"}},
	} {
		got := toolFailure(t, callMCPTool(t, w, w.deptToken, "compare_suppliers", tc.args))
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("compare_suppliers %s answered %q, which does not name %q", tc.args, got, want)
			}
		}
		if strings.Contains(got, "requires supplierIds") {
			t.Errorf("compare_suppliers %s answered %q: the argument was supplied, so this reports the wrong mistake", tc.args, got)
		}
	}

	// The value really being absent keeps the message it always had.
	for _, args := range []string{`{}`, `{"supplierIds":[]}`, `{"supplierIds":null}`} {
		got := toolFailure(t, callMCPTool(t, w, w.deptToken, "compare_suppliers", args))
		if !strings.Contains(got, "compare_suppliers requires supplierIds") {
			t.Errorf("compare_suppliers %s answered %q, want it to name the argument it needs", args, got)
		}
	}
}

func TestMCPGetSupplierRefusesAnIDThatIsNotARecordID(t *testing.T) {
	w := newScopeWorld(t)

	// A number said "get_supplier requires supplierId" even though one was
	// given, and a name said "supplier not found" — which the model relays as
	// the company not existing rather than as its own mistake. Both are
	// compared against get_supplier_risk's answer to the same mistake, so the
	// three tools that take this argument keep saying the same thing.
	for _, args := range []string{`{"supplierId":42}`, `{"supplierId":true}`, `{"supplierId":{"name":"SC-MINE"}}`} {
		got := toolFailure(t, callMCPTool(t, w, w.deptToken, "get_supplier", args))
		if strings.Contains(got, "requires supplierId") {
			t.Errorf("get_supplier %s answered %q: the argument was supplied, so this reports the wrong mistake", args, got)
		}
		if !strings.Contains(got, "supplierId") {
			t.Errorf("get_supplier %s answered %q, which does not name the argument", args, got)
		}
	}
	if got := toolFailure(t, callMCPTool(t, w, w.deptToken, "get_supplier", `{"supplierId":42}`)); !strings.Contains(got, "42") {
		t.Errorf("get_supplier with 42 answered %q, which does not quote the value back", got)
	}

	// A name where a record id belongs: the sibling wording, not "not found".
	got := toolFailure(t, callMCPTool(t, w, w.deptToken, "get_supplier", `{"supplierId":"SC-MINE"}`))
	if !strings.Contains(got, "record id, not a name") || !strings.Contains(got, "SC-MINE") {
		t.Errorf("get_supplier with a name answered %q, want it to say it wants a record id and quote the name", got)
	}

	// The refusals that have to survive: absent stays "requires", a real id one
	// department over stays a scope refusal, and an id of the right shape that
	// nothing matches stays "not found".
	if got := toolFailure(t, callMCPTool(t, w, w.deptToken, "get_supplier", `{}`)); !strings.Contains(got, "get_supplier requires supplierId") {
		t.Errorf("get_supplier {} answered %q, want it to name the argument it needs", got)
	}
	if got := toolFailure(t, callMCPTool(t, w, w.deptToken, "get_supplier", fmt.Sprintf(`{"supplierId":%q}`, w.theirSupplier))); !strings.Contains(got, "scope") {
		t.Errorf("get_supplier one department over answered %q, want the scope refusal", got)
	}
	if got := toolFailure(t, callMCPTool(t, w, w.deptToken, "get_supplier", `{"supplierId":"00000000-0000-4000-8000-000000000000"}`)); !strings.Contains(got, "not found") {
		t.Errorf("get_supplier with an unused id answered %q, want \"supplier not found\"", got)
	}
}

// The refusals must not have cost either tool a call that was always correct —
// including the two argument names a hardcoded client may still be sending and
// the blank-value fall-through between them.
func TestMCPSupplierIDToolsStillAnswerWellFormedCalls(t *testing.T) {
	w := newScopeWorld(t)
	second := seedSecondOwnSupplier(t, w)

	for _, tc := range []struct {
		name, args string
		want       []string
	}{
		{"자기 부서 두 곳", fmt.Sprintf(`{"supplierIds":[%q,%q]}`, w.mySupplier, second), []string{"SC-MINE", "SC-MINE-2"}},
		{"옆 부서는 빠진다", fmt.Sprintf(`{"supplierIds":[%q,%q]}`, w.mySupplier, w.theirSupplier), []string{"SC-MINE"}},
		{"ids 별칭", fmt.Sprintf(`{"ids":[%q,%q]}`, w.mySupplier, second), []string{"SC-MINE", "SC-MINE-2"}},
		{"빈 supplierIds 는 ids 로 넘어간다", fmt.Sprintf(`{"supplierIds":[],"ids":[%q,%q]}`, w.mySupplier, second), []string{"SC-MINE", "SC-MINE-2"}},
	} {
		if got := compared(t, w, tc.args); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: compare_suppliers %s returned %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}

	for _, tc := range []struct{ name, args string }{
		{"supplierId", fmt.Sprintf(`{"supplierId":%q}`, w.mySupplier)},
		{"id 별칭", fmt.Sprintf(`{"id":%q}`, w.mySupplier)},
		{"빈 supplierId 는 id 로 넘어간다", fmt.Sprintf(`{"supplierId":"   ","id":%q}`, w.mySupplier)},
	} {
		got := toolObject(t, callMCPTool(t, w, w.deptToken, "get_supplier", tc.args))
		if num, _ := got["supplierNumber"].(string); num != "SC-MINE" {
			t.Errorf("%s: get_supplier %s returned %v, want SC-MINE", tc.name, tc.args, got["supplierNumber"])
		}
	}
}
