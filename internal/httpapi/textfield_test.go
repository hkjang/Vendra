package httpapi

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

// unboundedByDesign lists the request-body strings a write handler deliberately
// does not measure, with the reason. Everything else that names a record has to
// be bounded, and this list is what keeps the sweep below honest instead of
// merely quiet.
var unboundedByDesign = map[string]string{
	// Long free text. These are paragraphs by design, read on a detail page and
	// never laid out beside anything, so the 2 MB body limit is their bound.
	"description": "리스크 설명",
	"mitigation":  "리스크 대응 방안",
	"comments":    "평가 의견",
	// Enumerated in the handler itself, which is a stricter check than a length.
	"severity": "리스크 등급",
	// Held to the shape of an address by the email sweep, which is a stricter
	// check than a length and bounds it at maxEmailLen on the way past.
	// TestEveryStoredEmailIsAnAddress watches these, and watches them by the
	// column they are written to rather than by the name the request gives them.
	"email": "이메일",
}

// TestEveryRequestLabelIsBounded keeps this sweep from having to be redone, the
// way TestEveryDateCastIsGuarded and TestEveryRequestNumberIsBounded keep the
// date and number ones.
//
// Same method: an operation rather than a spelling. The operation is a short
// free-text field taken from the request body and written to a text column the
// record is then displayed by — a title, a name, a code, a number. PostgreSQL
// text has no length, so nothing rejects a 20,000-character title; it saves,
// and then it is in every list, dropdown, export and audit line that quotes the
// record, on pages nobody has opened yet.
func TestEveryRequestLabelIsBounded(t *testing.T) {
	readsAString := regexp.MustCompile(`stringValue\(in, "([A-Za-z]+)"\)`)
	// A {"key", "label"} literal — textField or dateField. A field the date
	// sweep already checks arrives with its own bound and needs no second one.
	boundedKey := regexp.MustCompile(`\{"([A-Za-z]+)",\s*"[^"]*"\}`)
	writes := regexp.MustCompile(`INSERT INTO|UPDATE `)
	// The handlers that decode into a typed struct, so stringValue never appears
	// in them. They are named here because dropping their bound would otherwise
	// be invisible to this test.
	typed := map[string]bool{
		"createSpendTransaction": true, "createSupplierRelationship": true,
		"registerSupplierUser": true, "createUser": true, "updateUser": true,
		"updateMe": true, "createRole": true, "updateRole": true,
		"createOrganization": true, "createScorecard": true, "createAPIKey": true,
		"createScreeningTemplate": true,
	}
	validates := regexp.MustCompile(`validTextFields\(|validText\(`)

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(f os.FileInfo) bool {
		return !strings.HasSuffix(f.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	// The shared field lists, so a handler that spells its bound as
	// supplierTextFields counts the same as one that writes the literals out.
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
				varName, rest, ok := strings.Cut(decl, " = []textField{")
				if !ok || strings.ContainsAny(varName, " \t\n") {
					continue
				}
				list, _, _ := strings.Cut(rest, "\n}")
				for _, m := range boundedKey.FindAllStringSubmatch(list, -1) {
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
						t.Errorf("%s: %s decodes a typed body and bounds none of its labels", name, fn.Name.Name)
					}
					continue
				}
				if !writes.MatchString(body) {
					continue
				}
				bounded := map[string]bool{}
				for _, m := range boundedKey.FindAllStringSubmatch(body, -1) {
					bounded[m[1]] = true
				}
				for varName, keys := range shared {
					if strings.Contains(body, varName) {
						for _, k := range keys {
							bounded[k] = true
						}
					}
				}
				for _, m := range readsAString.FindAllStringSubmatch(body, -1) {
					key := m[1]
					// An id is a uuid the statement casts, not a label.
					if strings.HasSuffix(key, "Id") || bounded[key] {
						continue
					}
					if _, exempt := unboundedByDesign[key]; exempt {
						continue
					}
					t.Errorf("%s: %s writes %q from the request without bounding it; "+
						"text takes any length, so the record is saved and then renders "+
						"everywhere it is listed. Bound it with validTextFields, or say why "+
						"not in unboundedByDesign", name, fn.Name.Name, key)
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
