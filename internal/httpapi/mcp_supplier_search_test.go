package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestMCPSupplierNumberSearchMatchesREST(t *testing.T) {
	w := newScopeWorld(t)
	t.Cleanup(func() { wipe(t, w.pool) })
	// Keep SC- numbers for fixture cleanup, but separate all searchable fields:
	// otherwise a name/business-number match hides a missing number predicate.
	for _, s := range []struct{ id, name, business string }{
		{w.mySupplier, "Aurora Components", "7318294056"},
		{w.theirSupplier, "Borealis Materials", "9826401735"},
	} {
		if _, err := w.pool.Exec(context.Background(), `UPDATE suppliers SET name=$2,business_number=$3 WHERE id=$1`, s.id, s.name, s.business); err != nil {
			t.Fatal(err)
		}
	}
	check := func(t *testing.T, token, query, want string) {
		t.Helper()
		assertRows := func(t *testing.T, rows []any, global bool) {
			t.Helper()
			ids := []string{}
			for _, row := range rows {
				item, ok := row.(map[string]any)
				if !ok {
					t.Fatalf("invalid search row: %v", row)
				}
				if global && item["type"] != "supplier" {
					continue
				}
				id, ok := item["id"].(string)
				if !ok {
					t.Fatalf("missing supplier ID: %v", item)
				}
				ids = append(ids, id)
			}
			if want == "" {
				if len(ids) != 0 {
					t.Errorf("query %q returned excluded suppliers: %v", query, ids)
				}
			} else if len(ids) != 1 || ids[0] != want {
				t.Errorf("query %q supplier IDs = %v, want [%s]", query, ids, want)
			}
		}
		for _, path := range []string{"/api/v1/suppliers", "/api/v1/search"} {
			t.Run(path, func(t *testing.T) {
				rec := w.call(t, token, http.MethodGet, path+"?q="+url.QueryEscape(query), "")
				if rec.Code != http.StatusOK {
					t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
				}
				var result struct {
					Items []any `json:"items"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				assertRows(t, result.Items, path == "/api/v1/search")
			})
		}
		t.Run("MCP", func(t *testing.T) {
			rec := callMCPTool(t, w, token, "search_suppliers", fmt.Sprintf(`{"query":%q}`, query))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			assertRows(t, toolRows(t, rec), false)
			var envelope struct {
				Result struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.Result.Content) != 1 {
				t.Fatalf("expected one text result: %s", rec.Body.String())
			}
			var rows []any
			if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &rows); err != nil {
				t.Fatal(err)
			}
			assertRows(t, rows, false)
		})
	}
	for _, query := range []string{"SC-MINE", "sc-mine", "C-MI", "mi", "SC-", "Aurora", "7318294056"} {
		t.Run(query, func(t *testing.T) { check(t, w.deptToken, query, w.mySupplier) })
	}
	t.Run("other department", func(t *testing.T) { check(t, w.deptToken, "sc-theirs", "") })
	t.Run("colleague outside own scope", func(t *testing.T) { check(t, w.ownToken, "sc-mine", "") })
	// Prove the own-scoped caller can find the same record once it is theirs.
	if _, err := w.pool.Exec(context.Background(), `UPDATE suppliers SET owner_id=(SELECT id FROM users WHERE email='scope-own@vendra.test') WHERE id=$1`, w.mySupplier); err != nil {
		t.Fatal(err)
	}
	t.Run("own record", func(t *testing.T) { check(t, w.ownToken, "sc-mine", w.mySupplier) })
	if _, err := w.pool.Exec(context.Background(), `UPDATE suppliers SET deleted_at=now() WHERE id=$1`, w.mySupplier); err != nil {
		t.Fatal(err)
	}
	t.Run("deleted department record", func(t *testing.T) { check(t, w.deptToken, "sc-mine", "") })
	t.Run("deleted own record", func(t *testing.T) { check(t, w.ownToken, "sc-mine", "") })
}
