package httpapi

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A tool's description in tools/list is the whole of what a model knows about
// what the tool answers. It is read once, before any call, and it is what the
// model picks the tool by — so a description naming a dimension the answer does
// not carry does not merely oversell: it routes the question to the wrong tool
// and then leaves the model holding rows that cannot answer it. Asked which of
// two suppliers has more open issues, a model that was told compare_suppliers
// compares 이슈 calls it, gets eight fields with no issue among them, and has
// nowhere to go but "neither has any" or the gap in its own context.
//
// mcpComparisonDimensions is the vocabulary of that promise: every dimension a
// comparison description can claim, the words that claim it, and the keys in an
// answered row that would carry it. The 계약 and 이슈 keys are the names such
// data would arrive under if compare_suppliers ever did gather it — listed so
// that adding the claim back without the data fails here, and so that adding
// the data does not have to guess at a new vocabulary.
var mcpComparisonDimensions = []struct {
	dimension string
	claimedBy []string
	carriedBy []string
}{
	{"비용", []string{"비용", "지출"}, []string{"annualSpend"}},
	{"평가", []string{"평가", "점수"}, []string{"score", "grade"}},
	{"위험", []string{"위험", "리스크"}, []string{"riskLevel"}},
	{"계약", []string{"계약"}, []string{"contracts", "contractCount", "activeContracts"}},
	{"이슈", []string{"이슈"}, []string{"issues", "issueCount", "openIssues"}},
}

// claimedDimensions names the dimensions a sentence about comparing suppliers
// promises, by any of the words that promise them.
func claimedDimensions(text string) map[string]bool {
	claimed := map[string]bool{}
	for _, d := range mcpComparisonDimensions {
		for _, word := range d.claimedBy {
			if strings.Contains(text, word) {
				claimed[d.dimension] = true
			}
		}
	}
	return claimed
}

// mcpToolDescription returns the description a tool publishes through
// tools/list, which is the same map the handler answers that method from.
func mcpToolDescription(t *testing.T, name string) string {
	t.Helper()
	for _, tool := range mcpTools {
		if got, _ := tool["name"].(string); got != name {
			continue
		}
		description, _ := tool["description"].(string)
		if description == "" {
			t.Fatalf("%s publishes no description", name)
		}
		return description
	}
	t.Fatalf("mcpTools has no tool named %s", name)
	return ""
}

func sortedRowKeys(row map[string]any) []string {
	keys := make([]string, 0, len(row))
	for key := range row {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestCompareSuppliersAnswersEveryDimensionItsDescriptionClaims holds the
// published description to the rows the running tool answers with, through a
// real session and POST /mcp rather than against the query text, so a dimension
// counts as compared only when it arrives in the response body.
func TestCompareSuppliersAnswersEveryDimensionItsDescriptionClaims(t *testing.T) {
	w := newScopeWorld(t)
	second := seedSecondOwnSupplier(t, w)

	description := mcpToolDescription(t, "compare_suppliers")
	claimed := claimedDimensions(description)
	if len(claimed) < 2 {
		t.Fatalf("compare_suppliers' description %q claims %d dimensions, so this comparison proves nothing", description, len(claimed))
	}

	args := fmt.Sprintf(`{"supplierIds":[%q,%q]}`, w.mySupplier, second)
	rec := callMCPTool(t, w, w.deptToken, "compare_suppliers", args)
	if rec.Code != http.StatusOK {
		t.Fatalf("compare_suppliers %s: HTTP %d: %s", args, rec.Code, rec.Body.String())
	}
	rows := toolRows(t, rec)
	if len(rows) != 2 {
		t.Fatalf("compare_suppliers answered with %d row(s), want the two suppliers it was asked to compare: %s", len(rows), rec.Body.String())
	}

	// Every row is built by the same function, so a dimension missing from one
	// is missing from the comparison; reported once, with the keys that did
	// arrive, so the message says what the model is actually left holding.
	missing := map[string]bool{}
	for _, row := range rows {
		m, _ := row.(map[string]any)
		for _, d := range mcpComparisonDimensions {
			if !claimed[d.dimension] || missing[d.dimension] {
				continue
			}
			carried := false
			for _, key := range d.carriedBy {
				if _, ok := m[key]; ok {
					carried = true
				}
			}
			if !carried {
				missing[d.dimension] = true
				t.Errorf("compare_suppliers' description %q says it compares %s, but the row it answered with carries none of %v — only %v, so the model has nothing to compare %s with",
					description, d.dimension, d.carriedBy, sortedRowKeys(m), d.dimension)
			}
		}
	}
}

// guideToolRow is the second cell of the row the user guide's MCP tool table
// gives a tool, which is where a person reads the same promise.
var guideToolRow = regexp.MustCompile("(?m)^\\|\\s*`([a-z_]+)`\\s*\\|([^|]*)\\|")

// TestUserGuideClaimsTheComparisonTheToolDescriptionDoes keeps the two accounts
// of compare_suppliers equal. 4.6 of the user guide lists all eleven tools with
// a one-line 「하는 일」, written by reading the descriptions, and a reader of
// the guide is making the same decision about the same tool as the model is —
// so a dimension dropped from one account and left in the other is the promise
// surviving in the place nobody rechecked.
func TestUserGuideClaimsTheComparisonTheToolDescriptionDoes(t *testing.T) {
	guide := repoFile(t, "docs/USER_GUIDE.md")
	row := ""
	for _, m := range guideToolRow.FindAllStringSubmatch(guide, -1) {
		if m[1] == "compare_suppliers" {
			row = strings.TrimSpace(m[2])
		}
	}
	if row == "" {
		t.Fatalf("docs/USER_GUIDE.md has no MCP tool table row for compare_suppliers, so this comparison proves nothing")
	}

	described := claimedDimensions(mcpToolDescription(t, "compare_suppliers"))
	documented := claimedDimensions(row)
	for _, d := range mcpComparisonDimensions {
		switch {
		case described[d.dimension] && !documented[d.dimension]:
			t.Errorf("compare_suppliers' description claims it compares %s, but the user guide's row (%q) does not", d.dimension, row)
		case documented[d.dimension] && !described[d.dimension]:
			t.Errorf("the user guide's compare_suppliers row (%q) claims it compares %s, which the tool no longer describes itself as doing", row, d.dimension)
		}
	}
}
