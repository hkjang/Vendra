package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// stringValue answers "" for anything that is not a string, and "" is what
// these tools read as "no filter". So a model that sent `query:1001` to
// search_suppliers — the description says 공급업체 번호로 검색, which invites a
// number — ran `name ILIKE '%%'` and got the first hundred suppliers by name
// back as its search result, and get_supplier_issues, whose schema declares
// supplierId required, answered with every issue in scope when the argument
// arrived missing or numeric. An empty answer would be survivable; a full one
// is worse, because the model reads it as fact and repeats it to a person.
//
// seedScopeIssues plants issues on both departments' suppliers so an answer
// that has lost its supplier filter is visibly different from one that has
// not. The numbers carry the fixture's SC- prefix so wipe removes them.
func seedScopeIssues(t *testing.T, w *scopeWorld) {
	t.Helper()
	// The MCP gate wants issue.read for get_supplier_issues and the shared
	// fixture's role does not carry it — which is why nothing in the tree had
	// ever reached this tool. Granting it on the fixture's own roles leaves
	// every other scope assertion alone, and a principal is rebuilt from the
	// roles on each request, so the tokens already issued pick it up.
	if _, err := w.pool.Exec(context.Background(),
		`UPDATE roles SET permissions=permissions||'["issue.read"]'::jsonb WHERE code LIKE 'scope%'`); err != nil {
		t.Fatalf("grant issue.read: %v", err)
	}
	if _, err := w.pool.Exec(context.Background(),
		`INSERT INTO business_objects(object_type,number,supplier_id,organization_id,title,status)
		 SELECT 'issue',v.num,s.id,s.organization_id,v.num,'open'
		 FROM suppliers s JOIN (VALUES
			('SC-ISS-MINE-1','SC-MINE'),
			('SC-ISS-MINE-2','SC-MINE'),
			('SC-ISS-THEIRS-1','SC-THEIRS')
		 ) AS v(num,tag) ON s.supplier_number=v.tag`); err != nil {
		t.Fatalf("seed issues: %v", err)
	}
}

// toolNumbers reads the "number" of every row in a successful answer, sorted so
// the assertion does not depend on the tool's ordering.
func toolNumbers(t *testing.T, w *scopeWorld, tool, args string) []string {
	t.Helper()
	rec := callMCPTool(t, w, w.deptToken, tool, args)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s: HTTP %d: %s", tool, args, rec.Code, rec.Body.String())
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

// refusal asserts the tool answered with a message rather than with rows, and
// returns it so the caller can check the argument and the value are both named.
func refusal(t *testing.T, w *scopeWorld, tool, args string) string {
	t.Helper()
	rec := callMCPTool(t, w, w.deptToken, tool, args)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s: HTTP %d: %s", tool, args, rec.Code, rec.Body.String())
	}
	return toolFailure(t, rec)
}

func TestMCPSearchToolsRefuseFiltersThatAreNotText(t *testing.T) {
	w := newScopeWorld(t)
	seedScopeIssues(t, w)

	// Each case names the argument it got wrong and quotes the value back, so
	// the model can see which of its arguments to correct rather than reading
	// a filterless answer as the truth.
	for _, tc := range []struct {
		tool, args string
		want       []string
	}{
		{"search_suppliers", `{"query":1001}`, []string{"query", "1001"}},
		{"search_suppliers", `{"query":true}`, []string{"query", "true"}},
		{"search_suppliers", `{"query":{"name":"SC-MINE"}}`, []string{"query"}},
		{"search_suppliers", `{"query":["SC-MINE"]}`, []string{"query"}},
		{"search_contracts", `{"query":42}`, []string{"search_contracts", "query", "42"}},
		{"search_contracts", `{"supplierId":42}`, []string{"search_contracts", "supplierId", "42"}},
		{"search_purchase_orders", `{"query":42}`, []string{"search_purchase_orders", "query", "42"}},
		{"search_purchase_orders", `{"supplierId":42}`, []string{"search_purchase_orders", "supplierId", "42"}},
	} {
		got := refusal(t, w, tc.tool, tc.args)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s %s answered %q, which does not name %q", tc.tool, tc.args, got, want)
			}
		}
	}
}

func TestMCPSupplierIssuesRefusesAMissingOrNonTextSupplierID(t *testing.T) {
	w := newScopeWorld(t)
	seedScopeIssues(t, w)

	// The schema declares supplierId required, so the mistakes below are the
	// ones a model actually makes, and every one of them used to be answered
	// with all the issues the caller can reach. Rather than restate the
	// wording, each answer is compared against get_supplier_risk's answer to
	// the same mistake: the three tools take the same argument, so a model
	// that learns to correct one has learned to correct all three, and this
	// assertion keeps them saying the same thing if the wording is ever
	// rewritten.
	for _, args := range []string{
		`{}`,
		`{"supplierId":null}`,
		`{"supplierId":1001}`,
		`{"supplierId":true}`,
		`{"supplierId":{"name":"SC-MINE"}}`,
		`{"supplierId":"SC-MINE"}`,
	} {
		got := refusal(t, w, "get_supplier_issues", args)
		want := refusal(t, w, "get_supplier_risk", args)
		if strings.ReplaceAll(got, "get_supplier_issues", "get_supplier_risk") != want {
			t.Errorf("%s: get_supplier_issues answered %q but get_supplier_risk answered %q", args, got, want)
		}
	}
	// A name where an id belongs reaches PostgreSQL as a failed uuid cast, so
	// it has to be refused before the query, naming the value.
	got := refusal(t, w, "get_supplier_issues", `{"supplierId":"SC-MINE"}`)
	if !strings.Contains(got, "record id, not a name") || !strings.Contains(got, "SC-MINE") {
		t.Errorf("get_supplier_issues with a name answered %q, want it to say it wants a record id and quote the name", got)
	}
}

// The refusals above must not have cost the tools any of the calls that were
// always correct: a partial-text search, a number search, an id filter, and the
// department scope that keeps the neighbouring team's records out.
func TestMCPSearchToolsStillAnswerWellFormedCalls(t *testing.T) {
	w := newScopeWorld(t)
	seedScopeIssues(t, w)

	for _, tc := range []struct {
		name, tool, args string
		want             []string
	}{
		{"이름 부분 일치", "search_suppliers", `{"query":"MINE"}`, []string{"SC-MINE"}},
		{"번호 전체", "search_suppliers", `{"query":"SC-THEIRS"}`, []string{}},
		{"빈 query 는 전부", "search_suppliers", `{"query":""}`, []string{"SC-MINE"}},
		{"query 생략은 전부", "search_suppliers", `{}`, []string{"SC-MINE"}},
		{"계약 번호 부분 일치", "search_contracts", `{"query":"MY-CT"}`, []string{"SC-MY-CT"}},
		{"계약 빈 query 는 전부", "search_contracts", `{"query":""}`, []string{"SC-MY-CT"}},
		{"발주서 부분 일치", "search_purchase_orders", `{"query":"MY-PO"}`, []string{"SC-MY-PO"}},
		{"이슈는 공급업체 하나만", "get_supplier_issues", `{"supplierId":"` + w.mySupplier + `"}`, []string{"SC-ISS-MINE-1", "SC-ISS-MINE-2"}},
		{"옆 부서 공급업체의 이슈는 없다", "get_supplier_issues", `{"supplierId":"` + w.theirSupplier + `"}`, []string{}},
	} {
		got := toolNumbers(t, w, tc.tool, tc.args)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: %s %s returned %v, want %v", tc.name, tc.tool, tc.args, got, tc.want)
		}
	}

	// supplierId as a filter rather than as the whole query: the fixture's two
	// contracts sit one per department, so a filter that has gone missing shows
	// up as the neighbouring team's row appearing.
	if got := toolNumbers(t, w, "search_contracts", `{"supplierId":"`+w.mySupplier+`"}`); strings.Join(got, ",") != "SC-MY-CT" {
		t.Errorf("search_contracts filtered by supplierId returned %v, want [SC-MY-CT]", got)
	}
}
