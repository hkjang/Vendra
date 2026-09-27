package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// intNumber reads anything that is not a JSON number as the argument having
// been left out, so the count arguments the tools advertise were the last place
// a mistake in a call was answered with a number the caller never asked for.
//
// get_expiring_contracts is where that costs the most. `{"days":"30"}` — a
// model quoting a number, which is the mistake `minScore:"80"` already showed
// it makes — fell back to the 180일 기본값, so the answer carried contracts
// expiring four months out and the model relays them to the person who asked as
// expiring within the month. There is nothing in the answer to say the window
// was not the one requested. `{"limit":"5"}` is the same shape one step
// milder: a hundred rows returned as the five that were asked for.
//
// seedWindowedContracts plants one contract inside a 30일 창 and one well
// outside it, both in the caller's own department, so an answer built on a wider
// window than the call asked for is visible in the rows themselves. The numbers
// carry the fixture's SC- prefix so wipe removes them.
func seedWindowedContracts(t *testing.T, w *scopeWorld) {
	t.Helper()
	if _, err := w.pool.Exec(context.Background(),
		`INSERT INTO business_objects(object_type,number,supplier_id,organization_id,title,status,end_date)
		 SELECT 'contract',v.num,s.id,s.organization_id,v.num,'active',current_date+(v.days||' days')::interval
		 FROM suppliers s JOIN (VALUES ('SC-CT-30',30),('SC-CT-120',120)) AS v(num,days) ON s.supplier_number='SC-MINE'`); err != nil {
		t.Fatalf("seed contracts: %v", err)
	}
}

// seedLimitSuppliers adds suppliers in the caller's department so a limit that
// was thrown away is a different row count from a limit that was applied. The
// shared fixture carries one reachable supplier, where every limit looks alike.
func seedLimitSuppliers(t *testing.T, w *scopeWorld) {
	t.Helper()
	if _, err := w.pool.Exec(context.Background(),
		`INSERT INTO suppliers(supplier_number,business_number,name,status,organization_id,owner_id,annual_spend,score)
		 SELECT 'SC-LIM-'||g,'SC-LIM-'||g,'SC-LIM-'||g,'active',organization_id,owner_id,1000-g,90
		 FROM suppliers,generate_series(1,3) g WHERE supplier_number='SC-MINE'`); err != nil {
		t.Fatalf("seed suppliers: %v", err)
	}
}

// mcpAnswer reads a tool's reply without asserting which kind it is, so a case
// can report the rows a call answered with when it should have named a mistake
// instead. toolRows and refusal each fail the test on the other outcome.
func mcpAnswer(t *testing.T, w *scopeWorld, tool, args string) ([]any, string) {
	t.Helper()
	rec := callMCPTool(t, w, w.deptToken, tool, args)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s: HTTP %d: %s", tool, args, rec.Code, rec.Body.String())
	}
	var envelope struct {
		Result struct {
			StructuredContent []any `json:"structuredContent"`
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
		t.Fatalf("%s %s was refused before the tool ran: %s", tool, args, envelope.Error.Message)
	}
	if envelope.Result.IsError {
		if len(envelope.Result.Content) == 0 {
			t.Fatalf("%s %s failed without a message: %s", tool, args, rec.Body.String())
		}
		return nil, envelope.Result.Content[0].Text
	}
	return envelope.Result.StructuredContent, ""
}

// rowNumbers names the rows an answer carried, so a failure says which records
// came back rather than only how many.
func rowNumbers(rows []any) []string {
	out := []string{}
	for _, row := range rows {
		m, _ := row.(map[string]any)
		num, _ := m["number"].(string)
		if num == "" {
			num, _ = m["name"].(string)
		}
		out = append(out, num)
	}
	sort.Strings(out)
	return out
}

func TestMCPCountArgumentsRefuseValuesThatAreNotNumbers(t *testing.T) {
	w := newScopeWorld(t)
	seedWindowedContracts(t, w)
	seedLimitSuppliers(t, w)

	// The window is the case where the silent default answers with records the
	// call excluded, rather than with more of the records it asked for.
	if rows, failure := mcpAnswer(t, w, "get_expiring_contracts", `{"days":"30"}`); failure == "" {
		t.Errorf(`get_expiring_contracts {"days":"30"} answered %v: "30" is not a JSON number, so the window became the 180일 기본값 and the answer carries a contract expiring four months out as one expiring inside the month the caller asked about`, rowNumbers(rows))
	}

	// Every count argument names itself and quotes the value back, the way the
	// text and score arguments already do, so the model can see which argument
	// to correct instead of reading a wider answer as the one it asked for.
	for _, tc := range []struct {
		tool, args string
		want       []string
	}{
		{"get_expiring_contracts", `{"days":"30"}`, []string{"get_expiring_contracts", "days", "30"}},
		{"get_expiring_contracts", `{"days":true}`, []string{"days", "true"}},
		{"get_expiring_contracts", `{"days":{"from":1}}`, []string{"days"}},
		{"search_suppliers", `{"query":"","limit":"1"}`, []string{"search_suppliers", "limit", "1"}},
		{"search_suppliers", `{"query":"","limit":[1]}`, []string{"limit"}},
		{"analyze_spend", `{"limit":"1"}`, []string{"analyze_spend", "limit", "1"}},
		{"analyze_spend", `{"limit":false}`, []string{"limit", "false"}},
		{"recommend_suppliers", `{"limit":"1"}`, []string{"recommend_suppliers", "limit", "1"}},
	} {
		rows, failure := mcpAnswer(t, w, tc.tool, tc.args)
		if failure == "" {
			t.Errorf("%s %s answered with %d rows (%v) instead of naming the argument it could not read", tc.tool, tc.args, len(rows), rowNumbers(rows))
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(failure, want) {
				t.Errorf("%s %s answered %q, which does not name %q", tc.tool, tc.args, failure, want)
			}
		}
	}
}

// The refusals must not have cost the tools a call that was always correct:
// absent, null, a number inside the range, and the numbers outside the
// advertised range that deliberately keep falling back rather than failing.
func TestMCPCountArgumentsStillAnswerWellFormedCalls(t *testing.T) {
	w := newScopeWorld(t)
	seedWindowedContracts(t, w)
	seedLimitSuppliers(t, w)

	for _, tc := range []struct {
		name, args string
		want       []string
	}{
		{"30일", `{"days":30}`, []string{"SC-CT-30"}},
		{"29일은 아무것도 아니다", `{"days":29}`, []string{}},
		{"120일", `{"days":120}`, []string{"SC-CT-120", "SC-CT-30"}},
		{"생략은 180일", `{}`, []string{"SC-CT-120", "SC-CT-30"}},
		{"null 은 180일", `{"days":null}`, []string{"SC-CT-120", "SC-CT-30"}},
		{"1 아래는 기본값", `{"days":0.5}`, []string{"SC-CT-120", "SC-CT-30"}},
		{"상한 위는 상한", `{"days":1e100}`, []string{"SC-CT-120", "SC-CT-30"}},
	} {
		rows, failure := mcpAnswer(t, w, "get_expiring_contracts", tc.args)
		if failure != "" {
			t.Errorf("%s: get_expiring_contracts %s was refused: %s", tc.name, tc.args, failure)
			continue
		}
		if got := rowNumbers(rows); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: get_expiring_contracts %s returned %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}

	// The fixture's own supplier plus the three seeded ones, so an applied limit
	// and a discarded one are different counts.
	for _, tc := range []struct {
		tool, args string
		want       int
	}{
		{"search_suppliers", `{"query":"","limit":1}`, 1},
		{"search_suppliers", `{"query":"","limit":2}`, 2},
		{"search_suppliers", `{"query":""}`, 4},
		{"search_suppliers", `{"query":"","limit":null}`, 4},
		{"analyze_spend", `{"limit":1}`, 1},
		{"analyze_spend", `{}`, 4},
		{"recommend_suppliers", `{"limit":1}`, 1},
		{"recommend_suppliers", `{}`, 4},
	} {
		rows, failure := mcpAnswer(t, w, tc.tool, tc.args)
		if failure != "" {
			t.Errorf("%s %s was refused: %s", tc.tool, tc.args, failure)
			continue
		}
		if len(rows) != tc.want {
			t.Errorf("%s %s returned %d rows (%v), want %d", tc.tool, tc.args, len(rows), rowNumbers(rows), tc.want)
		}
	}
}
