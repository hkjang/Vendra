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
	"strconv"
	"strings"
	"testing"

	"github.com/hkjang/Vendra/internal/security"
)

// TestEveryAccountCreatedFromARequestNamesTheAddressAlreadyTaken binds every
// statement that creates an account from something a person sent.
//
// users.email is UNIQUE, and it is also the only thing an account is signed in
// with, so "that address already has an account" is a routine outcome and the
// one outcome the sender cannot fix by editing the form: the answer is to sign
// in, not to try again. Reported as a save failure it reads as a fault in what
// was typed. In the supplier portal there is no fault to find — the address
// comes off the invitation, not off the form — so the invited person retypes
// the same four boxes forever and never gets a different answer.
//
// Finding the statements rather than listing the handlers is the same method
// the other sweeps use: a fifth door onto users.email fails here until somebody
// decides, in writing, what it says when the address is taken.
func TestEveryAccountCreatedFromARequestNamesTheAddressAlreadyTaken(t *testing.T) {
	// Functions that write the column without a caller to answer, each with the
	// reason. A name here whose statement has gone away is a stale exception and
	// fails below.
	silent := map[string]string{
		"bootstrapAdmin": "설정에서 읽은 계정을 기동 시점에 만들고, ON CONFLICT(email) DO UPDATE라 이 키는 위반될 수 없다. 요청이 없으니 답할 상대도 없다",
		"oidcCallback":   "ON CONFLICT(email) DO UPDATE라 이 키는 위반될 수 없다 — 같은 주소면 기존 계정에 연결된다",
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(f os.FileInfo) bool {
		return !strings.HasSuffix(f.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	creates := map[string]string{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			source, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			base := fset.File(file.Pos()).Base()
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				body := string(source[int(fn.Pos())-base : int(fn.End())-base])
				if strings.Contains(body, "INSERT INTO users(") {
					creates[fn.Name.Name] = body
				}
			}
		}
	}
	if len(creates) < 4 {
		t.Fatalf("only %d statements were found writing users.email; the pattern has gone stale", len(creates))
	}
	for name, body := range creates {
		reason, excused := silent[name]
		if strings.Contains(body, "duplicateUserEmail(") {
			if excused {
				t.Errorf("%s is listed as having nobody to answer (%s) but it now reads duplicateUserEmail; drop the exception", name, reason)
			}
			continue
		}
		if excused {
			continue
		}
		t.Errorf("%s creates an account from a request and does not read duplicateUserEmail, so an "+
			"address that already has one comes back as a save failure — the sender is told to fix "+
			"a form that has nothing wrong with it. Either name the clash or say here why it cannot happen", name)
	}
	for name := range silent {
		if _, found := creates[name]; !found {
			t.Errorf("%s is excused here but no longer writes users.email", name)
		}
	}
}

// TestSelfRegistrationSaysWhichOfTheTwoIsAlreadyOnFile walks the invitation
// through the endpoint, which is where the one message was.
//
// The signup transaction can hit two different unique keys — the company's
// 사업자번호 and the account's email — and both used to end at the same
// sentence, "가입을 완료하지 못했습니다". Neither one can be escaped by trying
// again, and the form offers no box that would change the outcome, so the only
// move the sentence leaves is to mistype the 사업자번호 until it is a number
// nobody holds: the company then sits in the register twice, the second time
// under a number that belongs to no one.
func TestSelfRegistrationSaysWhichOfTheTwoIsAlreadyOnFile(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs and these would silently no-op.
		// audit_logs first — it points at the accounts by actor_id, and the
		// registration writes one for each account it creates.
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_email LIKE 'selfreg-%@vendra.test'
			OR actor_id IN(SELECT id FROM users WHERE email LIKE 'selfreg-%@vendra.test')`)
		_, _ = pool.Exec(ctx, `DELETE FROM email_verifications WHERE email LIKE 'selfreg-%@vendra.test'`)
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id IN(SELECT id FROM users WHERE email LIKE 'selfreg-%@vendra.test')`)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email LIKE 'selfreg-%@vendra.test'`)
		_, _ = pool.Exec(ctx, `DELETE FROM invitations WHERE email LIKE 'selfreg-%@vendra.test'`)
		_, _ = pool.Exec(ctx, `DELETE FROM suppliers WHERE business_number IN('777-77-77777','777-77-77778')`)
	})
	// A clean slate: the same addresses and numbers from an earlier run would
	// answer 409 on the very first registration.
	_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_email LIKE 'selfreg-%@vendra.test'`)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email LIKE 'selfreg-%@vendra.test'`)
	_, _ = pool.Exec(ctx, `DELETE FROM invitations WHERE email LIKE 'selfreg-%@vendra.test'`)
	_, _ = pool.Exec(ctx, `DELETE FROM suppliers WHERE business_number IN('777-77-77777','777-77-77778')`)

	// invite writes the row the buyer's invitation endpoint writes and hands
	// back the raw token, which is the only thing the registrant ever holds.
	issued := 0
	invite := func(email, supplierID string) string {
		t.Helper()
		issued++
		token := "selfreg-token-" + strconv.Itoa(issued) + "-" + email
		var id any
		if supplierID != "" {
			id = supplierID
		}
		if _, err := pool.Exec(ctx, `INSERT INTO invitations(email,supplier_id,token_hash,expires_at) VALUES(lower($1),$2,$3,now()+interval '7 days')`,
			email, id, security.TokenHash(token)); err != nil {
			t.Fatalf("seed the invitation for %s: %v", email, err)
		}
		return token
	}
	register := func(token, displayName, supplierName, businessNumber string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{
			"token": token, "displayName": displayName, "password": "SelfRegister!2026",
			"supplierName": supplierName, "businessNumber": businessNumber,
		})
		r := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	first := invite("selfreg-first@vendra.test", "")
	w := register(first, "첫 담당자", "자가등록 주식회사", "777-77-77777")
	if w.Code != http.StatusCreated {
		t.Fatalf("the first registration returned %d, want 201: %s", w.Code, w.Body.String())
	}

	// A second person at the same company, invited without the supplier bound.
	// The company is on file, and saying so is the whole difference between a
	// person who asks for the right invitation and a person who edits the one
	// number that must not be edited until the form lets them through.
	colleague := invite("selfreg-colleague@vendra.test", "")
	w = register(colleague, "같은 회사 담당자", "자가등록 주식회사", "777-77-77777")
	if w.Code != http.StatusConflict {
		t.Fatalf("a company already on file returned %d, want 409: %s", w.Code, w.Body.String())
	}
	code, msg := errorCodeAndMessage(t, w)
	if code != "duplicate_business_number" {
		t.Errorf("a company already on file answered %q, want duplicate_business_number: %s", code, msg)
	}
	if !strings.Contains(msg, "사업자번호") {
		t.Errorf("the refusal does not name which of the two is taken: %s", msg)
	}
	// Refused, not consumed: the invitation has to survive so the buyer's
	// re-issued one is not the registrant's second problem.
	var accepted *string
	if err := pool.QueryRow(ctx, `SELECT accepted_at::text FROM invitations WHERE email='selfreg-colleague@vendra.test'`).Scan(&accepted); err != nil {
		t.Fatalf("read the refused invitation: %v", err)
	}
	if accepted != nil {
		t.Error("the refused registration consumed the invitation, so the retry has nothing to retry with")
	}

	// The way in is an invitation bound to the company, which only the buyer can
	// issue — the registration never joins a stranger to an existing record.
	var supplierID string
	if err := pool.QueryRow(ctx, `SELECT id FROM suppliers WHERE business_number='777-77-77777'`).Scan(&supplierID); err != nil {
		t.Fatalf("read the registered supplier: %v", err)
	}
	bound := invite("selfreg-bound@vendra.test", supplierID)
	if w = register(bound, "초대받은 담당자", "", ""); w.Code != http.StatusCreated {
		t.Fatalf("an invitation bound to the company returned %d, want 201: %s", w.Code, w.Body.String())
	}

	// The other key: this address already signed up. Nothing on the form is
	// wrong — the address is carried by the invitation — so the only useful
	// answer is that the account exists.
	again := invite("selfreg-first@vendra.test", "")
	w = register(again, "첫 담당자", "다른 회사", "777-77-77778")
	if w.Code != http.StatusConflict {
		t.Fatalf("an address that already has an account returned %d, want 409: %s", w.Code, w.Body.String())
	}
	code, msg = errorCodeAndMessage(t, w)
	if code != "email_registered" {
		t.Errorf("an address that already has an account answered %q, want email_registered: %s", code, msg)
	}
	if !strings.Contains(msg, "로그인") {
		t.Errorf("the refusal does not say what to do instead of retrying the form: %s", msg)
	}
	// And the company on that refused attempt was rolled back with it, rather
	// than left in the register with nobody attached to it.
	var orphans int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM suppliers WHERE business_number='777-77-77778'`).Scan(&orphans); err != nil {
		t.Fatalf("count the rolled-back company: %v", err)
	}
	if orphans != 0 {
		t.Error("the refused registration left its company in the register with no account on it")
	}
}
