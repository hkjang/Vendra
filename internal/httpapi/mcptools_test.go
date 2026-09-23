package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func callMCPTool(t *testing.T, w *scopeWorld, token, name, args string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, name, args)
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

// toolRows decodes a successful tool answer, failing the test if the tool
// reported an error instead.
func toolRows(t *testing.T, rec *httptest.ResponseRecorder) []any {
	t.Helper()
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

// toolFailure decodes the message a tool answered with, failing the test if the
// call never reached the tool or if the tool answered normally.
func toolFailure(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Result struct {
			Content []struct {
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
		t.Fatalf("the call was refused before the tool ran: %s", envelope.Error.Message)
	}
	if !envelope.Result.IsError {
		t.Fatalf("the tool answered instead of failing: %s", rec.Body.String())
	}
	if len(envelope.Result.Content) == 0 {
		t.Fatal("the tool failed without a message")
	}
	return envelope.Result.Content[0].Text
}

func seedContracts(t *testing.T, w *scopeWorld, n int) {
	t.Helper()
	_, pool := newTestApp(t)
	ctx := context.Background()
	var org *string
	if err := pool.QueryRow(ctx, `SELECT organization_id::text FROM suppliers WHERE id=$1`, w.mySupplier).Scan(&org); err != nil {
		t.Fatalf("read organisation: %v", err)
	}
	t.Cleanup(func() {
		// t.Context() is already cancelled once cleanup runs.
		_, _ = pool.Exec(context.Background(), `DELETE FROM business_objects WHERE number LIKE 'BULK-%'`)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO business_objects(object_type,number,supplier_id,organization_id,title,status,amount,end_date)
		SELECT 'contract','BULK-'||n,$1::uuid,$2::uuid,'대량 계약','active',1000,current_date+(n||' days')::interval
		FROM generate_series(1,$3) n`, w.mySupplier, org, n); err != nil {
		t.Fatalf("seed contracts: %v", err)
	}
}

// get_expiring_contracts never ran: PostgreSQL cannot resolve "date + $1",
// because date plus an untyped parameter is ambiguous. Every call came back as
// a generic failure, which is the right thing to tell a caller and the reason
// nobody noticed.
func TestExpiringContractsToolReturnsContracts(t *testing.T) {
	w := newScopeWorld(t)
	seedContracts(t, w, 5)

	if _, err := w.pool.Exec(context.Background(), `UPDATE business_objects SET end_date=current_date+CASE number
		WHEN 'BULK-1' THEN 30 WHEN 'BULK-2' THEN 180 WHEN 'BULK-3' THEN 181
		WHEN 'BULK-4' THEN 3650 WHEN 'BULK-5' THEN 3651 END WHERE number LIKE 'BULK-%'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args string
		want int
	}{
		{`{}`, 2}, {`{"days":30}`, 1}, {`{"days":3650}`, 4},
		{`{"days":3651}`, 4}, {`{"days":0.5}`, 2}, {`{"days":1e100}`, 4},
	} {
		t.Run(tc.args, func(t *testing.T) {
			rows := toolRows(t, callMCPTool(t, w, w.deptToken, "get_expiring_contracts", tc.args))
			if len(rows) != tc.want {
				t.Fatalf("got %d contracts, want %d", len(rows), tc.want)
			}
		})
	}
}

// Every answer becomes part of a model's prompt, so a caller must not be able
// to widen one into the whole table.
func TestMCPToolAnswersAreBounded(t *testing.T) {
	w := newScopeWorld(t)
	seedContracts(t, w, 250)

	for _, tc := range []struct{ name, args string }{
		{"get_expiring_contracts", `{}`},
		{"get_expiring_contracts", `{"days":3650000}`},
		{"search_contracts", `{}`},
		{"search_suppliers", `{"query":""}`},
	} {
		t.Run(tc.name+" "+tc.args, func(t *testing.T) {
			rows := toolRows(t, callMCPTool(t, w, w.deptToken, tc.name, tc.args))
			if len(rows) > 100 {
				t.Errorf("the tool answered with %d rows; one call should not carry the table", len(rows))
			}
		})
	}
}

func TestIntNumberStaysInRange(t *testing.T) {
	for _, tc := range []struct {
		name           string
		value          any
		def, max, want int
	}{
		{"absent", nil, 180, 3650, 180},
		{"not a number", "180", 180, 3650, 180},
		{"zero", float64(0), 180, 3650, 180},
		{"negative", float64(-5), 180, 3650, 180},
		{"below one", float64(0.5), 180, 3650, 180},
		{"huge", float64(1e100), 180, 3650, 3650},
		{"fraction", float64(30.9), 180, 3650, 30},
		{"inside the range", float64(30), 180, 3650, 30},
		{"at the ceiling", float64(3650), 180, 3650, 3650},
		{"past the ceiling", float64(3650000), 180, 3650, 3650},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := intNumber(tc.value, tc.def, tc.max); got != tc.want {
				t.Errorf("intNumber(%v) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

// TestMCPToolResultsAreBounded covers the two tool queries that had no LIMIT.
// A tool result is dropped whole into a model's context, so an unbounded one is
// not a slow query, it is a call the caller cannot afford: 502 risks on a single
// supplier serialised to 445 KB, where the capped tools stayed under 40 KB
// against twenty thousand suppliers.
func TestMCPToolResultsAreBounded(t *testing.T) {
	w := newScopeWorld(t)
	pool := w.pool
	ctx := context.Background()

	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs.
		_, _ = pool.Exec(ctx, `DELETE FROM risks WHERE risk_type='MCPBOUND'`)
		_, _ = pool.Exec(ctx, `DELETE FROM evaluations WHERE evaluation_type='MCPBOUND'`)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO risks(supplier_id,risk_type,severity,probability,impact,status,description)
		SELECT $1,'MCPBOUND','HIGH',(g%10),(g%10),'open','경계 검증 리스크 '||g FROM generate_series(1,150) g`, w.mySupplier); err != nil {
		t.Fatalf("seed the risks: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO evaluations(supplier_id,evaluation_type,status,total_score,grade,scores)
		SELECT $1,'MCPBOUND','completed',(g%100),'B','{}'::jsonb FROM generate_series(1,150) g`, w.mySupplier); err != nil {
		t.Fatalf("seed the evaluations: %v", err)
	}

	for _, tc := range []struct{ tool, args string }{
		{"get_supplier_risk", fmt.Sprintf(`{"supplierId":%q}`, w.mySupplier)},
		{"get_supplier_score", fmt.Sprintf(`{"supplierId":%q}`, w.mySupplier)},
	} {
		rows := toolRows(t, callMCPTool(t, w, w.deptToken, tc.tool, tc.args))
		if len(rows) > 100 {
			t.Errorf("%s returned %d rows; a tool result has to fit in a context window", tc.tool, len(rows))
		}
		if len(rows) == 0 {
			t.Errorf("%s returned nothing, so the bound is not what is being measured", tc.tool)
		}
	}

	// The bound keeps the rows that matter: risks come back worst first, so the
	// tail that is dropped is the least severe.
	rows := toolRows(t, callMCPTool(t, w, w.deptToken, "get_supplier_risk", fmt.Sprintf(`{"supplierId":%q}`, w.mySupplier)))
	previous := 1e9
	for i, row := range rows {
		item, _ := row.(map[string]any)
		score, _ := item["score"].(float64)
		if score > previous {
			t.Fatalf("row %d scored %v after %v: the bound is cutting an unordered list", i, score, previous)
		}
		previous = score
	}
}

// A tool that is handed a supplier name where a record id belongs has to say
// so. The caller is a language model reading eleven descriptions at once, and
// it guesses; the only way it corrects itself is the message it gets back.
//
// The object tools and get_supplier already answer in words. The two that go
// through supplierScopeAllowed did not: a name reaches PostgreSQL as a failed
// uuid cast, the lookup returns an error, and an errored lookup was
// indistinguishable there from a supplier one department over — so the model
// was told "data scope denied" and relayed that to the person who asked as a
// permission problem with a supplier that was never looked up at all.
func TestASupplierToolSaysWhenItWasGivenANameInsteadOfARecordID(t *testing.T) {
	w := newScopeWorld(t)

	for _, tc := range []struct{ tool, args string }{
		{"get_supplier_risk", `{"supplierId":"대한전자"}`},
		{"get_supplier_score", `{"supplierId":"대한전자"}`},
		// Controls: these carried the message already and have to keep it.
		{"search_contracts", `{"supplierId":"대한전자"}`},
		{"search_purchase_orders", `{"supplierId":"대한전자"}`},
		{"compare_suppliers", `{"supplierIds":["대한전자","한빛소재"]}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			text := toolFailure(t, callMCPTool(t, w, w.deptToken, tc.tool, tc.args))
			if !strings.Contains(text, "record id") {
				t.Errorf("%s answered %q, which does not tell the model its argument was not a record id", tc.tool, text)
			}
			if strings.Contains(text, "scope") {
				t.Errorf("%s answered %q: the model's own mistake is reported as a permission problem", tc.tool, text)
			}
		})
	}

	// An argument left out entirely is the same mistake, and get_supplier
	// already names the argument it wanted.
	for _, tool := range []string{"get_supplier", "get_supplier_risk", "get_supplier_score"} {
		t.Run(tool+" with no argument", func(t *testing.T) {
			text := toolFailure(t, callMCPTool(t, w, w.deptToken, tool, `{}`))
			if !strings.Contains(text, "supplierId") {
				t.Errorf("%s answered %q, which does not name the argument it needs", tool, text)
			}
		})
	}

	// The refusal that has to survive the new message: a real id the caller
	// cannot reach is still a scope refusal.
	for _, tool := range []string{"get_supplier", "get_supplier_risk", "get_supplier_score"} {
		t.Run(tool+" one department over", func(t *testing.T) {
			text := toolFailure(t, callMCPTool(t, w, w.deptToken, tool, fmt.Sprintf(`{"supplierId":%q}`, w.theirSupplier)))
			if strings.Contains(text, "record id") {
				t.Errorf("%s answered %q for a supplier that exists; a real id is not an argument mistake", tool, text)
			}
		})
	}

	// And a supplier the caller can reach still answers with its rows.
	for _, tool := range []string{"get_supplier_risk", "get_supplier_score"} {
		t.Run(tool+" in scope", func(t *testing.T) {
			if rows := toolRows(t, callMCPTool(t, w, w.deptToken, tool, fmt.Sprintf(`{"supplierId":%q}`, w.mySupplier))); len(rows) != 1 {
				t.Errorf("%s returned %d rows for the caller's own supplier, want 1", tool, len(rows))
			}
		})
	}
}
