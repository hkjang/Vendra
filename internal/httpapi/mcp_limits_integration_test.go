package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"testing"
)

func TestMCPAdvertisedLimitsAreApplied(t *testing.T) {
	for _, scope := range []string{"department", "own"} {
		t.Run(scope, func(t *testing.T) {
			w := newScopeWorld(t)
			t.Cleanup(func() { wipe(t, w.pool) }) // wipe uses context.Background even during cleanup.
			ctx := context.Background()
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := w.pool.Exec(ctx, q, args...); err != nil {
					t.Fatal(err)
				}
			}
			token := w.deptToken
			if scope == "own" {
				token = w.ownToken
			}
			// Every excluded row would sort before the eligible rows in all three tools.
			exec(`UPDATE suppliers SET name='000 excluded',annual_spend=99999999,score=999,risk_level='LOW',categories='["limits"]' WHERE id IN ($1,$2)`, w.mySupplier, w.theirSupplier)
			if scope == "department" {
				exec(`UPDATE suppliers SET deleted_at=now() WHERE id=$1`, w.mySupplier)
			}
			exec(`INSERT INTO suppliers(supplier_number,business_number,name,status,organization_id,owner_id,annual_spend,score,risk_level,categories)
    SELECT 'SC-LIMIT-'||g,'SC-LIMIT-'||g,'Limit '||lpad(g::text,3,'0'),'active',u.organization_id,u.id,1000-g,100-g*0.1,'LOW','["limits"]'
    FROM users u CROSS JOIN generate_series(1,105) g WHERE u.email='scope-own@vendra.test'`)
			exec(`INSERT INTO suppliers(supplier_number,business_number,name,status,organization_id,owner_id,annual_spend,score,risk_level,categories,deleted_at)
    SELECT 'SC-LIMIT-DELETED','SC-LIMIT-DELETED','000 deleted','active',organization_id,id,99999999,999,'LOW','["limits"]',now() FROM users WHERE email='scope-own@vendra.test'`)
			ids := []string{}
			rows, err := w.pool.Query(ctx, `SELECT id::text FROM suppliers WHERE supplier_number LIKE 'SC-LIMIT-%' AND deleted_at IS NULL ORDER BY name`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(ids) != 105 {
				t.Fatalf("fixture has %d eligible rows", len(ids))
			}
			read := func(t *testing.T, tool, args string) []any {
				t.Helper()
				rec := callMCPTool(t, w, token, tool, args)
				if rec.Code != http.StatusOK {
					t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
				}
				structured := toolRows(t, rec)
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
					t.Fatal("expected one text result")
				}
				var textRows []any
				if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &textRows); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(structured, textRows) {
					t.Fatal("structured and text rows differ")
				}
				return structured
			}
			for _, tool := range []string{"search_suppliers", "analyze_spend", "recommend_suppliers"} {
				t.Run(tool, func(t *testing.T) {
					max := 100
					if tool == "recommend_suppliers" {
						max = 50
					}
					baseline := read(t, tool, `{"query":"","category":"limits","minScore":80}`)
					for _, tc := range []struct {
						name, value string
						want        int
					}{
						{"one", "1", 1}, {"two", "2", 2}, {"omitted", "", max}, {"ceiling", fmt.Sprint(max), max}, {"above", fmt.Sprint(max + 1), max},
						{"null", "null", max}, {"string", `"2"`, max}, {"zero", "0", max}, {"negative", "-1", max}, {"fraction below one", "0.5", max}, {"huge", "1e100", max}, {"fraction", "2.9", 2},
					} {
						t.Run(tc.name, func(t *testing.T) {
							args := `{"query":"","category":"limits","minScore":80`
							if tc.value != "" {
								args += `,"limit":` + tc.value
							}
							args += "}"
							got := read(t, tool, args)
							if len(got) != tc.want {
								t.Fatalf("got %d rows, want %d", len(got), tc.want)
							}
							for i, row := range got {
								item := row.(map[string]any)
								if item["id"] != ids[i] {
									t.Fatalf("row %d id=%v, want %s", i, item["id"], ids[i])
								}
								if item["annualSpend"] != float64(999-i) {
									t.Fatalf("spend=%v", item["annualSpend"])
								}
								if tool == "analyze_spend" {
									want := math.Round(100*float64(999-i)/99435*100) / 100 // all 105 accessible suppliers, before LIMIT
									if item["share"] != want || item["share"] != baseline[i].(map[string]any)["share"] {
										t.Fatalf("share=%v, want %v independent of limit", item["share"], want)
									}
								}
							}
						})
					}
				})
			}
			t.Run("advertised bounds", func(t *testing.T) {
				rec := w.call(t, token, "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
				var response struct {
					Result struct {
						Tools []struct {
							Name        string
							InputSchema struct {
								Properties map[string]struct{ Minimum, Maximum int }
							}
						}
					}
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				found := 0
				for _, tool := range response.Result.Tools {
					max := 0
					switch tool.Name {
					case "search_suppliers", "analyze_spend":
						max = 100
					case "recommend_suppliers":
						max = 50
					}
					if max == 0 {
						continue
					}
					found++
					bounds := tool.InputSchema.Properties["limit"]
					if bounds.Minimum != 1 || bounds.Maximum != max {
						t.Errorf("%s bounds=%+v", tool.Name, bounds)
					}
				}
				if found != 3 {
					t.Fatalf("found %d schemas", found)
				}
			})
			t.Run("recommendation filters", func(t *testing.T) {
				exec(`UPDATE suppliers SET status='inactive' WHERE id=$1`, ids[0])
				exec(`UPDATE suppliers SET risk_level='CRITICAL' WHERE id=$1`, ids[1])
				exec(`UPDATE suppliers SET categories='["other"]' WHERE id=$1`, ids[2])
				exec(`UPDATE suppliers SET score=70 WHERE id=$1`, ids[3])
				got := read(t, "recommend_suppliers", `{"category":"limits","minScore":80,"limit":2}`)
				if len(got) != 2 || got[0].(map[string]any)["id"] != ids[4] || got[1].(map[string]any)["id"] != ids[5] {
					t.Fatalf("filters returned %v", got)
				}
			})
			t.Run("amount permissions", func(t *testing.T) {
				exec(`UPDATE roles SET permissions='["supplier.read"]' WHERE code IN ('scope_department','scope_own')`)
				for _, tool := range []string{"search_suppliers", "recommend_suppliers"} {
					got := read(t, tool, `{"query":"","limit":1}`)
					if len(got) != 1 {
						t.Fatalf("%s: got %d rows", tool, len(got))
					}
					amount := got[0].(map[string]any)["annualSpend"]
					if tool == "search_suppliers" && amount != float64(0) || tool == "recommend_suppliers" && amount != nil {
						t.Fatalf("%s leaked amount %v", tool, amount)
					}
				}
			})
		})
	}
}
