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

// TestEveryStoredCurrencyIsOneTheApplicationCanPrice keeps this sweep from
// having to be redone, the way TestEveryStoredContactDetailIsOne,
// TestEveryRiskGradeIsInTheVocabulary, TestEverySourcingStandingIsInTheVocabulary
// and TestEveryStoredEmailIsAnAddress keep the ones before it.
//
// Same method: an operation rather than a spelling. The operation is a value
// taken from the request and written into a currency column — so the
// statement's own column list is what this reads, not the name the request
// happens to give the field.
//
// The code is the unit of a number the application adds up and ranks, and it
// was never looked at on any of the five doors: the buyer's business object,
// its correction, the portal's own submission, the bid, and the spend ledger.
// A text length was the only bound, and every wrong answer here is short.
func TestEveryStoredCurrencyIsOneTheApplicationCanPrice(t *testing.T) {
	insert := regexp.MustCompile(`INSERT INTO [a-z_]+\(([^)]*)\)`)
	// The assignments of an UPDATE, up to its WHERE — so that a currency read
	// in a lookup is not mistaken for one being stored.
	assignments := regexp.MustCompile(`SET ([^` + "`" + `]*)`)
	// validCurrency and validCurrencyFields.
	checks := regexp.MustCompile(`valid[A-Za-z]*Currency[A-Za-z]*\(`)

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
				stores := false
				for _, m := range insert.FindAllStringSubmatch(body, -1) {
					for _, c := range strings.Split(m[1], ",") {
						if strings.TrimSpace(c) == "currency" {
							stores = true
						}
					}
				}
				for _, m := range assignments.FindAllStringSubmatch(body, -1) {
					set, _, _ := strings.Cut(m[1], " WHERE ")
					for _, c := range strings.Split(set, ",") {
						if strings.HasPrefix(strings.TrimSpace(c), "currency=") {
							stores = true
						}
					}
				}
				if !stores {
					continue
				}
				writers++
				if !checks.MatchString(body) {
					t.Errorf("%s: %s writes the currency column without checking the code is "+
						"one the application can price. A length is not a check here — the code "+
						"is the unit of a number this application adds up and ranks, and it "+
						"converts between none of them. Check it with validCurrencyFields",
						name, fn.Name.Name)
				}
			}
		}
	}
	// The sweep is only worth anything while it still finds the statements: the
	// object create and its correction, the portal's own object, the bid, and
	// the spend ledger row.
	if writers < 5 {
		t.Errorf("only %d statements writing a currency column were found; the patterns "+
			"this test matches on have gone stale", writers)
	}
}

// TestARejectedCurrencyNamesTheBox holds the rejection to naming the field and
// the accepted value to the one shape the column holds.
func TestARejectedCurrencyNamesTheBox(t *testing.T) {
	in := map[string]any{"currency": "원"}
	w := httptest.NewRecorder()
	if validCurrencyFields(w, in, currencyField("currency", "통화")) {
		t.Fatal("a symbol was accepted where a currency code belongs")
	}
	if body := w.Body.String(); !strings.Contains(body, "통화") || !strings.Contains(body, "KRW") {
		t.Errorf("the rejection does not name the box to fix and what goes in it: %s", body)
	}

	// A code the caller did not send keeps whatever the statement already
	// holds, the same way the date, number, label, id, address and grade checks
	// beside it leave an absent field alone.
	if w := httptest.NewRecorder(); !validCurrencyFields(w, map[string]any{}, currencyField("currency", "통화")) {
		t.Errorf("an unsent code was refused: %s", w.Body.String())
	}

	// Case is not part of the answer. "usd" and "USD" are the same currency and
	// only one of them is a code the column holds and the screens can format.
	typed := map[string]any{"currency": "  usd "}
	if w := httptest.NewRecorder(); !validCurrencyFields(w, typed, currencyField("currency", "통화")) {
		t.Fatalf("a lower-case code was refused: %s", w.Body.String())
	}
	if typed["currency"] != "USD" {
		t.Errorf("the stored code is %q, which is not the one shape the column holds", typed["currency"])
	}

	for _, wrong := range []string{
		// The symbols and the words people write instead of the code.
		"원", "₩", "$", "달러", "won", "KRW 원",
		// A code that is not a currency, and one that is a country.
		"KRWW", "KR", "K",
		// The template nobody filled in.
		"${currency}",
	} {
		w := httptest.NewRecorder()
		if _, ok := validCurrency(w, wrong, "통화"); ok {
			t.Errorf("%q was accepted as a currency code", wrong)
		}
	}

	// Every code the vocabulary names, in the shape the forms offer it.
	for _, right := range currencyCodes {
		w := httptest.NewRecorder()
		got, ok := validCurrency(w, right, "통화")
		if !ok {
			t.Errorf("%q was refused: %s", right, w.Body.String())
			continue
		}
		if got != right {
			t.Errorf("%q was stored as %q", right, got)
		}
	}
}
