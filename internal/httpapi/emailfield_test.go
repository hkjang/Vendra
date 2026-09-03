package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// notFromTheCaller lists the statements that write an email column out of
// something other than a request body, with where the address came from
// instead. Everything else has to be checked, and this list is what keeps the
// sweep below honest instead of merely quiet.
var notFromTheCaller = map[string]string{
	// The deployment's own configuration, read once at start-up. There is no
	// caller to answer and no response to write one into.
	"bootstrapAdmin": "VENDRA_BOOTSTRAP_ADMIN_EMAIL",
	// What was typed at the sign-in form, kept only as the key the lockout
	// counts failures under. It never becomes an identity or a destination, and
	// refusing to record an attempt because its address was malformed would
	// hand the lockout a way to be walked around.
	"recordLoginAttempt": "로그인 시도 기록",
	// The provider's claim, already trimmed and lower-cased where it is read,
	// and used only when the provider says it is verified.
	"oidcCallback": "OIDC 공급자의 email 클레임",
	// The address off the contact row, which was checked when it was written.
	"portalRequestContactVerification": "담당자 레코드",
	// The address off the invitation, which createInvitation checked before the
	// link was ever handed out. Registration copies it; it does not accept one.
	"registerSupplierUser": "초대장",
}

// TestEveryStoredEmailIsAnAddress keeps this sweep from having to be redone,
// the way TestEveryDateCastIsGuarded, TestEveryRequestNumberIsBounded,
// TestEveryRequestLabelIsBounded, TestEveryRiskGradeIsInTheVocabulary and
// TestEveryRequestRecordIDIsChecked keep the ones before it.
//
// Same method: an operation rather than a spelling. The operation is a value
// taken from the request and written into an email column — so the statement's
// own column list is what this reads, not the name the request happens to give
// the field.
//
// Nothing checked either half. The shape went unexamined, so "김구매" — the
// name, out of the box above — saved on every one of these surfaces, and the
// form's type="email" is no help to the portal, an API client or a paste. And
// the form was never normalised: the inserts lower-case but do not trim, while
// login selects `WHERE email=$1` on a trimmed value, so an address pasted with
// a trailing space becomes an account that no password opens and whose only
// symptom is 자격 증명이 올바르지 않습니다.
func TestEveryStoredEmailIsAnAddress(t *testing.T) {
	insert := regexp.MustCompile(`INSERT INTO [a-z_]+\(([^)]*)\)`)
	// The assignments of an UPDATE, up to its WHERE — so that `WHERE email=$1`,
	// which reads an address rather than storing one, is not mistaken for a
	// write. `actor_email=` is not one either: that is a byline, not a
	// destination.
	assignments := regexp.MustCompile(`SET ([^` + "`" + `]*)`)
	// validEmail, validEmailFields, and validSupplierEmails, which is how the
	// two supplier doors spell the pair they share.
	validates := regexp.MustCompile(`valid[A-Za-z]*Email[A-Za-z]*\(`)

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(f os.FileInfo) bool {
		return !strings.HasSuffix(f.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}

	seen := map[string]bool{}
	writers := 0
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
				body := string(source)[int(fn.Pos())-base : int(fn.End())-base]
				stores := false
				for _, m := range insert.FindAllStringSubmatch(body, -1) {
					for _, column := range strings.Split(m[1], ",") {
						if strings.TrimSpace(column) == "email" {
							stores = true
						}
					}
				}
				for _, m := range assignments.FindAllStringSubmatch(body, -1) {
					set, _, _ := strings.Cut(m[1], " WHERE ")
					for _, column := range strings.Split(set, ",") {
						if strings.HasPrefix(strings.TrimSpace(column), "email=") {
							stores = true
						}
					}
				}
				if !stores {
					continue
				}
				writers++
				if _, exempt := notFromTheCaller[fn.Name.Name]; exempt {
					seen[fn.Name.Name] = true
					continue
				}
				if !validates.MatchString(body) {
					t.Errorf("%s: %s writes an email column without checking the address is "+
						"one. It is a destination and, on users.email, an identity: an "+
						"address that is not one saves quietly and is found weeks later, as "+
						"mail that never arrived or an account that cannot sign in. Check it "+
						"with validEmailFields, or say where the address comes from in "+
						"notFromTheCaller", name, fn.Name.Name)
				}
			}
		}
	}
	// The sweep is only worth anything while it still finds the statements.
	if writers < 10 {
		t.Errorf("only %d statements writing an email column were found; the patterns this "+
			"test matches on have gone stale", writers)
	}
	for name := range notFromTheCaller {
		if !seen[name] {
			t.Errorf("%s was renamed or no longer writes an email column; this test is no "+
				"longer watching it", name)
		}
	}
}

// TestARejectedEmailNamesTheBox holds the rejection to naming the field, and
// holds the accepted value to the form the lookups use.
func TestARejectedEmailNamesTheBox(t *testing.T) {
	in := map[string]any{"email": "김구매"}
	w := httptest.NewRecorder()
	if validEmailFields(w, in, emailField{"email", "담당자 이메일"}) {
		t.Fatal("a name was accepted where an address belongs")
	}
	if body := w.Body.String(); !strings.Contains(body, "담당자 이메일") {
		t.Errorf("the rejection does not name the box to fix: %s", body)
	}

	// An address the caller did not send keeps whatever default the statement
	// applies, the same way the date, number, label and id checks beside it
	// leave an absent field alone.
	if w := httptest.NewRecorder(); !validEmailFields(w, map[string]any{}, emailField{"email", "이메일"}) {
		t.Errorf("an unsent address was refused: %s", w.Body.String())
	}

	// What is stored is what login looks up: the pasted spaces and the capitals
	// off a business card are the caller's, not a different address.
	pasted := map[string]any{"email": "  Gu.Kim+RFQ@Acme.CO.KR "}
	if w := httptest.NewRecorder(); !validEmailFields(w, pasted, emailField{"email", "이메일"}) {
		t.Fatalf("a pasted address was refused: %s", w.Body.String())
	}
	if pasted["email"] != "gu.kim+rfq@acme.co.kr" {
		t.Errorf("the stored address is %q, which login would not find", pasted["email"])
	}

	for _, wrong := range []string{
		// The name from the box above, in both scripts.
		"김구매", "Kim Gu",
		// A whole line off a spreadsheet, and the one from a mail client.
		"김구매 <gu@acme.co.kr>", "gu@acme.co.kr, jy@acme.co.kr",
		// The domain alone, the local part alone, and the machine.
		"@acme.co.kr", "gu@", "gu@localhost", "gu@사내메일",
		// Two addresses run together, and a template nobody filled in.
		"gu@acme@co.kr", "${contactEmail}",
		// Shapes the mail system itself would refuse.
		"gu..kim@acme.co.kr", ".gu@acme.co.kr", "gu@acme..co.kr", "gu@-acme.co.kr",
		strings.Repeat("g", 250) + "@acme.co.kr",
	} {
		w := httptest.NewRecorder()
		if _, ok := validEmail(w, wrong, "이메일"); ok {
			t.Errorf("%q was accepted as an address", wrong)
		}
	}

	// The addresses a supplier register actually holds, none of which this may
	// start refusing.
	for _, right := range []string{
		"gu@acme.co.kr", "gu.kim@acme.co.kr", "purchasing+rfq@acme-tooling.com",
		"g_kim@sub.acme.co.kr", "007@a.io", "gu-kim@acme.kr",
	} {
		w := httptest.NewRecorder()
		if _, ok := validEmail(w, right, "이메일"); !ok {
			t.Errorf("%q was refused: %s", right, w.Body.String())
		}
	}
}
