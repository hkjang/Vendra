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

// TestEveryRiskGradeIsInTheVocabulary keeps this sweep from having to be redone,
// the way TestEveryDateCastIsGuarded, TestEveryRequestNumberIsBounded and
// TestEveryRequestLabelIsBounded keep the date, number and text ones.
//
// Same method: an operation rather than a spelling. The operation is a request
// value written into a risk grade — the column, or the approval rule that
// matches on it. The queries branch on the four spellings in riskGrades, so a
// grade outside them is not a different grade but no grade: the award
// calculation reads it as worse than CRITICAL, the high-risk count on the
// dashboard skips it, the recommendation shortlist keeps it, and an approval
// rule written for HIGH never fires for it. createRisk had checked its own
// field since it was written; the other five doors into the same vocabulary
// stored whatever arrived.
func TestEveryRiskGradeIsInTheVocabulary(t *testing.T) {
	readsAGrade := regexp.MustCompile(`stringValue\(in, "(riskLevel|severity)"\)`)
	writes := regexp.MustCompile(`INSERT INTO|UPDATE `)
	validates := regexp.MustCompile(`validEnumFields\(|validEnum\(|validWorkflowConditions\(`)
	// The handlers that decode into a typed struct, so stringValue never appears
	// in them. They are named here because dropping their check would otherwise
	// be invisible to this test.
	typed := map[string]bool{"createWorkflow": true, "updateWorkflow": true}

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
				body := string(source)[int(fn.Pos())-base : int(fn.End())-base]
				if typed[fn.Name.Name] {
					seen[fn.Name.Name] = true
					if !validates.MatchString(body) {
						t.Errorf("%s: %s stores an approval rule without checking the grade it routes on", name, fn.Name.Name)
					}
					continue
				}
				if !writes.MatchString(body) || !readsAGrade.MatchString(body) {
					continue
				}
				if !validates.MatchString(body) {
					t.Errorf("%s: %s writes a risk grade from the request without checking it "+
						"against riskGrades; a grade outside the four is stored as written and "+
						"then every query that branches on the spelling takes its ELSE",
						name, fn.Name.Name)
				}
			}
		}
	}
	for name := range typed {
		if !seen[name] {
			t.Errorf("%s was renamed or removed; this test is no longer watching it", name)
		}
	}
}

// TestARejectedGradeNamesTheBoxAndTheChoices holds the rejection to naming both
// the field and what it will accept. "저장하지 못했습니다" was the old answer to
// a bad value that reached PostgreSQL, and it told the caller neither.
func TestARejectedGradeNamesTheBoxAndTheChoices(t *testing.T) {
	field := riskGradeField("riskLevel", "리스크 등급")
	for _, grade := range riskGrades {
		w := httptest.NewRecorder()
		if !validEnum(w, grade, field) {
			t.Errorf("%s is one of the four and was refused: %s", grade, w.Body.String())
		}
	}
	// Absent is not wrong: the statement's own default applies, the same way
	// the date, number and text checks leave an unsent field alone.
	if w := httptest.NewRecorder(); !validEnum(w, "", field) {
		t.Errorf("an unsent grade was refused: %s", w.Body.String())
	}
	for _, wrong := range []string{"high", "High", "매우 높음", "5"} {
		w := httptest.NewRecorder()
		if validEnum(w, wrong, field) {
			t.Fatalf("%q was accepted as a risk grade", wrong)
		}
		body := w.Body.String()
		if !strings.Contains(body, "리스크 등급은") {
			t.Errorf("the rejection of %q does not name the box to fix: %s", wrong, body)
		}
		for _, grade := range riskGrades {
			if !strings.Contains(body, grade) {
				t.Errorf("the rejection of %q does not offer %s: %s", wrong, grade, body)
			}
		}
	}
}
