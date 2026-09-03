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

// notARecordID lists the request-body fields that end in "Id" and are not one,
// with the reason. Everything else has to be checked, and this list is what
// keeps the sweep below honest instead of merely quiet.
var notARecordID = map[string]string{
	// A key in somebody else's system. The column is text, not uuid, and the
	// label sweep already bounds its length.
	"erpVendorId": "ERP 벤더 코드",
}

// TestEveryRequestRecordIDIsChecked keeps this sweep from having to be redone,
// the way TestEveryDateCastIsGuarded, TestEveryRequestNumberIsBounded,
// TestEveryRequestLabelIsBounded and TestEveryRiskGradeIsInTheVocabulary keep
// the date, number, text and vocabulary ones.
//
// Same method: an operation rather than a spelling. The operation is a record
// id taken from the request and written into a uuid column, or used to look up
// the record it names. There were three doors into that operation and only two
// were watched — path ids at the router since app.go was written, query filters
// by uuidParam — while the ids a body carries went straight through.
//
// What arrives is not a different record but no record, and the answer depended
// on who asked. A handler that looks the id up first treats the failed query as
// a denial: supplierScopeAllowed selects the row and returns false on any
// error, so a typo came back as 403 "데이터 접근 범위를 벗어났습니다" — the
// caller told they may not see a record that does not exist. A company-scope
// account skips that lookup, reaches the cast, and gets 400 "저장하지
// 못했습니다" with no field named. Two wrong answers to the same typo, and on
// the upload path the file has already been streamed and hashed by then, so
// every retry spends the upload again.
func TestEveryRequestRecordIDIsChecked(t *testing.T) {
	readsAnID := regexp.MustCompile(`stringValue\(in, "([A-Za-z]+Id)"\)`)
	// A {"key", "label"} literal — uuidField, or the textField the label sweep
	// writes. Either way the key is measured before it reaches the statement.
	checkedKey := regexp.MustCompile(`\{"([A-Za-z]+Id)",\s*"[^"]*"\}`)
	writes := regexp.MustCompile(`INSERT INTO|UPDATE `)
	validates := regexp.MustCompile(`validUUIDFields\(|validRecordID\(`)
	// The handlers that read their ids from a typed struct or a form, so
	// stringValue never appears in them. They are named here because dropping
	// their check would otherwise be invisible to this test.
	typed := map[string]bool{
		"createUser": true, "updateUser": true, "createOrganization": true,
		"createAccessGrant": true, "createSpendTransaction": true,
		"createSupplierRelationship": true, "createScreening": true,
		"selectSourcingResponse": true, "uploadDocument": true, "createInvitation": true,
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(f os.FileInfo) bool {
		return !strings.HasSuffix(f.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	// The shared field lists, so a handler that spells its check as
	// objectUUIDFields counts the same as one that writes the literals out.
	shared := map[string][]string{}
	sources := map[string]string{}
	for _, pkg := range pkgs {
		for name := range pkg.Files {
			source, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			sources[name] = string(source)
			for _, decl := range strings.Split(string(source), "var ") {
				varName, rest, ok := strings.Cut(decl, " = []uuidField{")
				if !ok || strings.ContainsAny(varName, " \t\n") {
					continue
				}
				list, _, _ := strings.Cut(rest, "\n}")
				for _, m := range checkedKey.FindAllStringSubmatch(list, -1) {
					shared[varName] = append(shared[varName], m[1])
				}
			}
		}
	}

	seen := map[string]bool{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			source := sources[name]
			base := fset.File(file.Pos()).Base()
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				body := source[int(fn.Pos())-base : int(fn.End())-base]
				if typed[fn.Name.Name] {
					seen[fn.Name.Name] = true
					if !validates.MatchString(body) {
						t.Errorf("%s: %s reads a record id from a typed body or a form and checks none of them", name, fn.Name.Name)
					}
					continue
				}
				if !writes.MatchString(body) {
					continue
				}
				checked := map[string]bool{}
				for _, m := range checkedKey.FindAllStringSubmatch(body, -1) {
					checked[m[1]] = true
				}
				for varName, keys := range shared {
					if strings.Contains(body, varName) {
						for _, k := range keys {
							checked[k] = true
						}
					}
				}
				for _, m := range readsAnID.FindAllStringSubmatch(body, -1) {
					key := m[1]
					if checked[key] {
						continue
					}
					if _, exempt := notARecordID[key]; exempt {
						continue
					}
					t.Errorf("%s: %s writes %q from the request without checking it is a "+
						"record id; it reaches the statement as $n::uuid, which fails the "+
						"whole write and is reported with no field named — or, where a scope "+
						"lookup runs first, as a permission the caller does not have. Check "+
						"it with validUUIDFields, or say why not in notARecordID",
						name, fn.Name.Name, key)
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

// TestARejectedRecordIDNamesTheBox holds the rejection to naming the field.
// "저장하지 못했습니다" was the old answer on a form carrying four ids, and
// "데이터 접근 범위를 벗어났습니다" was the other one — the second worse than
// the first, because it describes a permission rather than a typo.
func TestARejectedRecordIDNamesTheBox(t *testing.T) {
	in := map[string]any{"supplierId": "SUP-000123"}
	w := httptest.NewRecorder()
	if validUUIDFields(w, in, uuidField{"supplierId", "공급업체 ID"}) {
		t.Fatal("a supplier number was accepted where a record id belongs")
	}
	if body := w.Body.String(); !strings.Contains(body, "공급업체 ID") {
		t.Errorf("the rejection does not name the box to fix: %s", body)
	}
	// An id the caller did not send keeps whatever default the statement
	// applies, the same way the date, number, text and grade checks beside it
	// leave an absent field alone.
	if w := httptest.NewRecorder(); !validUUIDFields(w, map[string]any{}, uuidField{"supplierId", "공급업체 ID"}) {
		t.Errorf("an unsent id was refused: %s", w.Body.String())
	}
	// Surrounding whitespace is the caller pasting an id, not a malformed one:
	// stringValue trims it and so does the statement's parameter.
	if w := httptest.NewRecorder(); !validRecordID(w, "  4f8a1c22-0b3d-4e6f-9a71-2c5d8e0f3b19  ", "공급업체 ID") {
		t.Errorf("a pasted id was refused: %s", w.Body.String())
	}
	for _, wrong := range []string{
		"1", "null", "undefined", "삼성전자",
		// One character short, and the trailing brace of a template that was
		// never filled in — both of which a client sends and PostgreSQL refuses.
		"4f8a1c22-0b3d-4e6f-9a71-2c5d8e0f3b1", "${supplierId}",
	} {
		w := httptest.NewRecorder()
		if validRecordID(w, wrong, "공급업체 ID") {
			t.Errorf("%q was accepted as a record id", wrong)
		}
	}
}
