package httpapi

import (
	"context"
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

// TestEverySupplierStatusIsInTheVocabulary is the risk-grade sweep run over the
// other word the supplier queries branch on.
//
// 거래 상태 is not a label. The dashboard's 거래 가능 tile counts
// status='active' and its 심사 대기 tile status='screening', the
// recommendation tool shortlists only status IN('active','approved'), and the
// register's filter selects on the spelling exactly. So a status outside the
// list is not a different state but no state: the supplier is in none of those
// answers and cannot be found by the filter that would have shown it.
//
// Two doors carried a status from the request — creating a supplier and editing
// one — and neither looked at it. The statuses the application writes for
// itself are checked here too, because the vocabulary is only one vocabulary if
// the SQL and the check agree on it.
func TestEverySupplierStatusIsInTheVocabulary(t *testing.T) {
	readsAStatus := regexp.MustCompile(`stringValue\(in, "status"\)`)
	// Not merely a statement about suppliers: one that puts a status into them.
	// Recording a risk or an evaluation writes a status of its own and touches
	// the supplier row for the grade it rolls up, and neither is this.
	writesTheStatus := regexp.MustCompile("UPDATE suppliers SET [^`]*[^.\\w]status=|INSERT INTO suppliers\\([^)]*\\bstatus\\b")
	validates := regexp.MustCompile(`supplierStatusField\(`)

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
				if !ok || fn.Body == nil {
					continue
				}
				body := string(source)[int(fn.Pos())-base : int(fn.End())-base]
				if !writesTheStatus.MatchString(body) || !readsAStatus.MatchString(body) {
					continue
				}
				if !validates.MatchString(body) {
					t.Errorf("%s: %s writes a supplier status from the request without checking it "+
						"against supplierStatuses; a status outside the list is stored as written and "+
						"the supplier then sits outside every count and filter that names one",
						name, fn.Name.Name)
				}
			}
		}
	}
}

// TestEverySupplierStatusInTheSQLIsInTheVocabulary reads the statements rather
// than the request fields: whatever the application stores in, or branches on,
// suppliers.status has to be a word the check will also accept. Otherwise the
// check is a second vocabulary rather than the one — a supplier the portal
// created could be one the edit form is not allowed to save back.
func TestEverySupplierStatusInTheSQLIsInTheVocabulary(t *testing.T) {
	known := map[string]bool{}
	for _, s := range supplierStatuses {
		known[s] = true
	}
	// A statement that joins another table has a status column of its own —
	// sourcing_responses and workflow_instances both do — so only the
	// single-table ones can be read as being about suppliers.
	aboutSuppliers := regexp.MustCompile(`(?:FROM|UPDATE|INTO) suppliers\b`)
	branches := regexp.MustCompile(`(^|[^.\w])status\s*(?:=|<>|!=)\s*'([a-z_]*)'`)
	sets := regexp.MustCompile(`(^|[^.\w])status\s+(?:NOT\s+)?IN\s*\(([^)]*)\)`)
	quoted := regexp.MustCompile(`'([a-z_]*)'`)

	statements, checked := packageSQL(t), 0
	for _, sql := range statements {
		if !aboutSuppliers.MatchString(sql) || strings.Contains(sql, " JOIN ") {
			continue
		}
		checked++
		for _, m := range branches.FindAllStringSubmatch(sql, -1) {
			if m[2] != "" && !known[m[2]] {
				t.Errorf("a statement branches on status %q, which supplierStatuses does not list, "+
					"so no caller can put a supplier into it: %s", m[2], sql)
			}
		}
		for _, m := range sets.FindAllStringSubmatch(sql, -1) {
			for _, q := range quoted.FindAllStringSubmatch(m[2], -1) {
				if q[1] != "" && !known[q[1]] {
					t.Errorf("a statement branches on status %q, which supplierStatuses does not list: %s", q[1], sql)
				}
			}
		}
		for _, stored := range insertedSupplierStatus(sql) {
			if !known[stored] {
				t.Errorf("a statement stores supplier status %q, which supplierStatuses does not list, "+
					"so the record it creates cannot be saved again from the edit form: %s", stored, sql)
			}
		}
	}
	if checked < 5 {
		t.Errorf("only %d supplier statements were read; the extraction has stopped finding them", checked)
	}
	// Completing a screening moves the supplier itself, choosing the status in
	// Go rather than in the statement. Those words are on the same list.
	moved := regexp.MustCompile(`supplierStatus\s*:?=\s*"([a-z_]*)"`)
	for _, m := range moved.FindAllStringSubmatch(strings.Join(packageSource(t), "\n"), -1) {
		if !known[m[1]] {
			t.Errorf("a handler moves a supplier to status %q, which supplierStatuses does not list", m[1])
		}
	}
}

// packageSource reads the package's own source, tests excluded.
func packageSource(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		files = append(files, string(source))
	}
	return files
}

// insertedSupplierStatus answers the literal statuses an INSERT INTO suppliers
// writes, by lining its VALUES list up against its column list.
func insertedSupplierStatus(sql string) []string {
	start := strings.Index(sql, "INSERT INTO suppliers(")
	if start < 0 {
		return nil
	}
	columns, after := balanced(sql[start+len("INSERT INTO suppliers"):])
	position := -1
	for i, column := range splitTopLevel(columns) {
		if strings.TrimSpace(column) == "status" {
			position = i
		}
	}
	if position < 0 {
		return nil
	}
	values := strings.Index(after, "VALUES(")
	if values < 0 {
		return nil
	}
	list, _ := balanced(after[values+len("VALUES"):])
	items := splitTopLevel(list)
	if position >= len(items) {
		return nil
	}
	var found []string
	for _, m := range regexp.MustCompile(`'([a-z_]*)'`).FindAllStringSubmatch(items[position], -1) {
		if m[1] != "" {
			found = append(found, m[1])
		}
	}
	return found
}

// balanced returns what sits inside the parenthesis s opens with, and the rest
// of s after it closes.
func balanced(s string) (inner, rest string) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return s[1:i], s[i+1:]
			}
		}
	}
	return "", ""
}

// splitTopLevel splits a SQL list on the commas that are not inside a nested
// call or a quoted literal.
func splitTopLevel(s string) []string {
	items, depth, start, quoted := []string{}, 0, 0, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			quoted = !quoted
		case '(':
			if !quoted {
				depth++
			}
		case ')':
			if !quoted {
				depth--
			}
		case ',':
			if !quoted && depth == 0 {
				items = append(items, s[start:i])
				start = i + 1
			}
		}
	}
	return append(items, s[start:])
}

// packageSQL collects the backquoted strings in the package's own source, which
// is where every statement in it is written.
func packageSQL(t *testing.T) []string {
	t.Helper()
	raw := regexp.MustCompile("(?s)`([^`]*)`")
	var statements []string
	for _, source := range packageSource(t) {
		for _, m := range raw.FindAllStringSubmatch(source, -1) {
			statements = append(statements, strings.Join(strings.Fields(m[1]), " "))
		}
	}
	return statements
}

// TestTheStatusListTheFormOffersIsTheOneTheAPIAccepts holds the two surfaces to
// one vocabulary.
//
// This is the check that was missing when the split happened. The register's
// filter, the badge label map and the edit form's dropdown each wrote the list
// out for themselves, and the dropdown's copy said "registered" where the other
// two said "registration" — a spelling that exists nowhere else in the
// application, and now one the API refuses. Choosing 등록 in the edit form
// produced a supplier the 등록 filter never returns and the badge has no Korean
// word for; and because "registration" was missing from the options, opening a
// supplier that had signed itself up through the portal and pressing save reset
// it to 후보, since a select whose value is not among its options shows the
// first one.
func TestTheStatusListTheFormOffersIsTheOneTheAPIAccepts(t *testing.T) {
	source := repoFile(t, "web/src/status.ts")
	declaration := regexp.MustCompile(`(?s)supplierStatuses[^=]*=\s*\[(.*?)\];`).FindStringSubmatch(source)
	if declaration == nil {
		t.Fatal("web/src/status.ts no longer declares supplierStatuses; the two surfaces can drift again")
	}
	var offered []string
	for _, m := range regexp.MustCompile(`value:\s*"([^"]*)"`).FindAllStringSubmatch(declaration[1], -1) {
		offered = append(offered, m[1])
	}
	if strings.Join(offered, ",") != strings.Join(supplierStatuses, ",") {
		t.Errorf("the form offers %v and the API accepts %v; a status on one list and not the other "+
			"is either a value nobody can save or a state nothing displays", offered, supplierStatuses)
	}
	// Every status carries a Korean label, or the badge falls back to the raw
	// English word in the middle of the register.
	for _, m := range regexp.MustCompile(`value:\s*"([^"]*)",\s*label:\s*"([^"]*)"`).FindAllStringSubmatch(declaration[1], -1) {
		if strings.TrimSpace(m[2]) == "" {
			t.Errorf("%s has no label", m[1])
		}
	}
	// The register must take its options from that list rather than writing a
	// fourth copy of it.
	register := repoFile(t, "web/src/pages/Suppliers.tsx")
	for _, status := range supplierStatuses {
		if strings.Contains(register, `<option value="`+status+`"`) {
			t.Errorf("Suppliers.tsx writes the status list out again (%s); that is how the two lists came apart", status)
		}
	}
}

// TestARejectedStatusNamesTheBoxAndTheChoices holds the rejection to naming
// both the field and what it will accept, the way the risk-grade one does.
func TestARejectedStatusNamesTheBoxAndTheChoices(t *testing.T) {
	field := supplierStatusField("status", "거래 상태")
	for _, status := range supplierStatuses {
		if w := httptest.NewRecorder(); !validEnum(w, status, field) {
			t.Errorf("%s is on the list and was refused: %s", status, w.Body.String())
		}
	}
	// Absent is not wrong: creating a supplier without one leaves the
	// statement's own 'candidate' in place.
	if w := httptest.NewRecorder(); !validEnum(w, "", field) {
		t.Errorf("an unsent status was refused: %s", w.Body.String())
	}
	for _, wrong := range []string{"registered", "Active", "거래 가능", "in_progress"} {
		w := httptest.NewRecorder()
		if validEnum(w, wrong, field) {
			t.Fatalf("%q was accepted as a 거래 상태", wrong)
		}
		body := w.Body.String()
		if !strings.Contains(body, "거래 상태는") {
			t.Errorf("the rejection of %q does not name the box to fix: %s", wrong, body)
		}
		if !strings.Contains(body, "registration") {
			t.Errorf("the rejection of %q does not offer the statuses it would take: %s", wrong, body)
		}
	}
}

// TestEverySupplierStatusOnAWriteIsInTheVocabulary calls the two doors rather
// than reading them, the way the date, amount, label, grade, record-id and
// address sweeps each do.
//
// A supplier's 거래 상태 decides which lists it appears in. The dashboard counts
// status='active' and status='screening', the recommendation tool shortlists
// status IN('active','approved'), and the register's filter selects on the word
// itself. Neither creating a supplier nor editing one looked at the status it
// was given, so a spelling nobody else uses was stored without complaint and
// the supplier then appeared in none of those answers — findable only by
// scrolling the unfiltered register, which is the screen nobody uses.
func TestEverySupplierStatusOnAWriteIsInTheVocabulary(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	var supplierID string
	if err := pool.QueryRow(ctx, `INSERT INTO suppliers(supplier_number,name,business_number,status,risk_level) VALUES('SUP-STATUSSWEEP','상태 검증 업체','SUP-STATUSSWEEP','active','LOW')
		ON CONFLICT(supplier_number) DO UPDATE SET name=excluded.name,status='active' RETURNING id`).Scan(&supplierID); err != nil {
		t.Fatalf("seed supplier: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs and these would silently no-op.
		_, _ = pool.Exec(ctx, `DELETE FROM suppliers WHERE business_number IN('SUP-STATUSSWEEP','SUP-STATUSSWEEP-NEW')`)
	})

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.204:5000"))
	send := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	for _, tc := range []struct {
		what, method, path, body string
	}{
		// The spelling the edit form itself used to offer, which nothing else
		// in the application has ever read.
		{"the status the edit form used to save", http.MethodPatch, "/api/v1/suppliers/" + supplierID,
			`{"status":"registered"}`},
		{"a status in the case the queries do not compare in", http.MethodPatch, "/api/v1/suppliers/" + supplierID,
			`{"status":"Active"}`},
		{"a supplier registered into a state nothing lists", http.MethodPost, "/api/v1/suppliers",
			`{"name":"Meridian Alloys","businessNumber":"SUP-STATUSSWEEP-NEW","status":"in_progress"}`},
		{"the Korean label instead of the word behind it", http.MethodPost, "/api/v1/suppliers",
			`{"name":"Meridian Alloys","businessNumber":"SUP-STATUSSWEEP-NEW","status":"거래 가능"}`},
	} {
		w := send(tc.method, tc.path, tc.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: returned %d, want 400: %s", tc.what, w.Code, w.Body.String())
			continue
		}
		code, msg := errorCodeAndMessage(t, w)
		if code != "validation_error" {
			t.Errorf("%s: answered %q, want validation_error: %s", tc.what, code, msg)
		}
		if !strings.Contains(msg, "거래 상태") {
			t.Errorf("%s: the rejection does not name the box to fix: %s", tc.what, msg)
		}
	}

	// None of them reached the table, and the supplier they aimed at still
	// holds the status it was seeded with.
	var stored string
	if err := pool.QueryRow(ctx, `SELECT status FROM suppliers WHERE id=$1`, supplierID).Scan(&stored); err != nil {
		t.Fatalf("read the supplier status: %v", err)
	}
	if stored != "active" {
		t.Errorf("a rejected update left the supplier at %q", stored)
	}
	var created int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM suppliers WHERE business_number='SUP-STATUSSWEEP-NEW'`).Scan(&created); err != nil {
		t.Fatalf("count the suppliers: %v", err)
	}
	if created != 0 {
		t.Errorf("a rejected create left %d rows in suppliers", created)
	}

	// Every status the register works in is still accepted, and is the one the
	// row ends up holding.
	for _, status := range supplierStatuses {
		w := send(http.MethodPatch, "/api/v1/suppliers/"+supplierID, `{"status":"`+status+`"}`)
		if w.Code != http.StatusOK {
			t.Errorf("%s is on the list and the edit returned %d: %s", status, w.Code, w.Body.String())
			continue
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM suppliers WHERE id=$1`, supplierID).Scan(&stored); err != nil {
			t.Fatalf("read the supplier status: %v", err)
		}
		if stored != status {
			t.Errorf("saving %s left the supplier at %q", status, stored)
		}
	}
}
