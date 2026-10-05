package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// objectOrderBy drops order=amount_desc back to updated_at for a caller without
// <type>.amount.read, which is the right call — sorting by a column the caller
// may not read leaks its ranking. What was wrong is that the response never said
// so. The web list offers 「금액 높은순」 and paints the dropdown from the URL
// parameter (web/src/pages/Objects.tsx), so the control kept reading
// "amount_desc" while the rows arrived newest-first and the amount column sat
// empty — and a reader takes the top of that list for the largest contract.
//
// This reads the applied `order` field and the actual row order out of the same
// response, for two roles differing only in contract.amount.read. Either half on
// its own proves nothing: the field could name a sort the rows do not follow,
// and the rows alone cannot tell the caller which sort produced them.
func TestContractListSaysWhichOrderItApplied(t *testing.T) {
	w := newScopeWorld(t)
	// Amount order and recency order are seeded to be exact opposites. The
	// fixture inserts every business_object with amount=99000000 and a default
	// now(), so without this the two sorts agree by accident and the assertion
	// below would hold whichever one ran.
	seedOrderedContract(t, w, "SC-ORD-BIG", 300000000, "3 days")
	seedOrderedContract(t, w, "SC-ORD-MID", 200000000, "2 days")
	seedOrderedContract(t, w, "SC-ORD-SMALL", 100000000, "1 day")
	// The fixture's own contract would otherwise sit in the middle of both
	// orders with an amount of its own; pinning it oldest and smallest keeps the
	// two expected sequences short and total.
	seedOrderedContract(t, w, "SC-MY-CT", 1, "4 days")

	byAmount := []string{"SC-ORD-BIG", "SC-ORD-MID", "SC-ORD-SMALL", "SC-MY-CT"}
	byRecency := []string{"SC-ORD-SMALL", "SC-ORD-MID", "SC-ORD-BIG", "SC-MY-CT"}

	for _, tc := range []struct {
		permissions string
		wantOrder   string
		wantNumbers []string
	}{
		// Holding the amount door: the requested sort is the applied sort.
		{`["contract.read","contract.amount.read"]`, "amount_desc", byAmount},
		// Without it the rows come back newest-first, and that is what the
		// response has to say — the same query string, the same session shape,
		// a different answer because the role is different.
		{`["contract.read"]`, "updated_desc", byRecency},
	} {
		grantOnly(t, w, tc.permissions)
		order, numbers := contractList(t, w, "?order=amount_desc", tc.permissions)
		if order != tc.wantOrder {
			t.Errorf("%s: GET /api/v1/contracts?order=amount_desc answered order %q, want %q — the caller asked for amount_desc and has no other way to learn which sort ran",
				tc.permissions, order, tc.wantOrder)
		}
		if !sameOrder(numbers, tc.wantNumbers) {
			t.Errorf("%s: rows arrived %v, want %v", tc.permissions, numbers, tc.wantNumbers)
		}
	}
}

// An unknown or absent order already falls through to updated_at. The response
// has to name that too, and the request has to keep succeeding: the web list
// reflects whatever sits in the URL straight back into the query string, so
// refusing an unrecognised value would empty the screen instead of correcting it.
func TestContractListNamesTheFallbackOrder(t *testing.T) {
	w := newScopeWorld(t)
	grantOnly(t, w, `["contract.read","contract.amount.read"]`)
	for _, query := range []string{"?order=banana", "?order=", "", "?order=due_asc", "?order=title_asc"} {
		want := "updated_desc"
		switch query {
		case "?order=due_asc":
			want = "due_asc"
		case "?order=title_asc":
			want = "title_asc"
		}
		order, numbers := contractList(t, w, query, query)
		if order != want {
			t.Errorf("GET /api/v1/contracts%s answered order %q, want %q", query, order, want)
		}
		if len(numbers) == 0 {
			t.Errorf("GET /api/v1/contracts%s answered no rows, so the order field above describes nothing", query)
		}
	}
}

// contractList reads the applied order and the row numbers, in the order the
// response carried them, out of one GET. The label is only used to name the
// caller in a failure.
func contractList(t *testing.T, w *scopeWorld, query, label string) (string, []string) {
	t.Helper()
	rec := doRequest(t, w.handler, http.MethodGet, "/api/v1/contracts"+query, w.deptToken)
	if rec.Code != 200 {
		t.Fatalf("%s: GET /api/v1/contracts%s answered %d: %s", label, query, rec.Code, rec.Body.String())
	}
	var body struct {
		Order string `json:"order"`
		Items []struct {
			Number string `json:"number"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode contract list: %v\n  body: %s", label, err, rec.Body.String())
	}
	if body.Order == "" {
		t.Fatalf("%s: GET /api/v1/contracts%s answered no order field, so a caller whose sort was dropped has no way to know: %s",
			label, query, rec.Body.String())
	}
	numbers := make([]string, 0, len(body.Items))
	for _, item := range body.Items {
		numbers = append(numbers, item.Number)
	}
	return body.Order, numbers
}

func sameOrder(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// seedOrderedContract writes a contract into the caller's own scope with both an
// explicit amount and an explicit updated_at, upserting by number so the
// fixture's own contract can be pinned the same way. Both columns have to be
// stated: the amount is what one sort reads and updated_at is what the other
// falls back to, and a test that leaves either at the fixture's value cannot
// tell the two sorts apart.
func seedOrderedContract(t *testing.T, w *scopeWorld, number string, amount float64, age string) {
	t.Helper()
	ctx := context.Background()
	tag, err := w.pool.Exec(ctx, `UPDATE business_objects SET amount=$2, updated_at=now()-$3::interval, status='active'
		WHERE number=$1 AND object_type='contract'`, number, amount, age)
	if err != nil {
		t.Fatalf("pin contract %s: %v", number, err)
	}
	if tag.RowsAffected() > 0 {
		return
	}
	if _, err := w.pool.Exec(ctx,
		`INSERT INTO business_objects(object_type,number,supplier_id,organization_id,owner_id,title,status,amount,updated_at)
			SELECT 'contract',$1,o.supplier_id,o.organization_id,o.owner_id,$1,'active',$2,now()-$3::interval
			FROM business_objects o WHERE o.id=$4`, number, amount, age, w.myContract); err != nil {
		t.Fatalf("seed contract %s: %v", number, err)
	}
}
