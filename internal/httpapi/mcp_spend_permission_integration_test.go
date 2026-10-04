package httpapi

import (
	"context"
	"fmt"
	"testing"
)

// The door onto a supplier's annual spend was cut twice, with different hinges.
//
// redactSupplier opens it for spend.read, analytics.read or
// supplier.financial.read; the summary queries next to it opened it for
// spend.read or analytics.read and had never heard of the third. So a role
// granted supplier.financial.read — the supplier-money permission the catalogue
// offers an administrator in exactly those words — read the real figure from
// get_supplier and a zero from search_suppliers, for the same supplier, in the
// same session, one call apart.
//
// A zero is not a withheld number, it is a number. The caller here is a model
// that has no way to tell the difference and no second source to check against,
// so "SC-MINE: annualSpend 0" is relayed to the person who asked as a supplier
// the company spends nothing with — a supplier it could have named the real
// figure for had it asked the other tool. These assertions read the figure back
// through both doors with one permission set, so neither can be moved without
// the other.
func TestSupplierSpendDoorOpensTheSameOnEveryTool(t *testing.T) {
	w := newScopeWorld(t)
	second := seedSecondOwnSupplier(t, w)
	const mineSpend, secondSpend = 12345678.0, 87654321.0
	seedAnnualSpend(t, w, w.mySupplier, mineSpend)
	seedAnnualSpend(t, w, second, secondSpend)

	// Only the supplier-money door, so the two wordings of the same question
	// disagree. spend.read and analytics.read are left out deliberately: with
	// either of them both readings already matched and there was nothing to see.
	grantOnly(t, w, `["supplier.read","supplier.financial.read"]`)

	// get_supplier goes through redactSupplier, which is also what the REST
	// list, the detail and the portal answer with — so this is the figure the
	// rest of the product shows this role.
	detail := toolObject(t, callMCPTool(t, w, w.deptToken, "get_supplier", fmt.Sprintf(`{"supplierId":%q}`, w.mySupplier)))
	if got, _ := detail["annualSpend"].(float64); got != mineSpend {
		t.Fatalf("get_supplier answered annualSpend %v for a role holding supplier.financial.read, want %v — the premise of this test is that this door is open", got, mineSpend)
	}

	for _, tc := range []struct {
		tool, args string
		want       map[string]float64
	}{
		{"search_suppliers", `{"query":"SC-MINE"}`, map[string]float64{"SC-MINE": mineSpend, "SC-MINE-2": secondSpend}},
		{"compare_suppliers", fmt.Sprintf(`{"supplierIds":[%q,%q]}`, w.mySupplier, second), map[string]float64{"SC-MINE": mineSpend, "SC-MINE-2": secondSpend}},
		{"recommend_suppliers", `{}`, map[string]float64{"SC-MINE": mineSpend, "SC-MINE-2": secondSpend}},
	} {
		for name, want := range spendByName(t, w, tc.tool, tc.args) {
			if expected, named := tc.want[name]; named && want != expected {
				t.Errorf("%s %s answered annualSpend %v for %s, but get_supplier answered %v for the same supplier in the same session: the model cannot tell a withheld figure from a real zero",
					tc.tool, tc.args, want, name, expected)
			}
		}
	}

	// The other direction: a role with none of the three must still be told
	// nothing, or this has opened the door rather than hung it straight.
	grantOnly(t, w, `["supplier.read"]`)
	if got, _ := toolObject(t, callMCPTool(t, w, w.deptToken, "get_supplier", fmt.Sprintf(`{"supplierId":%q}`, w.mySupplier)))["annualSpend"].(float64); got != 0 {
		t.Errorf("get_supplier answered annualSpend %v to a role with no money permission at all", got)
	}
	for _, tc := range []struct{ tool, args string }{
		{"search_suppliers", `{"query":"SC-MINE"}`},
		{"compare_suppliers", fmt.Sprintf(`{"supplierIds":[%q,%q]}`, w.mySupplier, second)},
		{"recommend_suppliers", `{}`},
	} {
		for name, got := range spendByName(t, w, tc.tool, tc.args) {
			if got != 0 {
				t.Errorf("%s %s answered annualSpend %v for %s to a role with no money permission at all", tc.tool, tc.args, got, name)
			}
		}
	}
}

// Each of the three wordings that open the door has to open it on every tool,
// and a role carrying none of them has to find all of them shut — checked one
// permission at a time so a single wording quietly falling out of one of the
// surfaces is a failure rather than something the others cover up.
func TestEverySupplierSpendPermissionIsReadTheSameWay(t *testing.T) {
	w := newScopeWorld(t)
	const mineSpend = 7654321.0
	seedAnnualSpend(t, w, w.mySupplier, mineSpend)

	for _, tc := range []struct {
		permissions string
		want        float64
	}{
		{`["supplier.read","spend.read"]`, mineSpend},
		{`["supplier.read","analytics.read"]`, mineSpend},
		{`["supplier.read","supplier.financial.read"]`, mineSpend},
		// The wildcards the shipped roles are actually written with. These are
		// here because the summary queries used to test for "*" by name beside
		// the three wordings, and dropping that in favour of the shared reading
		// is only safe if permissionMatches really does answer them — asserted
		// through a role row and a real session rather than taken on trust.
		{`["*"]`, mineSpend},
		{`["*.read"]`, mineSpend},
		{`["supplier.read","spend.*"]`, mineSpend},
		{`["supplier.read"]`, 0},
		{`["supplier.read","contract.read"]`, 0},
	} {
		grantOnly(t, w, tc.permissions)
		detail := toolObject(t, callMCPTool(t, w, w.deptToken, "get_supplier", fmt.Sprintf(`{"supplierId":%q}`, w.mySupplier)))
		got, _ := detail["annualSpend"].(float64)
		if got != tc.want {
			t.Errorf("%s: get_supplier answered annualSpend %v, want %v", tc.permissions, got, tc.want)
		}
		for _, tool := range []struct{ name, args string }{
			{"search_suppliers", `{"query":"SC-MINE"}`},
			{"recommend_suppliers", `{}`},
		} {
			summary := spendByName(t, w, tool.name, tool.args)["SC-MINE"]
			if summary != tc.want {
				t.Errorf("%s: %s answered annualSpend %v for SC-MINE while get_supplier answered %v, want both to say %v",
					tc.permissions, tool.name, summary, got, tc.want)
			}
		}
	}
}

// seedAnnualSpend writes a figure onto a supplier the fixture inserts without
// one: a column left at its default zero cannot show the difference between a
// figure withheld and a figure that is really zero, which is the whole subject.
func seedAnnualSpend(t *testing.T, w *scopeWorld, supplierID string, amount float64) {
	t.Helper()
	if _, err := w.pool.Exec(context.Background(), `UPDATE suppliers SET annual_spend=$2 WHERE id=$1`, supplierID, amount); err != nil {
		t.Fatalf("seed annual_spend: %v", err)
	}
}

// grantOnly replaces the fixture's permissions rather than adding to them. The
// shipped scope roles carry spend.*, under which every reading of the spend
// door already agrees — so the divergence is only visible once that is gone.
// The session reads its permissions per request, so the token stays valid.
func grantOnly(t *testing.T, w *scopeWorld, permissions string) {
	t.Helper()
	if _, err := w.pool.Exec(context.Background(), `UPDATE roles SET permissions=$1::jsonb WHERE code LIKE 'scope%'`, permissions); err != nil {
		t.Fatalf("set permissions: %v", err)
	}
}

// spendByName reads the annualSpend each row of a list-answering tool carries,
// keyed by the supplier name so the assertion does not depend on ordering. A
// row that omits the key or carries null reads as zero here, because that is
// what it amounts to for the model: no figure.
func spendByName(t *testing.T, w *scopeWorld, tool, args string) map[string]float64 {
	t.Helper()
	rec := callMCPTool(t, w, w.deptToken, tool, args)
	out := map[string]float64{}
	for _, row := range toolRows(t, rec) {
		m, _ := row.(map[string]any)
		name, _ := m["name"].(string)
		spend, _ := m["annualSpend"].(float64)
		out[name] = spend
	}
	if len(out) == 0 {
		t.Fatalf("%s %s answered no rows, so this proves nothing: %s", tool, args, rec.Body.String())
	}
	return out
}
