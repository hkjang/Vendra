package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// business_objects.amount is opened by <objectType>.amount.read everywhere the
// column is carried — the contract detail and list (redactObject), the amount
// sort order, the MCP get_expiring_contracts tool and the AI context's expiring
// contracts all ask for contract.amount.read by that name. The dashboard's
// activeContractValue KPI sums the same column and had never heard of it: it
// accepted spend.read or analytics.read only.
//
// So a role granted contract.read + contract.amount.read + dashboard.read read
// the real figure from GET /api/v1/contracts/<id> and ₩0 from GET
// /api/v1/dashboard, for the same contracts, in the same session. The zero is
// the damaging half: it does not say "withheld", it says the company holds no
// active contract value — and the card rendering it puts a link to the contract
// list that answers the real amounts directly beside it. Reading both bodies in
// one session is the point of this test; either surface on its own looks
// self-consistent.
func TestDashboardReadsTheContractAmountDoor(t *testing.T) {
	w := newScopeWorld(t)
	const firstAmount, secondAmount, spend = 4200000.0, 1300000.0, 7654321.0
	second := seedOwnContract(t, w, "SC-MY-CT-2", secondAmount, "approved")
	seedContractAmount(t, w, w.myContract, firstAmount, "active")
	seedAnnualSpend(t, w, w.mySupplier, spend)
	total := firstAmount + secondAmount

	// The contract-amount door and nothing else from the money side: the
	// fixture's roles carry spend.*, under which both readings already agree, so
	// the divergence only shows once that is gone.
	const contractDoor = `["supplier.read","dashboard.read","contract.read","contract.amount.read"]`
	grantOnly(t, w, contractDoor)

	detailTotal := 0.0
	for _, id := range []string{w.myContract, second} {
		amount, ok := contractDetailAmount(t, w, id)
		if !ok {
			t.Fatalf("GET /api/v1/contracts/%s withheld amount from a role holding contract.amount.read, so the premise of this test is gone", id)
		}
		detailTotal += amount
	}
	if detailTotal != total {
		t.Fatalf("the contract details add up to %v, want %v — the seed is wrong, not the dashboard", detailTotal, total)
	}

	kpis := dashboardKPIs(t, w, contractDoor)
	if kpis.ActiveContractValue == 0 {
		t.Errorf("%s: GET /api/v1/contracts/<id> answered %v across the active contracts, but GET /api/v1/dashboard answered kpis.activeContractValue 0 in the same session: a zero reads as a company with no contract value, not as a figure withheld, and the card carrying it links to the list that just answered the real amounts",
			contractDoor, detailTotal)
	}
	if kpis.ActiveContractValue != total {
		t.Errorf("%s: kpis.activeContractValue %v, want %v (the active and the approved contract)", contractDoor, kpis.ActiveContractValue, total)
	}
	// The two flags stay two. contract.amount.read is business_objects.amount and
	// says nothing about suppliers.annual_spend, which has its own door in
	// canReadSupplierSpend — collapsing them back into one shared flag is how
	// they came to be gated together in the first place.
	if kpis.AnnualSpend != 0 {
		t.Errorf("%s: kpis.annualSpend %v for a role holding only the contract-amount door, want 0 — that column answers canReadSupplierSpend", contractDoor, kpis.AnnualSpend)
	}
}

// Every wording that opens business_objects.amount has to open the dashboard's
// sum of it, and a role carrying none of them has to find it shut — one
// permission set at a time, so a single wording quietly falling out is a failure
// rather than something the others cover for. annualSpend is asserted in the
// same breath because it is the *other* column behind the *other* flag: this
// table fails if the contract door starts answering for supplier spend or the
// other way round.
func TestEveryContractAmountPermissionIsReadTheSameWay(t *testing.T) {
	w := newScopeWorld(t)
	const amount, spend = 4200000.0, 7654321.0
	seedContractAmount(t, w, w.myContract, amount, "active")
	seedAnnualSpend(t, w, w.mySupplier, spend)

	for _, tc := range []struct {
		permissions                  string
		wantContractValue, wantSpend float64
	}{
		// The wording that used to be missing here, with and without the
		// contract.read that lets the same role read the detail. contract.read is
		// deliberately not required alongside: canReadSupplierSpend does not
		// require supplier.read either, and this is not the round to decide that
		// differently for one of the two.
		{`["supplier.read","dashboard.read","contract.read","contract.amount.read"]`, amount, 0},
		{`["supplier.read","dashboard.read","contract.amount.read"]`, amount, 0},
		{`["supplier.read","dashboard.read","contract.*"]`, amount, 0},
		// Already open before this change and must stay exactly as open.
		{`["supplier.read","dashboard.read","spend.read"]`, amount, spend},
		{`["supplier.read","dashboard.read","analytics.read"]`, amount, spend},
		// The wildcards the shipped roles are written with. These are here
		// because the flag used to test for "*" by name beside the two wordings,
		// and dropping that is only safe if permissionMatches really does answer
		// them — asserted through a role row and a real session, not on trust.
		{`["*"]`, amount, spend},
		{`["*.read"]`, amount, spend},
		// supplier.financial.read is the supplier-money door and names nothing
		// about this column, so it opens annual_spend and not the contract sum.
		{`["supplier.read","dashboard.read","supplier.financial.read"]`, 0, spend},
		{`["supplier.read","dashboard.read"]`, 0, 0},
		{`["supplier.read","dashboard.read","contract.read"]`, 0, 0},
	} {
		grantOnly(t, w, tc.permissions)
		kpis := dashboardKPIs(t, w, tc.permissions)
		if kpis.ActiveContractValue != tc.wantContractValue {
			t.Errorf("%s: kpis.activeContractValue %v, want %v", tc.permissions, kpis.ActiveContractValue, tc.wantContractValue)
		}
		if kpis.AnnualSpend != tc.wantSpend {
			t.Errorf("%s: kpis.annualSpend %v, want %v — the two columns keep separate doors", tc.permissions, kpis.AnnualSpend, tc.wantSpend)
		}
	}
}

// dashboardKPIs reads the KPI block the dashboard answers this role with. The
// permission set is passed only so a failure names which role produced it.
func dashboardKPIs(t *testing.T, w *scopeWorld, permissions string) struct {
	AnnualSpend         float64 `json:"annualSpend"`
	ActiveContractValue float64 `json:"activeContractValue"`
} {
	t.Helper()
	rec := doRequest(t, w.handler, http.MethodGet, "/api/v1/dashboard", w.deptToken)
	if rec.Code != 200 {
		t.Fatalf("%s: dashboard answered %d: %s", permissions, rec.Code, rec.Body.String())
	}
	var body struct {
		KPIs struct {
			AnnualSpend         float64 `json:"annualSpend"`
			ActiveContractValue float64 `json:"activeContractValue"`
		} `json:"kpis"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode dashboard: %v\n  body: %s", permissions, err, rec.Body.String())
	}
	return body.KPIs
}

// contractDetailAmount reads the amount the contract detail answers with, and
// whether it carried one at all — redactObject drops the key rather than
// zeroing it, which is the distinction the dashboard loses.
func contractDetailAmount(t *testing.T, w *scopeWorld, id string) (float64, bool) {
	t.Helper()
	rec := doRequest(t, w.handler, http.MethodGet, "/api/v1/contracts/"+id, w.deptToken)
	if rec.Code != 200 {
		t.Fatalf("GET /api/v1/contracts/%s answered %d: %s", id, rec.Code, rec.Body.String())
	}
	var body struct {
		Amount *float64 `json:"amount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode contract %s: %v\n  body: %s", id, err, rec.Body.String())
	}
	if body.Amount == nil {
		return 0, false
	}
	return *body.Amount, true
}

// seedContractAmount writes an amount and a status onto a contract the fixture
// inserts with its own. The status matters as much as the figure: the KPI sums
// only status IN('active','approved'), so a contract seeded with a figure and
// left in another state contributes nothing and the assertion would hold for the
// wrong reason.
func seedContractAmount(t *testing.T, w *scopeWorld, id string, amount float64, status string) {
	t.Helper()
	if _, err := w.pool.Exec(context.Background(),
		`UPDATE business_objects SET amount=$2,status=$3 WHERE id=$1`, id, amount, status); err != nil {
		t.Fatalf("seed contract amount: %v", err)
	}
}

// seedOwnContract adds a second in-scope contract so the KPI is a sum of more
// than one row. With a single contract the dashboard's total and the detail's
// amount are the same number for a reason that has nothing to do with summing.
func seedOwnContract(t *testing.T, w *scopeWorld, number string, amount float64, status string) string {
	t.Helper()
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO business_objects(object_type,number,supplier_id,organization_id,owner_id,title,status,amount)
			SELECT 'contract',$1,o.supplier_id,o.organization_id,o.owner_id,$1,$2,$3 FROM business_objects o WHERE o.id=$4
			RETURNING id`, number, status, amount, w.myContract).Scan(&id); err != nil {
		t.Fatalf("seed second contract: %v", err)
	}
	return id
}
