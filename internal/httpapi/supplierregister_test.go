package httpapi

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEverySupplierColumnTheRegisterWritesCanBeCorrected binds the two
// statements that make up a supplier master record: the one the registration
// writes and the one the edit form writes back.
//
// The register asks for values a person types, and a person typing gets one of
// them wrong eventually. Four of the columns the registration filled in were
// missing from the edit — business_number, corporate_number, trading_since and
// annual_spend — so PATCH accepted a body naming them, answered 200, and
// changed nothing. Silence, not a refusal: the caller has no way to learn that
// the correction did not happen.
//
// Reading the column lists rather than the field names is the same method the
// other sweeps use. A column added to the registration tomorrow fails here
// until somebody decides, in writing, whether it can ever be corrected.
func TestEverySupplierColumnTheRegisterWritesCanBeCorrected(t *testing.T) {
	// The columns the edit deliberately does not touch, each with the reason.
	// A column named here that the edit has since taken on, or that the
	// registration no longer writes, is a stale exception and fails below.
	writeOnce := map[string]string{
		"supplier_number": "등록이 붙이는 코드이고 어떤 폼도 사람에게 입력받지 않는다. " +
			"외부 시스템의 코드는 erp_vendor_id가 따로 들고 있고 그쪽은 고칠 수 있다",
		"created_by": "등록한 사람은 사실이라 고칠 것이 없다",
	}

	create := supplierStatementBody(t, "createSupplier")
	update := supplierStatementBody(t, "updateSupplier")

	inserted := regexp.MustCompile(`INSERT INTO suppliers\(([^)]*)\)`).FindStringSubmatch(create)
	if inserted == nil {
		t.Fatal("the registration's INSERT INTO suppliers was not found; this test has gone stale")
	}
	registers := []string{}
	for _, column := range strings.Split(inserted[1], ",") {
		registers = append(registers, strings.TrimSpace(column))
	}
	if len(registers) < 26 {
		t.Fatalf("only %d columns were read out of the registration; the pattern has gone stale", len(registers))
	}

	set := regexp.MustCompile("UPDATE suppliers SET ([^`]*)").FindStringSubmatch(update)
	if set == nil {
		t.Fatal("the edit's UPDATE suppliers was not found; this test has gone stale")
	}
	assignment := regexp.MustCompile(`^([a-z_]+)=`)
	assignments, _, _ := strings.Cut(set[1], " WHERE ")
	corrects := map[string]bool{}
	for _, fragment := range strings.Split(assignments, ",") {
		if m := assignment.FindStringSubmatch(strings.TrimSpace(fragment)); m != nil {
			corrects[m[1]] = true
		}
	}

	for _, column := range registers {
		if corrects[column] {
			if reason, excused := writeOnce[column]; excused {
				t.Errorf("%s is listed as write-once (%s) but the edit now writes it; drop the exception",
					column, reason)
			}
			continue
		}
		if _, excused := writeOnce[column]; excused {
			continue
		}
		t.Errorf("the registration writes suppliers.%s and the edit does not, so a value typed "+
			"wrong at registration can never be corrected — PATCH answers 200 and changes nothing. "+
			"Either carry it in the UPDATE or say here why it can never change", column)
	}
	registered := map[string]bool{}
	for _, column := range registers {
		registered[column] = true
	}
	for column := range writeOnce {
		if !registered[column] {
			t.Errorf("suppliers.%s is excused here but the registration no longer writes it", column)
		}
	}
}

// supplierStatementBody returns the source of one handler in this package, so a
// statement can be read where it is written rather than by grepping the file.
func supplierStatementBody(t *testing.T, function string) string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(f os.FileInfo) bool {
		return !strings.HasSuffix(f.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			source, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			base := fset.File(file.Pos()).Base()
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Body != nil && fn.Name.Name == function {
					return string(source[int(fn.Pos())-base : int(fn.End())-base])
				}
			}
		}
	}
	t.Fatalf("%s was renamed or removed; this test is no longer watching it", function)
	return ""
}

// TestASupplierRegistrationNumberCanBeCorrected walks the correction through
// the endpoint, which is where the silence was.
//
// A 사업자번호 with a wrong digit is not a cosmetic fault. It is the column the
// register is keyed on and the one the search box looks records up by, so the
// company cannot be found by the number anybody has for it, the duplicate check
// guards a number no company holds — the real company can be registered a
// second time — and the tax office gets a different answer from the register.
// Until now the only way out was to delete the supplier and register it again,
// taking every contract, order, evaluation and risk hanging off the record with
// it. The portal even tells the supplier that a change to the 사업자번호 is
// applied after internal approval; there was no box on the internal side.
func TestASupplierRegistrationNumberCanBeCorrected(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	var typoed, other string
	if err := pool.QueryRow(ctx, `INSERT INTO suppliers(supplier_number,name,business_number,corporate_number,status,risk_level,trading_since,annual_spend)
		VALUES('SUP-REGFIX-A','등록번호 정정 업체','000-00-00001','110111-0000001','active','LOW','2020-01-02',1000)
		ON CONFLICT(supplier_number) DO UPDATE SET business_number=excluded.business_number,corporate_number=excluded.corporate_number,trading_since=excluded.trading_since,annual_spend=excluded.annual_spend RETURNING id`).Scan(&typoed); err != nil {
		t.Fatalf("seed the mistyped supplier: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO suppliers(supplier_number,name,business_number,status,risk_level)
		VALUES('SUP-REGFIX-B','등록번호 정정 이웃','111-11-11111','active','LOW')
		ON CONFLICT(supplier_number) DO UPDATE SET business_number=excluded.business_number RETURNING id`).Scan(&other); err != nil {
		t.Fatalf("seed the neighbouring supplier: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs and these would silently no-op.
		_, _ = pool.Exec(ctx, `DELETE FROM suppliers WHERE supplier_number IN('SUP-REGFIX-A','SUP-REGFIX-B')`)
	})

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.231:5000"))
	patch := func(id, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, "/api/v1/suppliers/"+id, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// The correction can be wrong in the same ways the registration can, and it
	// now passes the same checks — a date the column cannot hold used to come
	// back as a cast failure with no field named, and a negative spend subtracts
	// from every rollup the supplier appears in.
	for _, tc := range []struct{ what, body, names string }{
		{"a trading-since date the column cannot hold", `{"tradingSince":"2026-13-45"}`, "거래 시작일"},
		{"a spend that subtracts", `{"annualSpend":-5000}`, "연간 거래금액"},
	} {
		w := patch(typoed, tc.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: returned %d, want 400: %s", tc.what, w.Code, w.Body.String())
			continue
		}
		code, msg := errorCodeAndMessage(t, w)
		if code != "validation_error" {
			t.Errorf("%s: answered %q, want validation_error: %s", tc.what, code, msg)
		}
		if !strings.Contains(msg, tc.names) {
			t.Errorf("%s: the rejection does not name the box to fix: %s", tc.what, msg)
		}
	}

	// The number another company is already on file under is refused as such,
	// rather than as a database error over a perfectly well-formed number.
	w := patch(typoed, `{"businessNumber":"111-11-11111"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("a number another company holds returned %d, want 409: %s", w.Code, w.Body.String())
	}
	if code, _ := errorCodeAndMessage(t, w); code != "duplicate_business_number" {
		t.Errorf("the conflict answered %q, want duplicate_business_number", code)
	}

	read := func() (number, corporate, since string, spend float64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT business_number,coalesce(corporate_number,''),coalesce(to_char(trading_since,'YYYY-MM-DD'),''),annual_spend FROM suppliers WHERE id=$1`, typoed).
			Scan(&number, &corporate, &since, &spend); err != nil {
			t.Fatalf("read the supplier: %v", err)
		}
		return
	}
	if number, corporate, since, spend := read(); number != "000-00-00001" || corporate != "110111-0000001" || since != "2020-01-02" || spend != 1000 {
		t.Errorf("a refused correction still moved the record: %q %q %q %v", number, corporate, since, spend)
	}

	// And the correction itself, which had nowhere to go before this.
	if w := patch(typoed, `{"businessNumber":"222-22-22222","corporateNumber":"110111-0000002","tradingSince":"2021-03-04","annualSpend":5000}`); w.Code != http.StatusOK {
		t.Fatalf("the registration numbers could not be corrected: %d %s", w.Code, w.Body.String())
	}
	if number, corporate, since, spend := read(); number != "222-22-22222" || corporate != "110111-0000002" || since != "2021-03-04" || spend != 5000 {
		t.Fatalf("the correction stored %q %q %q %v", number, corporate, since, spend)
	}

	// A value the caller did not send is not a cleared one: an edit of the name
	// alone leaves all four where the correction put them.
	if w := patch(typoed, `{"name":"등록번호 정정 업체 v2"}`); w.Code != http.StatusOK {
		t.Fatalf("an edit carrying no registration number returned %d: %s", w.Code, w.Body.String())
	}
	if number, corporate, since, spend := read(); number != "222-22-22222" || corporate != "110111-0000002" || since != "2021-03-04" || spend != 5000 {
		t.Errorf("an unrelated edit changed the registration numbers to %q %q %q %v", number, corporate, since, spend)
	}

	// The point of the whole thing: the search box now finds the company under
	// the number the people looking for it actually have.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/suppliers?q=222-22-22222", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	found := httptest.NewRecorder()
	handler.ServeHTTP(found, r)
	if found.Code != http.StatusOK {
		t.Fatalf("the register search returned %d: %s", found.Code, found.Body.String())
	}
	var list struct {
		Items []struct {
			ID             string `json:"id"`
			BusinessNumber string `json:"businessNumber"`
		} `json:"items"`
	}
	if err := json.Unmarshal(found.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode the register search: %v", err)
	}
	matched := false
	for _, item := range list.Items {
		if item.ID == typoed && item.BusinessNumber == "222-22-22222" {
			matched = true
		}
	}
	if !matched {
		t.Errorf("the corrected supplier is not found by its number: %s", found.Body.String())
	}
}
