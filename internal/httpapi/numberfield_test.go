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

func TestValidNumberNamesTheFieldAndTheRange(t *testing.T) {
	amount := amountField("amount", "금액")
	for _, value := range []float64{0, 1, 1000, maxAmount} {
		rec := httptest.NewRecorder()
		if !validNumber(rec, value, amount) {
			_, msg := errorCodeAndMessage(t, rec)
			t.Errorf("validNumber(%v) rejected a usable value: %s", value, msg)
		}
	}
	// -0.01 is the smallest wrong one, and the two above the ceiling are the
	// pair that overflowed numeric(20,4) and numeric(20,2) respectively.
	for _, value := range []float64{-0.01, -1000, maxAmount + 1e9, 1e17, 1e19} {
		rec := httptest.NewRecorder()
		if validNumber(rec, value, amount) {
			t.Errorf("validNumber(%v) accepted it", value)
			continue
		}
		code, msg := errorCodeAndMessage(t, rec)
		if code != "validation_error" {
			t.Errorf("validNumber(%v) answered %q, want validation_error", value, code)
		}
		if !strings.HasPrefix(msg, "금액은 ") {
			t.Errorf("validNumber(%v) said %q; it has to name the field, with the right particle", value, msg)
		}
		// The caller cannot correct the value without being told the range.
		if !strings.Contains(msg, "1,000,000,000,000,000") {
			t.Errorf("validNumber(%v) said %q; it has to say what the limit is", value, msg)
		}
	}
}

func TestValidNumberFieldsSkipsAbsentFieldsAndReportsTheFirstBadOne(t *testing.T) {
	// An absent field is not a zero: the statements leave the column alone or
	// apply their own default, so ranging a field nobody sent would refuse
	// requests that were always fine.
	if !validNumberFields(httptest.NewRecorder(), map[string]any{"title": "제목"},
		amountField("amount", "금액"), objectScoreField) {
		t.Error("validNumberFields rejected a body that carried no numbers at all")
	}
	if !validNumberFields(httptest.NewRecorder(), map[string]any{"amount": float64(500000), "score": float64(88)},
		amountField("amount", "금액"), objectScoreField) {
		t.Error("validNumberFields rejected a usable pair")
	}

	rec := httptest.NewRecorder()
	if validNumberFields(rec, map[string]any{"amount": float64(1000), "score": float64(9999)},
		amountField("amount", "금액"), objectScoreField) {
		t.Fatal("validNumberFields accepted a score of 9999")
	}
	_, msg := errorCodeAndMessage(t, rec)
	if !strings.HasPrefix(msg, "점수는 ") {
		t.Errorf("reported %q; the bad field is the score", msg)
	}
}

func TestGroupDigitsWritesABoundSomebodyCanRead(t *testing.T) {
	for value, want := range map[float64]string{
		0: "0", 10: "10", 100: "100", 1000: "1,000", 36500: "36,500",
		maxAmount: "1,000,000,000,000,000", -1500: "-1,500", 1234.5: "1,234.5",
	} {
		if got := groupDigits(value); got != want {
			t.Errorf("groupDigits(%v) = %q, want %q", value, got, want)
		}
	}
}

// TestEveryRequestNumberIsBounded keeps this sweep from having to be redone,
// the way TestEveryDateCastIsGuarded keeps the date one.
//
// The dates were found by an operation — a request value reaching PostgreSQL
// as `$n::date` — rather than by a spelling, and the numbers are the same
// shape: a value taken out of the request body with numberValue and handed to
// a numeric column. Unbounded, it either overflows the column, which comes
// back as "저장하지 못했습니다" with no field named, or it fits and is worse:
// a negative amount subtracts from the spend rollup and slips under every
// approval threshold, and an off-scale score outranks every honest one.
func TestEveryRequestNumberIsBounded(t *testing.T) {
	readsANumber := regexp.MustCompile(`numberValue\(`)
	validates := regexp.MustCompile(`validNumberFields\(|validNumber\(`)
	// The two handlers that decode into a typed struct instead of a map, so
	// numberValue never appears in them. They are named here because dropping
	// their bound would otherwise be invisible to this test.
	typed := map[string]bool{"createSpendTransaction": true, "portalSourcingResponse": true}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(f os.FileInfo) bool {
		return !strings.HasSuffix(f.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	seen := map[string]bool{}
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
				if typed[fn.Name.Name] {
					seen[fn.Name.Name] = true
				} else if fn.Name.Name == "numberValue" || !readsANumber.MatchString(body) {
					continue
				}
				if validates.MatchString(body) {
					continue
				}
				t.Errorf("%s: %s takes a number from the request without bounding it; "+
					"out of range it reaches PostgreSQL and comes back as an unattributed save failure, "+
					"and in range it is stored as if somebody meant it",
					name, fn.Name.Name)
			}
		}
	}
	for name := range typed {
		if !seen[name] {
			t.Errorf("%s was renamed or removed; this test is no longer watching it", name)
		}
	}
}
