package httpapi

import (
	"fmt"
	"testing"
)

// The spend door is hung straight on every tool now, but two of them still
// answer the shut door with a number.
//
// search_suppliers and compare_suppliers select
// `CASE WHEN $5 THEN annual_spend ELSE 0 END`, so a role without the money
// permission reads `"annualSpend":0`. The two siblings that withhold the very
// same column behind the very same flag select `CASE WHEN $N THEN annual_spend
// END` and answer `"annualSpend":null` — recommend_suppliers and the AI context.
//
// suppliers.annual_spend is `numeric(20,2) NOT NULL DEFAULT 0`, so a null can
// only ever have come from the withholding: null means "not shown to you" and 0
// means "this company is a supplier we spend nothing with". Those are different
// facts and the column can carry both, which is exactly what these two tools
// throw away. A tool answer is dropped whole into a model's context with no
// second source to check it against, so `{"name":"SC-MINE","annualSpend":0}`
// reaches the person who asked as a figure rather than as a refusal.
//
// Both halves are asserted against a supplier whose spend is really zero, in
// the same call as one whose spend is not, because "0 is withheld" and "0 is the
// figure" are only distinguishable when both appear at once.
func TestWithheldSupplierSpendIsNullNotZeroOnEveryTool(t *testing.T) {
	w := newScopeWorld(t)
	second := seedSecondOwnSupplier(t, w)
	const mineSpend = 12345678.0
	seedAnnualSpend(t, w, w.mySupplier, mineSpend)
	// Written out rather than left at the column default so the intent is on
	// the page: this company really is one we spend nothing with.
	seedAnnualSpend(t, w, second, 0)

	compareArgs := fmt.Sprintf(`{"supplierIds":[%q,%q]}`, w.mySupplier, second)
	tools := []struct{ name, args string }{
		{"search_suppliers", `{"query":"SC-MINE"}`},
		{"compare_suppliers", compareArgs},
		// The sibling that already answers null. It is called in the same run,
		// with the same role and the same two suppliers, so the shape the other
		// two are being held to is read off a live answer rather than assumed
		// from the query text — jsonb_build_object keeping a null-valued key is
		// the assumption this pins down.
		{"recommend_suppliers", `{}`},
	}

	// A role holding none of the three wordings of the money door.
	grantOnly(t, w, `["supplier.read"]`)
	withheld := map[string]map[string]any{}
	for _, tool := range tools {
		withheld[tool.name] = spendValueByName(t, w, tool.name, tool.args)
		for _, name := range []string{"SC-MINE", "SC-MINE-2"} {
			if got := withheld[tool.name][name]; got != nil {
				t.Errorf("%s answered annualSpend %#v for %s to a role with no money permission: a number reads as the figure, and this column cannot distinguish a withheld figure from a real zero unless the withheld one is null",
					tool.name, got, name)
			}
		}
	}

	// The same role, the same suppliers, with the door open: the real figure,
	// and a real zero that is a zero rather than a null.
	grantOnly(t, w, `["supplier.read","supplier.financial.read"]`)
	shown := map[string]map[string]any{}
	for _, tool := range tools {
		shown[tool.name] = spendValueByName(t, w, tool.name, tool.args)
		if got := shown[tool.name]["SC-MINE"]; got != mineSpend {
			t.Errorf("%s answered annualSpend %#v for SC-MINE to a role holding supplier.financial.read, want %v", tool.name, got, mineSpend)
		}
		// The contrast this whole test exists for: the same key, on the same
		// tool, in the same answer, carries 0 for the company we spend nothing
		// with and null for the company whose figure is being withheld.
		if got := shown[tool.name]["SC-MINE-2"]; got != 0.0 {
			t.Errorf("%s answered annualSpend %#v for SC-MINE-2, want 0 — a supplier whose annual_spend really is zero must read as zero, not as withheld", tool.name, got)
		}
		if withheld[tool.name]["SC-MINE-2"] == shown[tool.name]["SC-MINE-2"] {
			t.Errorf("%s answers %#v for SC-MINE-2 whether the figure is withheld or really zero, so the two facts are indistinguishable in the model's context",
				tool.name, shown[tool.name]["SC-MINE-2"])
		}
	}

	// All three tools answer the same question about the same supplier, so they
	// have to answer it the same way — including recommend_suppliers, which
	// this change does not touch and which is therefore the reference.
	for _, name := range []string{"SC-MINE", "SC-MINE-2"} {
		for _, tool := range tools {
			if got, want := withheld[tool.name][name], withheld["recommend_suppliers"][name]; got != want {
				t.Errorf("with the door shut, %s answered annualSpend %#v for %s but recommend_suppliers answered %#v", tool.name, got, name, want)
			}
			if got, want := shown[tool.name][name], shown["recommend_suppliers"][name]; got != want {
				t.Errorf("with the door open, %s answered annualSpend %#v for %s but recommend_suppliers answered %#v", tool.name, got, name, want)
			}
		}
	}
}

// spendValueByName reads the annualSpend each row of a list-answering tool
// carries without casting it, keyed by supplier name.
//
// The existing spendByName cannot be used here: it reads the figure as
// `m["annualSpend"].(float64)` with the error dropped, so a null arrives as 0
// and the difference this test is about is invisible through it. The key has to
// stay present as well — a tool that dropped it altogether would also read as
// nil here — so that is asserted rather than left to the nil.
func spendValueByName(t *testing.T, w *scopeWorld, tool, args string) map[string]any {
	t.Helper()
	rec := callMCPTool(t, w, w.deptToken, tool, args)
	out := map[string]any{}
	for _, row := range toolRows(t, rec) {
		m, ok := row.(map[string]any)
		if !ok {
			t.Fatalf("%s %s answered a row that is not an object: %#v", tool, args, row)
		}
		name, _ := m["name"].(string)
		spend, present := m["annualSpend"]
		if !present {
			t.Errorf("%s %s dropped the annualSpend key for %s: a caller cannot tell an absent key from a column it was never going to be shown", tool, args, name)
		}
		out[name] = spend
	}
	if len(out) == 0 {
		t.Fatalf("%s %s answered no rows, so this proves nothing: %s", tool, args, rec.Body.String())
	}
	return out
}
