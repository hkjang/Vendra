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

// TestEveryStoredContactDetailIsOne keeps this sweep from having to be redone,
// the way TestEveryDateCastIsGuarded, TestEveryRequestNumberIsBounded,
// TestEveryRequestLabelIsBounded, TestEveryRiskGradeIsInTheVocabulary,
// TestEveryRequestRecordIDIsChecked and TestEveryStoredEmailIsAnAddress keep
// the ones before it.
//
// Same method: an operation rather than a spelling. The operation is a value
// taken from the request and written into the phone or website column — so the
// statement's own column list is what this reads, not the name the request
// happens to give the field.
//
// Neither was ever examined. Both sat in a list that measured a length, so
// "내선 3번" was a telephone number and the internal wiki's title was a web
// address, on all five doors: the register, the buyer's edit form, the portal's
// own profile, and the two that write a contact. A length is no check at all
// here — the values that get typed into these boxes by mistake are short.
func TestEveryStoredContactDetailIsOne(t *testing.T) {
	insert := regexp.MustCompile(`INSERT INTO [a-z_]+\(([^)]*)\)`)
	// The assignments of an UPDATE, up to its WHERE — so that a phone read in a
	// lookup is not mistaken for one being stored.
	assignments := regexp.MustCompile(`SET ([^` + "`" + `]*)`)
	// validPhone, validPhoneFields, validWebsite, validWebsiteFields, and
	// validSupplierContactDetails, which is how the three supplier doors spell
	// the pair they share.
	checks := map[string]*regexp.Regexp{
		"phone":   regexp.MustCompile(`valid[A-Za-z]*(Phone|ContactDetails)[A-Za-z]*\(`),
		"website": regexp.MustCompile(`valid[A-Za-z]*(Website|ContactDetails)[A-Za-z]*\(`),
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(f os.FileInfo) bool {
		return !strings.HasSuffix(f.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}

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
				for column, validates := range checks {
					stores := false
					for _, m := range insert.FindAllStringSubmatch(body, -1) {
						for _, c := range strings.Split(m[1], ",") {
							if strings.TrimSpace(c) == column {
								stores = true
							}
						}
					}
					for _, m := range assignments.FindAllStringSubmatch(body, -1) {
						set, _, _ := strings.Cut(m[1], " WHERE ")
						for _, c := range strings.Split(set, ",") {
							if strings.HasPrefix(strings.TrimSpace(c), column+"=") {
								stores = true
							}
						}
					}
					if !stores {
						continue
					}
					writers++
					if !validates.MatchString(body) {
						t.Errorf("%s: %s writes the %s column without checking the value is "+
							"one. A length is not a check here — the wrong thing typed into "+
							"these boxes is short, and it saves looking like the right thing. "+
							"Check it with validPhoneFields or validWebsiteFields",
							name, fn.Name.Name, column)
					}
				}
			}
		}
	}
	// The sweep is only worth anything while it still finds the statements: the
	// register's insert, the buyer's update, the portal's own profile update,
	// and the two contact inserts.
	if writers < 7 {
		t.Errorf("only %d statements writing a phone or website column were found; the "+
			"patterns this test matches on have gone stale", writers)
	}
}

// TestARejectedContactDetailNamesTheBox holds the rejection to naming the field
// and the accepted value to one shape, and — the point of the whole change —
// holds the check to still taking what a supplier register is actually full of.
func TestARejectedContactDetailNamesTheBox(t *testing.T) {
	in := map[string]any{"phone": "내선 3번"}
	w := httptest.NewRecorder()
	if validPhoneFields(w, in, phoneField{"phone", "전화번호"}) {
		t.Fatal("a note was accepted where a telephone number belongs")
	}
	if body := w.Body.String(); !strings.Contains(body, "전화번호") {
		t.Errorf("the rejection does not name the box to fix: %s", body)
	}

	site := map[string]any{"website": "사내 위키 - 자재부"}
	w = httptest.NewRecorder()
	if validWebsiteFields(w, site, websiteField{"website", "웹사이트"}) {
		t.Fatal("a document title was accepted where a web address belongs")
	}
	if body := w.Body.String(); !strings.Contains(body, "웹사이트") {
		t.Errorf("the rejection does not name the box to fix: %s", body)
	}

	// A detail the caller did not send keeps whatever the statement already
	// holds, the same way the date, number, label, id and address checks beside
	// it leave an absent field alone.
	if w := httptest.NewRecorder(); !validPhoneFields(w, map[string]any{}, phoneField{"phone", "전화번호"}) {
		t.Errorf("an unsent number was refused: %s", w.Body.String())
	}
	if w := httptest.NewRecorder(); !validWebsiteFields(w, map[string]any{}, websiteField{"website", "웹사이트"}) {
		t.Errorf("an unsent address was refused: %s", w.Body.String())
	}

	// What is stored is one shape. The buyer types the host the way it is
	// printed; the portal's form used to demand the scheme and lock the
	// supplier out of the rest of the page over it.
	typed := map[string]any{"website": "  WWW.Acme.CO.KR/제품  "}
	if w := httptest.NewRecorder(); !validWebsiteFields(w, typed, websiteField{"website", "웹사이트"}) {
		t.Fatalf("a bare host was refused: %s", w.Body.String())
	}
	if typed["website"] != "https://www.acme.co.kr/제품" {
		t.Errorf("the stored address is %q, which is not the one shape the column holds", typed["website"])
	}

	for _, wrong := range []string{
		// The extension note, and the two answers somebody gives instead of a
		// number.
		"내선 3번", "본사에 문의", "-",
		// The department from the box above, and a template nobody filled in.
		"구매팀", "${supplierPhone}",
		// Digits, but not enough of them to reach anybody.
		"1234", "02-123",
	} {
		w := httptest.NewRecorder()
		if _, ok := validPhone(w, wrong, "전화번호"); ok {
			t.Errorf("%q was accepted as a telephone number", wrong)
		}
	}

	// The numbers a supplier register actually holds, none of which this may
	// start refusing.
	for _, right := range []string{
		"02-1234-5678", "010-1234-5678", "1588-0000", "+82 2 1234 5678",
		"(031) 123-4567", "02-1234-5678/9", "+1 212 555 0100", "0212345678",
	} {
		w := httptest.NewRecorder()
		if _, ok := validPhone(w, right, "전화번호"); !ok {
			t.Errorf("%q was refused: %s", right, w.Body.String())
		}
	}

	for _, wrong := range []string{
		// The note in place of the address, in both scripts.
		"사내 위키 - 자재부", "확인 후 입력", "ask the sales rep",
		// A hostname on somebody's own machine, and a bare name.
		"localhost", "http://intranet", "acme",
		// Schemes that are not a company's website, including the one that is a
		// script rather than a destination.
		"javascript:alert(1)", "ftp://files.acme.co.kr", "mailto:gu@acme.co.kr",
		// The shape a phishing link is written in.
		"https://acme.co.kr@evil.example",
		// Malformed hosts.
		"https://", "https://-acme.co.kr", "https://acme..co.kr", "https://acme.k",
		strings.Repeat("a", 200) + ".example",
	} {
		w := httptest.NewRecorder()
		if _, ok := validWebsite(w, wrong, "웹사이트"); ok {
			t.Errorf("%q was accepted as a web address", wrong)
		}
	}

	// The addresses a supplier register actually holds, each with the one shape
	// the column ends up holding it in.
	for typed, stored := range map[string]string{
		"acme.co.kr":                     "https://acme.co.kr",
		"www.acme.co.kr":                 "https://www.acme.co.kr",
		"https://www.acme.co.kr":         "https://www.acme.co.kr",
		"http://acme-tooling.com/about":  "http://acme-tooling.com/about",
		"HTTPS://Acme.CO.KR":             "https://acme.co.kr",
		"acme.co.kr:8443/portal":         "https://acme.co.kr:8443/portal",
		"acme.co.kr?utm_source=business": "https://acme.co.kr?utm_source=business",
	} {
		w := httptest.NewRecorder()
		got, ok := validWebsite(w, typed, "웹사이트")
		if !ok {
			t.Errorf("%q was refused: %s", typed, w.Body.String())
			continue
		}
		if got != stored {
			t.Errorf("%q was stored as %q, want %q", typed, got, stored)
		}
	}
}
