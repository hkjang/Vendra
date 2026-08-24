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

	rows := toolRows(t, callMCPTool(t, w, w.deptToken, "get_expiring_contracts", `{}`))
	if len(rows) == 0 {
		t.Fatal("the tool found no contracts although several expire inside the default window")
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
