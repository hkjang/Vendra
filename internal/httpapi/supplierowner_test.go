package httpapi

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestASupplierWithNoOwnerCanBeHandedToOne walks the handover through the
// endpoint, which had no door for it at all.
//
// owner_id and organization_id were settled at registration and never again:
// no statement in the application wrote either column after the INSERT, so the
// register could describe a state of affairs that had stopped being true. A
// supplier whose 담당자 left the company stayed on their name, and one the
// portal registered itself arrives with no 담당자 at all — the portal's INSERT
// names neither column. Nobody could take either record on. For an account
// whose data scope is 'own' that is not a cosmetic gap: the scope filter is
// `owner_id=$me`, so the supplier is invisible to the person actually working
// with it, and the only way to put it in their hands was to delete it and
// register it again, taking every contract, order and evaluation with it.
//
// A transfer is not an ordinary edit, though. It decides who can see the
// record, so it only goes to somebody the caller can already see — and to
// exactly the people the picker offered.
func TestASupplierWithNoOwnerCanBeHandedToOne(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	one := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", strings.SplitN(strings.TrimSpace(query), "\n", 2)[0], err)
		}
		return id
	}
	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs and these would silently no-op.
		// The audit rows go before the users they name, and the users before
		// the supplier one of them points at.
		for _, q := range []string{
			`DELETE FROM audit_logs WHERE actor_id IN (SELECT id FROM users WHERE email LIKE 'handover-%')`,
			`DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'handover-%')`,
			`DELETE FROM user_roles WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'handover-%')`,
			`UPDATE suppliers SET owner_id=NULL WHERE supplier_number='SUP-HANDOVER'`,
			`DELETE FROM users WHERE email LIKE 'handover-%'`,
			`DELETE FROM audit_logs WHERE object_id IN (SELECT id::text FROM suppliers WHERE supplier_number='SUP-HANDOVER')`,
			`DELETE FROM suppliers WHERE supplier_number='SUP-HANDOVER'`,
			`DELETE FROM roles WHERE code='handover_own'`,
			`DELETE FROM organizations WHERE name='이관검증팀'`,
		} {
			_, _ = pool.Exec(ctx, q)
		}
	})

	organization := one(`INSERT INTO organizations(name,path) VALUES('이관검증팀','/') RETURNING id`)
	hash, _ := bcrypt.GenerateFromPassword([]byte("ScopeProbe!2026"), bcrypt.MinCost)
	role := one(`INSERT INTO roles(code,name,permissions,data_scope,system)
		VALUES('handover_own','handover_own','["supplier.*"]'::jsonb,'own',false) RETURNING id`)
	successor := one(`INSERT INTO users(email,display_name,user_type,organization_id,status,password_hash)
		VALUES('handover-successor@vendra.test','후임 담당자','internal',$1,'active',$2) RETURNING id`, organization, string(hash))
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, successor, role); err != nil {
		t.Fatalf("give the successor a role: %v", err)
	}
	departed := one(`INSERT INTO users(email,display_name,user_type,organization_id,status,password_hash)
		VALUES('handover-departed@vendra.test','퇴사한 담당자','internal',$1,'disabled',$2) RETURNING id`, organization, string(hash))
	// A supplier the portal registered itself: 'registration' status, and no
	// 담당자 and no 조직, because portal.go's INSERT names neither column.
	supplierID := one(`INSERT INTO suppliers(supplier_number,name,business_number,status)
		VALUES('SUP-HANDOVER','이관 검증 업체','555-55-55555','registration')
		ON CONFLICT(supplier_number) DO UPDATE SET owner_id=NULL,organization_id=NULL RETURNING id`)
	portalUser := one(`INSERT INTO users(email,display_name,user_type,supplier_id,status,password_hash)
		VALUES('handover-portal@vendra.test','포털 사용자','supplier',$1,'active',$2) RETURNING id`, supplierID, string(hash))

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts`)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.240:5000"))
	successorToken := signInToken(t, handler, "handover-successor@vendra.test")

	call := func(token, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}
	visibleToSuccessor := func() bool {
		t.Helper()
		w := call(successorToken, http.MethodGet, "/api/v1/suppliers?q=SUP-HANDOVER", "")
		if w.Code != http.StatusOK {
			t.Fatalf("the successor's register listing returned %d: %s", w.Code, w.Body.String())
		}
		var list struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode the register listing: %v", err)
		}
		for _, item := range list.Items {
			if item.ID == supplierID {
				return true
			}
		}
		return false
	}

	if visibleToSuccessor() {
		t.Fatal("the unowned supplier is already visible to an 'own' scope account; this test proves nothing")
	}

	// Who may not be handed the record, and why each one is refused where a
	// bare id check would have let it through.
	for _, tc := range []struct{ what, body, names string }{
		{"an id that is not one", `{"ownerId":"담당자님"}`, "담당자"},
		{"a user that does not exist", `{"ownerId":"00000000-0000-0000-0000-000000000009"}`, "담당자"},
		{"an account that has been deactivated", `{"ownerId":"` + departed + `"}`, "담당자"},
		{"a supplier's own portal login", `{"ownerId":"` + portalUser + `"}`, "담당자"},
		{"an organisation that does not exist", `{"organizationId":"00000000-0000-0000-0000-000000000009"}`, "조직"},
	} {
		w := call(admin, http.MethodPatch, "/api/v1/suppliers/"+supplierID, tc.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: returned %d, want 400: %s", tc.what, w.Code, w.Body.String())
			continue
		}
		code, msg := errorCodeAndMessage(t, w)
		if code != "validation_error" {
			t.Errorf("%s: answered %q, want validation_error: %s", tc.what, code, msg)
		}
		if !strings.Contains(msg, tc.names) {
			t.Errorf("%s: the rejection does not name the box to fix: %s", tc.what, msg)
		}
	}
	var owner, org *string
	if err := pool.QueryRow(ctx, `SELECT owner_id::text,organization_id::text FROM suppliers WHERE id=$1`, supplierID).Scan(&owner, &org); err != nil {
		t.Fatalf("read the supplier: %v", err)
	}
	if owner != nil || org != nil {
		t.Fatalf("a refused transfer still moved the record: owner %v organisation %v", owner, org)
	}

	// The picker offers the successor and nobody the transfer would refuse.
	w := call(admin, http.MethodGet, "/api/v1/user-candidates", "")
	if w.Code != http.StatusOK {
		t.Fatalf("the 담당자 candidates returned %d: %s", w.Code, w.Body.String())
	}
	var candidates struct {
		Items []struct {
			ID               string `json:"id"`
			OrganizationID   string `json:"organizationId"`
			OrganizationName string `json:"organizationName"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &candidates); err != nil {
		t.Fatalf("decode the candidates: %v", err)
	}
	offered := map[string]bool{}
	for _, item := range candidates.Items {
		offered[item.ID] = true
		if item.ID == successor && (item.OrganizationID != organization || item.OrganizationName != "이관검증팀") {
			t.Errorf("the candidate carries no organisation for the screen to name: %+v", item)
		}
	}
	if !offered[successor] {
		t.Errorf("the picker does not offer the person the transfer accepts: %s", w.Body.String())
	}
	if offered[departed] || offered[portalUser] {
		t.Errorf("the picker offers somebody the transfer would refuse: %s", w.Body.String())
	}

	// And the handover itself, which had nowhere to go before this.
	if w := call(admin, http.MethodPatch, "/api/v1/suppliers/"+supplierID,
		`{"ownerId":"`+successor+`","organizationId":"`+organization+`"}`); w.Code != http.StatusOK {
		t.Fatalf("the supplier could not be handed on: %d %s", w.Code, w.Body.String())
	}
	if err := pool.QueryRow(ctx, `SELECT owner_id::text,organization_id::text FROM suppliers WHERE id=$1`, supplierID).Scan(&owner, &org); err != nil {
		t.Fatalf("read the supplier: %v", err)
	}
	if owner == nil || *owner != successor || org == nil || *org != organization {
		t.Fatalf("the handover stored owner %v organisation %v", owner, org)
	}

	// A value the caller did not send is not a cleared one: an edit of the name
	// alone leaves the record on its new owner.
	if w := call(admin, http.MethodPatch, "/api/v1/suppliers/"+supplierID, `{"name":"이관 검증 업체 v2"}`); w.Code != http.StatusOK {
		t.Fatalf("an edit carrying no transfer returned %d: %s", w.Code, w.Body.String())
	}
	if err := pool.QueryRow(ctx, `SELECT owner_id::text FROM suppliers WHERE id=$1`, supplierID).Scan(&owner); err != nil {
		t.Fatalf("read the supplier: %v", err)
	}
	if owner == nil || *owner != successor {
		t.Errorf("an unrelated edit moved the record off its owner: %v", owner)
	}

	// The point of the whole thing: the person now holding the account can see
	// the supplier they are holding it for.
	if !visibleToSuccessor() {
		t.Error("the supplier is still invisible to the 담당자 it was handed to")
	}
}

// TestThePeopleTheOwnerPickerOffersAreTheOnesTheTransferAccepts binds the list
// to the check.
//
// They are two statements over the same table, and written apart they drift:
// a name the screen offered would be refused on save, or — the worse way round
// — an id nobody was offered would be accepted, which is how a record leaves
// the caller's scope for somewhere they cannot follow it. Both read the same
// predicate, and this fails if either stops.
func TestThePeopleTheOwnerPickerOffersAreTheOnesTheTransferAccepts(t *testing.T) {
	for _, function := range []string{"internalUserCandidates", "ownerCandidateInScope"} {
		body := supplierStatementBody(t, function)
		if !strings.Contains(body, "internalUserInScope(") {
			t.Errorf("%s writes out who counts as a candidate itself instead of reading "+
				"internalUserInScope; the picker and the transfer can now disagree about "+
				"who may hold a supplier", function)
		}
	}
	// The predicate is only worth sharing if it is the whole answer, so nothing
	// else in the package may select internal users to hand work to.
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(f os.FileInfo) bool {
		return !strings.HasSuffix(f.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse the package: %v", err)
	}
	for _, pkg := range pkgs {
		for name := range pkg.Files {
			source, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			for _, line := range strings.Split(string(source), "\n") {
				if !strings.Contains(line, "user_type='internal'") || strings.Contains(line, "alias + \".user_type") {
					continue
				}
				t.Errorf("%s spells out an internal-user filter of its own; use internalUserInScope so "+
					"the picker and the transfer stay one answer:\n  %s", name, strings.TrimSpace(line))
			}
		}
	}
}
