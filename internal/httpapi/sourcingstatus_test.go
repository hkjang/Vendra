package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TestEverySourcingStandingIsInTheVocabulary is the supplier-status sweep run
// over the other table whose status the statements branch on.
//
// A bidder's standing in an RFQ/RFP is not a label. Declining is allowed only
// from IN('invited','draft'), asking a question only while status<>'declined',
// and the read receipt at the end of the response save must leave the standings
// the award wrote alone. A word outside the list is not a different standing
// but no standing: none of those statements sees it, and both screens that show
// one print it as stored.
//
// The award wrote one. It put the request's status onto the bidders as well, so
// a 우선협상 selection stamped the chosen company "preferred_negotiation" — a
// word this table has never held.
func TestEverySourcingStandingIsInTheVocabulary(t *testing.T) {
	known := map[string]bool{}
	for _, s := range sourcingParticipantStatuses {
		known[s] = true
	}
	// Only statements about that table, and within them only the column that
	// belongs to it: the portal's list joins the request and the bid, and both
	// of those carry a status of their own under a different alias.
	aboutParticipants := regexp.MustCompile(`sourcing_participants`)
	branches := regexp.MustCompile(`(?:^|[^.\w])(?:p\.)?status\s*(?:=|<>|!=)\s*'([a-z_]*)'`)
	sets := regexp.MustCompile(`(?:^|[^.\w])(?:p\.)?status\s+(?:NOT\s+)?IN\s*\(([^)]*)\)`)
	quoted := regexp.MustCompile(`'([a-z_]*)'`)

	checked := 0
	for _, sql := range packageSQL(t) {
		if !aboutParticipants.MatchString(sql) {
			continue
		}
		checked++
		for _, m := range branches.FindAllStringSubmatch(sql, -1) {
			if m[1] != "" && !known[m[1]] {
				t.Errorf("a statement branches on the standing %q, which sourcingParticipantStatuses "+
					"does not list, so no award can put a bidder into it: %s", m[1], sql)
			}
		}
		for _, m := range sets.FindAllStringSubmatch(sql, -1) {
			for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
				if q[1] != "" && !known[q[1]] {
					t.Errorf("a statement branches on the standing %q, which "+
						"sourcingParticipantStatuses does not list: %s", q[1], sql)
				}
			}
		}
	}
	if checked < 4 {
		t.Errorf("only %d participant statements were read; the extraction has stopped finding them", checked)
	}

	// The default the table itself applies is the standing an invitation
	// creates, and it is on the same list.
	migration := repoFile(t, "internal/db/migrations/003_sourcing_and_spend.sql")
	table := migration[strings.Index(migration, "CREATE TABLE sourcing_participants"):]
	def := regexp.MustCompile(`status text NOT NULL DEFAULT '([a-z_]*)'`).FindStringSubmatch(table)
	if def == nil {
		t.Fatal("the sourcing_participants table no longer defaults its status; the vocabulary lost its starting word")
	}
	if !known[def[1]] {
		t.Errorf("the table defaults a standing to %q, which sourcingParticipantStatuses does not list", def[1])
	}

	// And the two sets the handlers read the vocabulary through are drawn from
	// it, so neither can name a standing that cannot be stored.
	for _, standing := range []string{"preferred", "selected", "not_selected"} {
		if !known[standing] {
			t.Errorf("the award writes the standing %q, which sourcingParticipantStatuses does not list", standing)
		}
		if !sourcingStandingIsTheCommittees(standing) {
			t.Errorf("%q is written by the award but sourcingStandingIsTheCommittees does not protect it, "+
				"so the bidder's next save writes over the decision", standing)
		}
	}
	for _, standing := range sourcingParticipantStatuses {
		if sourcingBiddingClosed(standing) && !sourcingStandingIsTheCommittees(standing) {
			t.Errorf("%q closes the bidding but is not one of the committee's standings", standing)
		}
	}
}

// TestTheStandingsTheAPIWritesAreTheOnesTheScreensName holds the two surfaces to
// one vocabulary, the way the supplier statuses are held.
//
// Neither screen showing a standing had a word for any of them: the buyer's
// 참여 공급업체 list and the supplier's own portal card both printed the stored
// value, so the company that had not been chosen read "not_selected" in the
// same blue as the one that had.
func TestTheStandingsTheAPIWritesAreTheOnesTheScreensName(t *testing.T) {
	source := repoFile(t, "web/src/status.ts")
	declaration := regexp.MustCompile(`(?s)sourcingParticipantLabels[^=]*=\s*\{(.*?)\n\};`).FindStringSubmatch(source)
	if declaration == nil {
		t.Fatal("web/src/status.ts no longer declares sourcingParticipantLabels; the two surfaces can drift again")
	}
	labelled := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*([a-z_]+):`).FindAllStringSubmatch(declaration[1], -1) {
		labelled[m[1]] = true
	}
	for _, standing := range sourcingParticipantStatuses {
		if !labelled[standing] {
			t.Errorf("the API can store the standing %q and no screen has a word for it; "+
				"it is shown to the buyer and to the bidder exactly as stored", standing)
		}
	}
	for standing := range labelled {
		// 'closed' is the portal's own answer for a request past its due date,
		// not something the table holds.
		if standing == "closed" {
			continue
		}
		found := false
		for _, s := range sourcingParticipantStatuses {
			found = found || s == standing
		}
		if !found {
			t.Errorf("the screens label a standing %q that the API never writes", standing)
		}
	}
}

// TestAnAwardedBidderCannotWriteOverTheDecision walks the whole decision from
// both sides.
//
// The response save ends by stamping the participant row with the bid's own
// status, and only the due date stood in front of it — which is not the same
// date as the award. A bidder the committee had passed over could reopen the
// quote form any time before the deadline and their 미선정 became 작성 중: the
// buyer's participant list showed them in the running again, with nothing
// anywhere to say the award had been made. Submitting went further and put
// recalculateSourcing back over the comparison the decision had been taken
// from, after it was taken.
//
// 우선협상 is deliberately not that: revising the quote is what the selection is
// for, so the save is allowed and the standing survives it.
func TestAnAwardedBidderCannotWriteOverTheDecision(t *testing.T) {
	f, pool := newPortalFixture(t)
	ctx := context.Background()

	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs. These go before the fixture's own
		// cleanup removes the suppliers they point at, which is why they are
		// registered after it.
		_, _ = pool.Exec(ctx, `DELETE FROM sourcing_selections WHERE sourcing_id IN (SELECT id FROM business_objects WHERE number='RFQ-STANDING')`)
		_, _ = pool.Exec(ctx, `DELETE FROM sourcing_responses WHERE sourcing_id IN (SELECT id FROM business_objects WHERE number='RFQ-STANDING')`)
		_, _ = pool.Exec(ctx, `DELETE FROM sourcing_participants WHERE sourcing_id IN (SELECT id FROM business_objects WHERE number='RFQ-STANDING')`)
		_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE number='RFQ-STANDING'`)
	})
	_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE number='RFQ-STANDING'`)

	var adminID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&adminID); err != nil {
		t.Fatalf("read the admin: %v", err)
	}
	var rfqID string
	if err := pool.QueryRow(ctx, `INSERT INTO business_objects(object_type,number,title,status,due_date,created_by)
		VALUES('rfq','RFQ-STANDING','낙찰 기록 보존','open',current_date+30,$1) RETURNING id`, adminID).Scan(&rfqID); err != nil {
		t.Fatalf("seed the rfq: %v", err)
	}
	bid := func(supplierID string, amount int) string {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO sourcing_participants(sourcing_id,supplier_id,status) VALUES($1,$2,'submitted')`, rfqID, supplierID); err != nil {
			t.Fatalf("seed the participant: %v", err)
		}
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO sourcing_responses(sourcing_id,supplier_id,status,currency,total_amount,submitted_at)
			VALUES($1,$2,'submitted','KRW',$3,now()) RETURNING id`, rfqID, supplierID, amount).Scan(&id); err != nil {
			t.Fatalf("seed the bid: %v", err)
		}
		return id
	}
	// The portal account belongs to supplier A, so A is the bidder whose own
	// view of the decision this test can take.
	bidOfA, bidOfB := bid(f.supplierA, 3_000_000), bid(f.supplierB, 2_000_000)

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, f.handler, testAdminEmail, testAdminPassword, "198.51.100.44:5000"))
	award := func(responseID, kind string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"responseId": responseID, "selectionType": kind, "reason": "검증"})
		r := httptest.NewRequest(http.MethodPost, "/api/v1/sourcing/"+rfqID+"/select", strings.NewReader(string(body)))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	saveResponse := func() *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"currency": "KRW", "totalAmount": 2_500_000})
		r := httptest.NewRequest(http.MethodPut, "/api/v1/portal/sourcing/"+rfqID+"/response", strings.NewReader(string(body)))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	standing := func(supplierID string) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, `SELECT status FROM sourcing_participants WHERE sourcing_id=$1 AND supplier_id=$2`, rfqID, supplierID).Scan(&s); err != nil {
			t.Fatalf("read the standing: %v", err)
		}
		return s
	}

	// 우선협상. The chosen bidder is marked in the participants' own vocabulary
	// — not "preferred_negotiation", which is the request's status and was what
	// this used to write — and the others are left where they were.
	if w := award(bidOfA, "preferred"); w.Code != http.StatusOK {
		t.Fatalf("the preferred selection returned %d: %s", w.Code, w.Body.String())
	}
	if got := standing(f.supplierA); got != "preferred" {
		t.Errorf("the preferred bidder stands as %q", got)
	}
	if got := standing(f.supplierB); got != "submitted" {
		t.Errorf("a preferred selection moved the other bidder to %q", got)
	}

	// Negotiating is the point of it, so the save goes through — and the
	// standing outlives the read receipt that follows it.
	if w := saveResponse(); w.Code != http.StatusOK {
		t.Fatalf("the preferred bidder could not revise the quote: %d %s", w.Code, w.Body.String())
	}
	if got := standing(f.supplierA); got != "preferred" {
		t.Errorf("revising the quote wrote the preferred bidder's standing back to %q", got)
	}

	// The final award. Now the other bidder wins and A is not chosen.
	if w := award(bidOfB, "final"); w.Code != http.StatusOK {
		t.Fatalf("the final award returned %d: %s", w.Code, w.Body.String())
	}
	if got := standing(f.supplierB); got != "selected" {
		t.Errorf("the winner stands as %q", got)
	}
	if got := standing(f.supplierA); got != "not_selected" {
		t.Errorf("the bidder that was passed over stands as %q", got)
	}

	// This is the save that used to erase it.
	w := saveResponse()
	if w.Code != http.StatusConflict {
		t.Fatalf("a bidder that was not selected saved a quote anyway with %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "sourcing_awarded") {
		t.Errorf("the refusal does not name the award: %s", w.Body.String())
	}
	if got := standing(f.supplierA); got != "not_selected" {
		t.Errorf("the refused save still moved the standing to %q", got)
	}

	// And the bidder is told. The portal reported 'closed' over any standing
	// once the due date had passed, so the one thing the list exists to say
	// went missing on the day the request shut.
	if _, err := pool.Exec(ctx, `UPDATE business_objects SET due_date=current_date-1 WHERE id=$1`, rfqID); err != nil {
		t.Fatalf("move the due date: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/portal/sourcing", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
	list := httptest.NewRecorder()
	f.handler.ServeHTTP(list, r)
	if list.Code != http.StatusOK {
		t.Fatalf("the portal list returned %d: %s", list.Code, list.Body.String())
	}
	var body struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the portal list: %v", err)
	}
	found := false
	for _, item := range body.Items {
		if item.ID != rfqID {
			continue
		}
		found = true
		if item.Status != "not_selected" {
			t.Errorf("the portal reports the awarded request as %q", item.Status)
		}
	}
	if !found {
		t.Errorf("the portal list does not carry the request at all: %s", list.Body.String())
	}
}
