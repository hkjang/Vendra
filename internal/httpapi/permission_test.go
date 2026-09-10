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

	"golang.org/x/crypto/bcrypt"
)

// requiredPermission finds the permission a door asks for: require("x", …) and
// hasPermission(p, "x"). The doors registered from objectRoutes ask for
// typeName+".read" and are counted from the table itself, not from here.
var requiredPermission = []*regexp.Regexp{
	regexp.MustCompile(`\brequire\("([^"]+)"`),
	regexp.MustCompile(`\bhasPermission\([A-Za-z_.]+, "([^"]+)"\)`),
}

// TestEveryPermissionTheApplicationChecksIsOneARoleCanBeGiven holds the
// vocabulary to the doors, in both directions.
//
// A permission is only ever read on the wanted side of hasPermission, so the
// two halves fail in opposite ways. A door asking for a permission the
// catalogue cannot offer is a screen no role can be given — the person who
// needs it gets a 403 nobody can clear. A permission in the catalogue that no
// door asks for is the other half, and it is the one that had already
// happened: "procurement.*" could be handed out, listed in the role table and
// opened nothing at all.
func TestEveryPermissionTheApplicationChecksIsOneARoleCanBeGiven(t *testing.T) {
	offered := map[string]bool{}
	for _, code := range permissionCodes() {
		if offered[code] {
			t.Errorf("%q is listed twice", code)
		}
		offered[code] = true
	}

	checked := map[string]string{}
	for _, route := range objectRoutes {
		// The three routes registered from the table, and the fourth door that
		// is not a route: objects.go asks for objectType+".amount.read" before
		// it shows the money.
		for _, action := range []string{"read", "create", "update", "amount.read"} {
			checked[route.objectType+"."+action] = route.path
		}
	}
	// The MCP tools name their permission in a map rather than at a door, so
	// the same words have to be collected from there.
	mcp := regexp.MustCompile(`(?s)required := map\[string\]string\{(.*?)\}\[`).
		FindStringSubmatch(repoFile(t, "internal/httpapi/integrations.go"))
	if mcp == nil {
		t.Fatal("integrations.go no longer maps an MCP tool to the permission it needs")
	}
	for _, m := range regexp.MustCompile(`:\s*"([^"]+)"`).FindAllStringSubmatch(mcp[1], -1) {
		checked[m[1]] = "the MCP tool map"
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source := repoFile(t, "internal/httpapi/"+entry.Name())
		for _, pattern := range requiredPermission {
			for _, m := range pattern.FindAllStringSubmatch(source, -1) {
				checked[m[1]] = entry.Name()
			}
		}
	}
	if len(checked) == 0 {
		t.Fatal("no permission check was found in the package; this test is no longer watching anything")
	}

	for permission, where := range checked {
		if !offered[permission] {
			t.Errorf("%s asks for %q and no role can be given it, so the screen behind that door is unreachable",
				where, permission)
		}
	}
	for _, permission := range permissionCodes() {
		if _, asked := checked[permission]; !asked {
			t.Errorf("a role can be given %q and no door asks for it; the role lists the permission and opens nothing",
				permission)
		}
	}
}

// TestEveryStatementThatWritesRolePermissionsChecksThem keeps the sweep from
// having to be redone, the way TestEveryStatementThatWritesApprovalStepsChecks
// Them and TestEveryStoredContactDetailIsOne keep theirs. The operation, not a
// spelling: a value out of the request written into the permissions or the
// data_scope column of roles.
func TestEveryStatementThatWritesRolePermissionsChecksThem(t *testing.T) {
	insert := regexp.MustCompile(`INSERT INTO roles\(([^)]*)\)`)
	assignments := regexp.MustCompile(`UPDATE roles SET ([^` + "`" + `]*)`)
	checks := map[string]*regexp.Regexp{
		"permissions": regexp.MustCompile(`validPermissions\(`),
		"data_scope":  regexp.MustCompile(`dataScopeField\(`),
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
						t.Errorf("%s writes %s into roles without checking it; a word no door asks for "+
							"is stored, listed and opens nothing", fn.Name.Name, column)
					}
				}
			}
		}
	}
	if writers < 4 {
		t.Errorf("found %d statements writing a role's permissions or scope, want the create and the update of each; "+
			"this test is looking in the wrong place", writers)
	}
}

// TestARejectedPermissionNamesTheBoxAndWhatItWouldTake holds the rejection to
// naming the field, the word and what could have been meant. A permission is
// typed rather than picked, so a refusal that does not carry the list leaves
// the writer guessing at the very thing they got wrong.
func TestARejectedPermissionNamesTheBoxAndWhatItWouldTake(t *testing.T) {
	// Every permission the shipped catalogue is written with, wildcards
	// included, has to pass — the edit form sends the stored list back.
	for _, good := range []string{"*", "*.read", "supplier.*", "portal.*", "contract.*", "dashboard.read",
		"purchase_request.create", "risk.read", "evaluation.*", "workflow.read", "payment.*", "ai.use"} {
		w := httptest.NewRecorder()
		if _, ok := validPermissions(w, []string{good}, "권한"); !ok {
			t.Errorf("%q opens a door and was refused: %s", good, w.Body.String())
		}
	}
	for _, tc := range []struct{ wrong, offers string }{
		{"procurement.*", "supplier"},                   // no such area: the areas there are
		{"risk.security.*", "risk.read"},                // an area that exists: its doors
		{"contract.review", "contract.read"},            // ditto
		{"supplier.raed", "supplier.read"},              // a typo in the last segment
		{"purchase_request", "purchase_request.create"}, // an area with no action
	} {
		w := httptest.NewRecorder()
		if _, ok := validPermissions(w, []string{"supplier.read", tc.wrong}, "권한"); ok {
			t.Fatalf("%q was accepted as a 권한", tc.wrong)
		}
		body := w.Body.String()
		if !strings.Contains(body, "권한") || !strings.Contains(body, tc.wrong) {
			t.Errorf("the rejection of %q does not name the box and the word: %s", tc.wrong, body)
		}
		if !strings.Contains(body, tc.offers) {
			t.Errorf("the rejection of %q does not offer %q: %s", tc.wrong, tc.offers, body)
		}
	}
	// A blank entry is the textarea's stray line, and it would be stored as a
	// permission that matches nothing.
	w := httptest.NewRecorder()
	if _, ok := validPermissions(w, []string{"supplier.read", "  "}, "권한"); ok {
		t.Error("a blank permission was accepted")
	}
	// And the stored list is the cleaned one.
	cleaned, ok := validPermissions(httptest.NewRecorder(), []string{" supplier.read ", "contract.*"}, "권한")
	if !ok || strings.Join(cleaned, ",") != "supplier.read,contract.*" {
		t.Errorf("validPermissions cleaned to %v", cleaned)
	}
	if cleaned, ok := validPermissions(httptest.NewRecorder(), nil, "권한"); !ok || cleaned == nil {
		t.Error("an empty list has to clean to an empty list, not to JSON null: a role stored as null " +
			"cannot be read by the login query at all")
	}
}

// TestEveryDataScopeIsOneTheQueriesBranchOn holds the scope vocabulary to the
// two places that read the column. Both take their ELSE as 'own', so a scope
// spelled any other way is not refused anywhere — it is quietly the narrowest
// one, at login, long after the role was saved as 전사.
func TestEveryDataScopeIsOneTheQueriesBranchOn(t *testing.T) {
	auth := repoFile(t, "internal/httpapi/auth.go")
	scopeFunction := repoFile(t, "internal/db/migrations/005_division_scope.sql")
	for _, scope := range dataScopes {
		if scope == "own" {
			continue // the ELSE of both, so it is not spelled out in either
		}
		if !strings.Contains(auth, "WHEN '"+scope+"' THEN") {
			t.Errorf("the login query does not read %q, so a role saved with it logs in as 'own'", scope)
		}
		if !strings.Contains(scopeFunction, "'"+scope+"'") {
			t.Errorf("vendra_org_in_scope does not know %q", scope)
		}
	}
	// And the only screen a scope is chosen from offers exactly these.
	admin := repoFile(t, "web/src/pages/Admin.tsx")
	form := regexp.MustCompile(`(?s)<select name="dataScope".*?</select>`).FindString(admin)
	if form == "" {
		t.Fatal("Admin.tsx no longer has a dataScope picker; this test is no longer watching the form")
	}
	offered := regexp.MustCompile(`<option value="([^"]*)"`).FindAllStringSubmatch(form, -1)
	if len(offered) != len(dataScopes) {
		t.Fatalf("the form offers %d scopes and the queries branch on %d", len(offered), len(dataScopes))
	}
	for i, m := range offered {
		if m[1] != dataScopes[i] {
			t.Errorf("the form offers %q where the queries read %q", m[1], dataScopes[i])
		}
	}
}

// TestThePermissionsTheFormOffersAreTheOnesTheAPIAccepts holds the roles form
// and the gate to one list, the way the workflow types and the supplier
// statuses are held. The form is where a permission is typed, and it offered no
// list at all — which is how a role came to be written with a word no door
// asks for in the first place.
func TestThePermissionsTheFormOffersAreTheOnesTheAPIAccepts(t *testing.T) {
	source := repoFile(t, "web/src/status.ts")
	offered := tsStringList(t, source, "permissionCodes")
	if strings.Join(offered, ",") != strings.Join(permissionCodes(), ",") {
		t.Errorf("the form offers %v and the gate checks %v; a permission on one list and not the other "+
			"is either one nobody can hand out or one that opens nothing", offered, permissionCodes())
	}
	admin := repoFile(t, "web/src/pages/Admin.tsx")
	if !strings.Contains(admin, "permissionCodes.map(") {
		t.Error("the 권한 field does not read permissionCodes, so the list is written out twice or not at all")
	}
}

// TestTheRolesTheProductShipsWithReachTheScreensTheyName calls the doors rather
// than reading them.
//
// 구매 관리자 is the role the product is built around, and it opened nothing:
// "procurement.*" is a prefix no door asks for, so contracts, purchase orders,
// RFQs and deliveries all answered 403 — and so did the dashboard, at login,
// because no internal role but 경영진 carried dashboard.read.
func TestTheRolesTheProductShipsWithReachTheScreensTheyName(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	// Every permission the shipped catalogue holds has to open something. This
	// reads the catalogue after the migrations rather than the seed, which is
	// where the correction lands.
	rows, err := pool.Query(ctx, `SELECT code,permissions FROM roles WHERE system=true ORDER BY code`)
	if err != nil {
		t.Fatalf("read the role catalogue: %v", err)
	}
	catalogue := map[string][]string{}
	for rows.Next() {
		var code string
		var permissions []string
		if err := rows.Scan(&code, &permissions); err != nil {
			t.Fatalf("scan a role: %v", err)
		}
		catalogue[code] = permissions
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("read the role catalogue: %v", err)
	}
	if len(catalogue) == 0 {
		t.Fatal("the role catalogue is empty; this test is no longer watching it")
	}
	for code, permissions := range catalogue {
		for _, permission := range permissions {
			if !permissionGrantsSomething(permission) {
				t.Errorf("the %s role is shipped with %q, which no door asks for: the role lists it and opens nothing",
					code, permission)
			}
		}
	}

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.221:5100"))
	send := func(cookie, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// A rule that opens nothing is refused where it is written, and the refusal
	// says what the writer could have meant.
	w := send(admin, http.MethodPost, "/api/v1/admin/roles",
		`{"code":"perm_probe_dead","name":"권한 없는 역할","dataScope":"company","permissions":["procurement.*"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a role written with a permission no door asks for returned %d, want 400: %s", w.Code, w.Body.String())
	}
	code, msg := errorCodeAndMessage(t, w)
	if code != "validation_error" || !strings.Contains(msg, "procurement.*") {
		t.Errorf("the refusal answered %q/%q", code, msg)
	}
	// So is a scope the login query cannot read: it would come back as 'own'.
	if w := send(admin, http.MethodPost, "/api/v1/admin/roles",
		`{"code":"perm_probe_scope","name":"전사 역할","dataScope":"all","permissions":["supplier.read"]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a role written with an unreadable data scope returned %d, want 400: %s", w.Code, w.Body.String())
	}
	var stored int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM roles WHERE code LIKE 'perm_probe_%'`).Scan(&stored); err != nil {
		t.Fatalf("count the probe roles: %v", err)
	}
	if stored != 0 {
		t.Errorf("%d refused roles were stored anyway", stored)
	}

	// And the shipped 구매 관리자 reaches the screens it is named for.
	const buyerEmail = "perm-buyer@vendra.test"
	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs and these would silently no-op.
		_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email=$1)`, buyerEmail)
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id IN (SELECT id FROM users WHERE email=$1)`, buyerEmail)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_id IN (SELECT id FROM users WHERE email=$1)`, buyerEmail)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email=$1`, buyerEmail)
		_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, buyerEmail)
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE code LIKE 'perm_probe_%'`)
	})
	const buyerPassword = "PermissionProbe!2026"
	hash, _ := bcrypt.GenerateFromPassword([]byte(buyerPassword), bcrypt.MinCost)
	var buyerID string
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,display_name,user_type,status,password_hash)
		VALUES($1,'권한 검증 구매 담당','internal','active',$2)
		ON CONFLICT (email) DO UPDATE SET status='active',password_hash=EXCLUDED.password_hash RETURNING id`,
		buyerEmail, string(hash)).Scan(&buyerID); err != nil {
		t.Fatalf("seed the buyer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id)
		SELECT $1,id FROM roles WHERE code='procurement_manager' ON CONFLICT DO NOTHING`, buyerID); err != nil {
		t.Fatalf("give the buyer the role: %v", err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, buyerEmail)
	buyer := sessionCookieFrom(t, postLogin(t, handler, buyerEmail, buyerPassword, "203.0.113.222:5100"))
	for _, path := range []string{
		"/api/v1/dashboard", "/api/v1/contracts", "/api/v1/purchase-requests", "/api/v1/rfq",
		"/api/v1/purchase-orders", "/api/v1/deliveries", "/api/v1/inspections", "/api/v1/suppliers",
	} {
		if w := send(buyer, http.MethodGet, path, ""); w.Code != http.StatusOK {
			t.Errorf("구매 관리자 asking for %s got %d: %s", path, w.Code, w.Body.String())
		}
	}
	// The role is still a role and not a master key: the settings and the user
	// list are behind "*", which it does not carry.
	if w := send(buyer, http.MethodGet, "/api/v1/admin/users", ""); w.Code != http.StatusForbidden {
		t.Errorf("구매 관리자 asking for the user list got %d, want 403", w.Code)
	}
}
