package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// The recommendation tool's description and 사용자 가이드 4.6 both say a call can
// shortlist "품목, 최소 점수, 최대 위험 등급", but the schema carried no grade
// argument and the query was fixed at `risk_level NOT IN('CRITICAL')`. A model
// that believed the description and sent a ceiling got HIGH suppliers back in a
// shortlist it had asked to stop at MEDIUM, with nothing in the answer saying
// the argument had been dropped. The same branch read minScore with a bare
// float64 assertion, so `"80"` became 0 and the floor disappeared entirely.
//
// recommendSeed plants one supplier per grade plus a grade outside the
// vocabulary, and a low-scoring one for the floor to exclude.
func recommendSeed(t *testing.T, w *scopeWorld) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture's own two suppliers would otherwise land in the shortlist. The
	// caller's is soft deleted so the answers keep proving deleted rows stay
	// out, and the neighbouring department's is made fully eligible so they keep
	// proving the scope holds with a ceiling in play.
	exec(`UPDATE suppliers SET deleted_at=now() WHERE id=$1`, w.mySupplier)
	exec(`UPDATE suppliers SET status='active',score=100,risk_level='LOW',categories='["rec"]' WHERE id=$1`, w.theirSupplier)
	// risk_level is a plain text column with no constraint behind it, so
	// 'SC-REC-blank' stands for what a legacy import or an older form could have
	// written: a grade the vocabulary does not name.
	exec(`INSERT INTO suppliers(supplier_number,business_number,name,status,organization_id,owner_id,score,risk_level,categories,annual_spend)
		SELECT v.tag,v.tag,v.tag,'active',u.organization_id,u.id,v.score,v.risk,'["rec"]'::jsonb,1000
		FROM users u CROSS JOIN (VALUES
			('SC-REC-critical',99,'CRITICAL'),
			('SC-REC-low',95,'LOW'),
			('SC-REC-medium',90,'MEDIUM'),
			('SC-REC-high',85,'HIGH'),
			('SC-REC-blank',80,''),
			('SC-REC-lowscore',50,'LOW')
		) AS v(tag,score,risk) WHERE u.email='scope-dept@vendra.test'`)
}

// recommendNames reads a successful shortlist, checking on the way that the
// structured answer and the text answer say the same thing — toolRows only
// looks at structuredContent.
func recommendNames(t *testing.T, w *scopeWorld, token, args string) []string {
	t.Helper()
	rec := callMCPTool(t, w, token, "recommend_suppliers", args)
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
		t.Fatalf("expected one text result: %s", rec.Body.String())
	}
	var textRows []any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &textRows); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(structured, textRows) {
		t.Fatal("structured and text rows differ")
	}
	names := []string{}
	for _, row := range structured {
		item, ok := row.(map[string]any)
		if !ok {
			t.Fatalf("row is not an object: %#v", row)
		}
		names = append(names, item["name"].(string))
	}
	return names
}

func TestRecommendSuppliersAppliesTheRiskCeilingItAdvertises(t *testing.T) {
	w := newScopeWorld(t)
	t.Cleanup(func() { wipe(t, w.pool) }) // wipe uses context.Background even during cleanup.
	recommendSeed(t, w)

	for _, tc := range []struct {
		name, args string
		want       []string
	}{
		// Unchanged behaviour: no ceiling excludes CRITICAL and nothing else,
		// so the supplier carrying a grade outside the vocabulary is still
		// shortlisted. Turning the default path into a whitelist would have
		// dropped it.
		{"no ceiling", `{"category":"rec","minScore":80}`,
			[]string{"SC-REC-low", "SC-REC-medium", "SC-REC-high", "SC-REC-blank"}},
		{"no ceiling and no floor", `{"category":"rec"}`,
			[]string{"SC-REC-low", "SC-REC-medium", "SC-REC-high", "SC-REC-blank", "SC-REC-lowscore"}},
		{"ceiling HIGH", `{"category":"rec","minScore":80,"maxRisk":"HIGH"}`,
			[]string{"SC-REC-low", "SC-REC-medium", "SC-REC-high"}},
		{"ceiling MEDIUM", `{"category":"rec","minScore":80,"maxRisk":"MEDIUM"}`,
			[]string{"SC-REC-low", "SC-REC-medium"}},
		{"ceiling LOW", `{"category":"rec","minScore":80,"maxRisk":"LOW"}`,
			[]string{"SC-REC-low"}},
		// The ceiling is an extra condition, not a replacement for the floor or
		// the limit the tool already applied.
		{"ceiling with the floor doing the work", `{"category":"rec","minScore":90,"maxRisk":"HIGH"}`,
			[]string{"SC-REC-low", "SC-REC-medium"}},
		{"ceiling with a limit", `{"category":"rec","minScore":80,"maxRisk":"HIGH","limit":2}`,
			[]string{"SC-REC-low", "SC-REC-medium"}},
		// An empty ceiling is the argument being absent, not a filter that
		// matches nothing.
		{"ceiling omitted as an empty string", `{"category":"rec","minScore":80,"maxRisk":""}`,
			[]string{"SC-REC-low", "SC-REC-medium", "SC-REC-high", "SC-REC-blank"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := recommendNames(t, w, w.deptToken, tc.args)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("shortlist %v, want %v", got, tc.want)
			}
		})
	}

	// The ceiling is no way around the data scope: the `own` user owns none of
	// these suppliers, and the eligible supplier one department over stays out
	// of every answer above.
	t.Run("own scope sees none of them", func(t *testing.T) {
		if got := recommendNames(t, w, w.ownToken, `{"category":"rec","maxRisk":"HIGH"}`); len(got) != 0 {
			t.Fatalf("shortlist %v, want none", got)
		}
	})
}

func TestRecommendSuppliersNamesArgumentsItCannotApply(t *testing.T) {
	w := newScopeWorld(t)
	t.Cleanup(func() { wipe(t, w.pool) })

	for _, tc := range []struct {
		name, args string
		// The message has to carry the value the caller sent, or a model cannot
		// tell which of its arguments to correct.
		wants []string
	}{
		{"korean grade", `{"category":"rec","maxRisk":"매우높음"}`, []string{"maxRisk", "매우높음", "MEDIUM"}},
		{"lower case grade", `{"category":"rec","maxRisk":"low"}`, []string{"maxRisk", `"low"`, "LOW"}},
		// CRITICAL is not offered as a ceiling: the tool never recommends one,
		// so accepting it would only restate the default.
		{"critical as a ceiling", `{"category":"rec","maxRisk":"CRITICAL"}`, []string{"maxRisk", "CRITICAL"}},
		{"grade as a number", `{"category":"rec","maxRisk":3}`, []string{"maxRisk"}},
		{"score as text", `{"category":"rec","minScore":"80"}`, []string{"minScore", `"80"`, "number"}},
		{"score as a boolean", `{"category":"rec","minScore":true}`, []string{"minScore", "number"}},
		{"score as an object", `{"category":"rec","minScore":{"min":80}}`, []string{"minScore", "number"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := toolFailure(t, callMCPTool(t, w, w.deptToken, "recommend_suppliers", tc.args))
			for _, want := range tc.wants {
				if !strings.Contains(message, want) {
					t.Fatalf("message %q does not mention %q", message, want)
				}
			}
		})
	}

	// An absent or null argument is not a mistake — both tools' callers leave
	// them out, and the shortlist then applies no floor and no ceiling.
	for _, args := range []string{`{"category":"rec"}`, `{"category":"rec","minScore":null,"maxRisk":null}`} {
		t.Run("accepted: "+args, func(t *testing.T) {
			recommendNames(t, w, w.deptToken, args)
		})
	}
}

// TestMCPRecommendSchemaOffersTheRiskCeiling needs no database: mcpTools is a
// package variable and tools/list hands it out as it stands.
func TestMCPRecommendSchemaOffersTheRiskCeiling(t *testing.T) {
	// The ceiling ranks grades by riskGrades order, not by how they sort as
	// text, where "HIGH" < "LOW" < "MEDIUM". If that slice is ever reordered or
	// renamed, the ranking below stops meaning what it says.
	if got := strings.Join(riskGrades, " "); got != "LOW MEDIUM HIGH CRITICAL" {
		t.Fatalf("riskGrades is %q; the ceiling assumes ascending order ending at CRITICAL", got)
	}
	var schema map[string]any
	for _, tool := range mcpTools {
		if tool["name"] == "recommend_suppliers" {
			schema = tool["inputSchema"].(map[string]any)
		}
	}
	if schema == nil {
		t.Fatal("recommend_suppliers is not in mcpTools")
	}
	field, ok := schema["properties"].(map[string]any)["maxRisk"].(map[string]any)
	if !ok {
		t.Fatalf("the schema has no maxRisk: %#v", schema["properties"])
	}
	if field["type"] != "string" {
		t.Fatalf("maxRisk type is %v, want string", field["type"])
	}
	if got := field["enum"]; !reflect.DeepEqual(got, mcpRiskCeilings) {
		t.Fatalf("maxRisk enum is %#v, want %#v", got, mcpRiskCeilings)
	}
	// Advertising CRITICAL would invite a ceiling the tool cannot honour: it
	// has never recommended one.
	for _, grade := range mcpRiskCeilings {
		if grade == "CRITICAL" {
			t.Fatal("CRITICAL is offered as a ceiling")
		}
	}
	if len(mcpRiskCeilings) != len(riskGrades)-1 {
		t.Fatalf("ceilings %v do not cover every grade below CRITICAL", mcpRiskCeilings)
	}
}
