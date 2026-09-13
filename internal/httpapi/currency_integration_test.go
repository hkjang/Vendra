package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestABidIsPricedInTheCurrencyTheTenderWasPutOutIn walks the door the wrong
// currency actually came through.
//
// The portal's quote form offered KRW, USD, EUR and JPY, and nothing downstream
// had ever read the answer. recalculateSourcing scores price as
// 100*min_amount/total_amount across every submitted bid, so a quote of 50,000
// USD standing beside quotes of 68,000,000 KRW is not read as a bid in another
// currency — it is read as the smallest number, takes the whole of the price
// weight, and sorts to the top of the committee's table, which renders it as
// ₩50,000 beside the rest. There is no rate table anywhere in the application
// to bring the two onto one scale.
func TestABidIsPricedInTheCurrencyTheTenderWasPutOutIn(t *testing.T) {
	f, pool := newPortalFixture(t)
	ctx := context.Background()

	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs. These go before the fixture's own
		// cleanup removes the supplier they point at, which is why they are
		// registered after it.
		_, _ = pool.Exec(ctx, `DELETE FROM sourcing_responses WHERE sourcing_id IN (SELECT id FROM business_objects WHERE number='RFQ-CURRENCY')`)
		_, _ = pool.Exec(ctx, `DELETE FROM sourcing_participants WHERE sourcing_id IN (SELECT id FROM business_objects WHERE number='RFQ-CURRENCY')`)
		_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE number='RFQ-CURRENCY'`)
	})
	_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE number='RFQ-CURRENCY'`)

	var adminID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&adminID); err != nil {
		t.Fatalf("read the admin: %v", err)
	}
	// A tender put out in dollars, which is the case the mistake is invisible
	// in: every screen used to render every amount as won.
	var rfqID string
	if err := pool.QueryRow(ctx, `INSERT INTO business_objects(object_type,number,title,status,currency,due_date,created_by)
		VALUES('rfq','RFQ-CURRENCY','통화 기준 검증','open','USD',current_date+30,$1) RETURNING id`, adminID).Scan(&rfqID); err != nil {
		t.Fatalf("seed the rfq: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sourcing_participants(sourcing_id,supplier_id,status) VALUES($1,$2,'invited')`, rfqID, f.supplierA); err != nil {
		t.Fatalf("seed the participant: %v", err)
	}

	quote := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		payload, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPut, "/api/v1/portal/sourcing/"+rfqID+"/response", strings.NewReader(string(payload)))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	storedCurrency := func() string {
		t.Helper()
		var code string
		if err := pool.QueryRow(ctx, `SELECT currency FROM sourcing_responses WHERE sourcing_id=$1 AND supplier_id=$2`, rfqID, f.supplierA).Scan(&code); err != nil {
			t.Fatalf("read the bid's currency: %v", err)
		}
		return code
	}

	// The quote the old form let a supplier send: a number of won against a
	// tender in dollars. It is refused where it is written, and the refusal
	// says which currency the request is in.
	w := quote(map[string]any{"submit": true, "currency": "KRW", "totalAmount": 50_000_000})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a bid in another currency was taken with %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "currency_mismatch") || !strings.Contains(body, "USD") {
		t.Errorf("the refusal does not say what the tender is priced in: %s", body)
	}
	if err := pool.QueryRow(ctx, `SELECT currency FROM sourcing_responses WHERE sourcing_id=$1 AND supplier_id=$2`, rfqID, f.supplierA).Scan(new(string)); err == nil {
		t.Error("the refused bid was stored anyway")
	}

	// A code nobody prices anything in is not a currency at all.
	if w := quote(map[string]any{"currency": "달러", "totalAmount": 50_000}); w.Code != http.StatusBadRequest {
		t.Errorf("a word was taken as a currency code with %d: %s", w.Code, w.Body.String())
	}

	// Saying nothing means the tender's currency, which is the only reading
	// that leaves the comparison meaning anything.
	if w := quote(map[string]any{"totalAmount": 50_000}); w.Code != http.StatusOK {
		t.Fatalf("a quote with no currency was refused: %d %s", w.Code, w.Body.String())
	}
	if got := storedCurrency(); got != "USD" {
		t.Errorf("the bid was stored in %q, not the tender's currency", got)
	}

	// Case is not part of the answer.
	if w := quote(map[string]any{"submit": true, "currency": "usd", "totalAmount": 48_000}); w.Code != http.StatusOK {
		t.Fatalf("a lower-case code was refused: %d %s", w.Code, w.Body.String())
	}
	if got := storedCurrency(); got != "USD" {
		t.Errorf("the bid was stored in %q after a lower-case code", got)
	}

	// And the bidder is told what to price the quote in, rather than being
	// offered four currencies and corrected afterwards. The list the portal's
	// quote form is drawn from carried the due date and not the currency.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/portal/sourcing", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
	list := httptest.NewRecorder()
	f.handler.ServeHTTP(list, r)
	if list.Code != http.StatusOK {
		t.Fatalf("the tender list returned %d: %s", list.Code, list.Body.String())
	}
	var answer struct {
		Items []struct {
			ID       string `json:"id"`
			Currency string `json:"currency"`
			Response *struct {
				Currency string `json:"currency"`
			} `json:"response"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the tender list: %v", err)
	}
	found := false
	for _, item := range answer.Items {
		if item.ID != rfqID {
			continue
		}
		found = true
		if item.Currency != "USD" {
			t.Errorf("the tender is reported as priced in %q", item.Currency)
		}
		if item.Response == nil || item.Response.Currency != "USD" {
			t.Errorf("the bidder's own quote is reported as %+v", item.Response)
		}
	}
	if !found {
		t.Error("the tender the supplier was invited to is missing from the portal list")
	}
}

// TestACurrencyOnABusinessObjectIsOneAndCanBeCorrected walks the other door:
// the buyer's own record, where the code is written and — until now — could
// never be rewritten.
func TestACurrencyOnABusinessObjectIsOneAndCanBeCorrected(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM business_objects WHERE title='통화 정정 검증'`)
	})
	_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE title='통화 정정 검증'`)
	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	token := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "198.51.100.77:5000"))

	call := func(method, path string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		payload, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(payload)))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// The symbol somebody types instead of the code. Stored, it is not another
	// currency but no currency: the amount has no unit and no screen can say
	// what the number means.
	w := call(http.MethodPost, "/api/v1/rfq", map[string]any{"title": "통화 정정 검증", "currency": "₩", "amount": 1000})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a symbol was taken as a currency code with %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "통화") {
		t.Errorf("the refusal does not name the box to fix: %s", body)
	}

	w = call(http.MethodPost, "/api/v1/rfq", map[string]any{"title": "통화 정정 검증", "currency": "usd", "amount": 1000})
	if w.Code != http.StatusCreated {
		t.Fatalf("a valid tender was refused: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode the created object: %v", err)
	}
	if created.Currency != "USD" {
		t.Errorf("the tender was stored as %q, which is not the one shape the column holds", created.Currency)
	}

	// The correction. Nothing wrote this column after the create, so a record
	// entered under the wrong currency answered 200 and kept the first answer.
	if w := call(http.MethodPatch, "/api/v1/rfq/"+created.ID, map[string]any{"currency": "달러"}); w.Code != http.StatusBadRequest {
		t.Errorf("a word was taken as a correction with %d: %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPatch, "/api/v1/rfq/"+created.ID, map[string]any{"currency": "KRW"}); w.Code != http.StatusOK {
		t.Fatalf("the correction was refused: %d %s", w.Code, w.Body.String())
	}
	var code string
	if err := pool.QueryRow(ctx, `SELECT currency FROM business_objects WHERE id=$1`, created.ID).Scan(&code); err != nil {
		t.Fatalf("read the corrected currency: %v", err)
	}
	if code != "KRW" {
		t.Errorf("the currency is still %q after the correction", code)
	}
	// An edit that says nothing about the currency leaves it where it is.
	if w := call(http.MethodPatch, "/api/v1/rfq/"+created.ID, map[string]any{"title": "통화 정정 검증"}); w.Code != http.StatusOK {
		t.Fatalf("an unrelated edit was refused: %d %s", w.Code, w.Body.String())
	}
	if err := pool.QueryRow(ctx, `SELECT currency FROM business_objects WHERE id=$1`, created.ID).Scan(&code); err != nil {
		t.Fatalf("read the currency again: %v", err)
	}
	if code != "KRW" {
		t.Errorf("an unrelated edit moved the currency to %q", code)
	}
}
