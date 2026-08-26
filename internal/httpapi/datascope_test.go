package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type scopeWorld struct {
	handler http.Handler
	// pool lets a test seed rows the shared fixture does not carry.
	pool *pgxpool.Pool
	// Records belonging to the caller's own department.
	mySupplier, myContract, myPO, myRFQ, myDocument, myRisk, myEvaluation string
	// The same set one department over.
	theirSupplier, theirContract, theirPO, theirRFQ string
	theirDocument, theirRisk, theirEvaluation       string
	theirScreening, theirContact                    string
	deptToken, ownToken                             string
	deptUser                                        string
}

func newScopeWorld(t *testing.T) *scopeWorld {
	t.Helper()
	app, pool := newTestApp(t)
	ctx := context.Background()
	h := app.Handler()
	wipe(t, pool)
	root := t.TempDir()
	if _, err := pool.Exec(ctx, `UPDATE settings SET value=jsonb_build_object('driver','filesystem','path',$1::text) WHERE key='storage'`, root); err != nil {
		t.Fatalf("configure storage: %v", err)
	}

	one := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", strings.SplitN(strings.TrimSpace(query), "\n", 2)[0], err)
		}
		return id
	}

	mine := one(`INSERT INTO organizations(name,path) VALUES('구매1팀','/') RETURNING id`)
	theirs := one(`INSERT INTO organizations(name,path) VALUES('구매2팀','/') RETURNING id`)

	w := &scopeWorld{handler: h, pool: pool}
	hash, _ := bcrypt.GenerateFromPassword([]byte("ScopeProbe!2026"), bcrypt.MinCost)
	role := func(code, scope string) string {
		return one(`INSERT INTO roles(code,name,permissions,data_scope,system)
			VALUES($1,$1,'["supplier.*","contract.*","rfq.*","purchase_order.*","document.*","risk.*","evaluation.*","workflow.*","spend.*","analytics.read","ai.use"]'::jsonb,$2,false) RETURNING id`, code, scope)
	}
	user := func(email, orgID, roleID string) string {
		id := one(`INSERT INTO users(email,display_name,user_type,organization_id,status,password_hash)
			VALUES($1,$1,'internal',$2,'active',$3) RETURNING id`, email, orgID, string(hash))
		if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, id, roleID); err != nil {
			t.Fatalf("user_roles: %v", err)
		}
		return id
	}
	w.deptUser = user("scope-dept@vendra.test", mine, role("scope_department", "department"))
	user("scope-own@vendra.test", mine, role("scope_own", "own"))

	supplier := func(tag, orgID, owner string) string {
		return one(`INSERT INTO suppliers(supplier_number,business_number,name,status,organization_id,owner_id)
			VALUES($1,$1,$1,'active',$2,NULLIF($3,'')::uuid) RETURNING id`, "SC-"+tag, orgID, owner)
	}
	object := func(typ, num, supplierID, orgID, owner string) string {
		return one(`INSERT INTO business_objects(object_type,number,supplier_id,organization_id,owner_id,title,status,amount)
			VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$2,'active',99000000) RETURNING id`, typ, num, supplierID, orgID, owner)
	}
	// The stored file has to exist inside the configured root, or retrieval
	// fails on the path rather than on the scope being tested.
	document := func(tag, name, supplierID, objectID string) string {
		path := filepath.Join(root, tag)
		if err := os.WriteFile(path, []byte("내용"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return one(`INSERT INTO documents(supplier_id,object_type,object_id,document_type,name,version,storage_path,content_type,size,checksum,status)
			VALUES($1,'contract',$2,'contract',$3,1,$4,'text/plain',6,'abc','active') RETURNING id`, supplierID, objectID, name, path)
	}

	// The caller's department owns these, with the department user as owner so
	// the `own` scope has something to be excluded from.
	w.mySupplier = supplier("MINE", mine, w.deptUser)
	w.myContract = object("contract", "SC-MY-CT", w.mySupplier, mine, w.deptUser)
	w.myPO = object("purchase_order", "SC-MY-PO", w.mySupplier, mine, w.deptUser)
	w.myRFQ = object("rfq", "SC-MY-RFQ", w.mySupplier, mine, w.deptUser)
	w.myDocument = document("mine", "내 문서.txt", w.mySupplier, w.myContract)
	w.myRisk = one(`INSERT INTO risks(supplier_id,risk_type,probability,impact,severity,status) VALUES($1,'재무',3,3,'HIGH','open') RETURNING id`, w.mySupplier)
	w.myEvaluation = one(`INSERT INTO evaluations(supplier_id,evaluation_type,status) VALUES($1,'정기','draft') RETURNING id`, w.mySupplier)

	w.theirSupplier = supplier("THEIRS", theirs, "")
	w.theirContract = object("contract", "SC-CT", w.theirSupplier, theirs, "")
	w.theirPO = object("purchase_order", "SC-PO", w.theirSupplier, theirs, "")
	w.theirRFQ = object("rfq", "SC-RFQ", w.theirSupplier, theirs, "")
	w.theirDocument = document("theirs", "남의 문서.txt", w.theirSupplier, w.theirContract)
	w.theirRisk = one(`INSERT INTO risks(supplier_id,risk_type,probability,impact,severity,status) VALUES($1,'재무',3,3,'HIGH','open') RETURNING id`, w.theirSupplier)
	w.theirEvaluation = one(`INSERT INTO evaluations(supplier_id,evaluation_type,status) VALUES($1,'정기','draft') RETURNING id`, w.theirSupplier)
	template := one(`INSERT INTO screening_templates(name,active,items,result_rules,required_document_types)
		VALUES('기본',true,'[]'::jsonb,'{}'::jsonb,'[]'::jsonb) RETURNING id`)
	w.theirScreening = one(`INSERT INTO supplier_screenings(supplier_id,template_id) VALUES($1,$2) RETURNING id`, w.theirSupplier, template)
	w.theirContact = one(`INSERT INTO supplier_contacts(supplier_id,name,email) VALUES($1,'남의 담당','x@y.z') RETURNING id`, w.theirSupplier)

	if _, err := pool.Exec(ctx, `DELETE FROM login_attempts`); err != nil {
		t.Fatalf("reset attempts: %v", err)
	}
	w.deptToken = signInToken(t, h, "scope-dept@vendra.test")
	w.ownToken = signInToken(t, h, "scope-own@vendra.test")
	return w
}

// wipe removes only what this fixture creates. Emptying the tables outright
// collided with every other fixture's rows, and the two foreign keys between
// users and suppliers point at each other, so the order below releases one
// side before removing either.
func wipe(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, q := range []string{
		`UPDATE suppliers SET owner_id=NULL WHERE supplier_number LIKE 'SC-%'`,
		`DELETE FROM workflow_actions WHERE instance_id IN (SELECT i.id FROM workflow_instances i JOIN business_objects o ON o.id=i.object_id WHERE o.number LIKE 'SC-%')`,
		`DELETE FROM workflow_instances WHERE object_id IN (SELECT id FROM business_objects WHERE number LIKE 'SC-%')`,
		`DELETE FROM document_signatures WHERE document_id IN (SELECT id FROM documents WHERE supplier_id IN (SELECT id FROM suppliers WHERE supplier_number LIKE 'SC-%'))`,
		`DELETE FROM documents WHERE supplier_id IN (SELECT id FROM suppliers WHERE supplier_number LIKE 'SC-%')`,
		`DELETE FROM sourcing_selections WHERE sourcing_id IN (SELECT id FROM business_objects WHERE number LIKE 'SC-%')`,
		`DELETE FROM sourcing_questions WHERE sourcing_id IN (SELECT id FROM business_objects WHERE number LIKE 'SC-%')`,
		`DELETE FROM sourcing_responses WHERE sourcing_id IN (SELECT id FROM business_objects WHERE number LIKE 'SC-%')`,
		`DELETE FROM sourcing_participants WHERE sourcing_id IN (SELECT id FROM business_objects WHERE number LIKE 'SC-%')`,
		`DELETE FROM business_objects WHERE number LIKE 'SC-%'`,
		`DELETE FROM supplier_screenings WHERE supplier_id IN (SELECT id FROM suppliers WHERE supplier_number LIKE 'SC-%')`,
		`DELETE FROM risks WHERE supplier_id IN (SELECT id FROM suppliers WHERE supplier_number LIKE 'SC-%')`,
		`DELETE FROM evaluations WHERE supplier_id IN (SELECT id FROM suppliers WHERE supplier_number LIKE 'SC-%')`,
		`DELETE FROM supplier_contacts WHERE supplier_id IN (SELECT id FROM suppliers WHERE supplier_number LIKE 'SC-%')`,
		`DELETE FROM spend_transactions WHERE supplier_id IN (SELECT id FROM suppliers WHERE supplier_number LIKE 'SC-%')`,
		`DELETE FROM access_grants WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'scope-%')`,
		`DELETE FROM audit_logs WHERE actor_id IN (SELECT id FROM users WHERE email LIKE 'scope-%')`,
		`DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'scope-%')`,
		`DELETE FROM user_roles WHERE user_id IN (SELECT id FROM users WHERE email LIKE 'scope-%')`,
		`DELETE FROM users WHERE email LIKE 'scope-%'`,
		`DELETE FROM suppliers WHERE supplier_number LIKE 'SC-%'`,
		`DELETE FROM roles WHERE code LIKE 'scope_%'`,
		`DELETE FROM organizations WHERE name IN ('구매1팀','구매2팀')`,
		`DELETE FROM login_attempts`,
	} {
		if _, err := pool.Exec(context.Background(), q); err != nil {
			t.Fatalf("wipe: %v\n  %s", err, q)
		}
	}
}

func signInToken(t *testing.T, h http.Handler, email string) string {
	t.Helper()
	w := postLogin(t, h, email, "ScopeProbe!2026", "203.0.113.20:1000")
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in %s: %d %s", email, w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatalf("sign-in %s issued no cookie", email)
	return ""
}

func (w *scopeWorld) call(t *testing.T, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

// Every endpoint that takes a record id must refuse a record outside the
// caller's department, whether it reads, writes or acts on it.
func TestDepartmentScopeRefusesAnotherDepartmentsRecords(t *testing.T) {
	w := newScopeWorld(t)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/suppliers/" + w.theirSupplier, ""},
		{"PATCH", "/api/v1/suppliers/" + w.theirSupplier, `{"name":"침입"}`},
		{"GET", "/api/v1/suppliers/" + w.theirSupplier + "/activity", ""},
		{"GET", "/api/v1/suppliers/" + w.theirSupplier + "/contacts", ""},
		{"POST", "/api/v1/suppliers/" + w.theirSupplier + "/contacts", `{"name":"침입","email":"a@b.c"}`},
		{"GET", "/api/v1/suppliers/" + w.theirSupplier + "/objects", ""},
		{"GET", "/api/v1/suppliers/" + w.theirSupplier + "/risks", ""},
		{"POST", "/api/v1/suppliers/" + w.theirSupplier + "/risks", `{"riskType":"침입","probability":1,"impact":1}`},
		{"GET", "/api/v1/suppliers/" + w.theirSupplier + "/evaluations", ""},
		{"POST", "/api/v1/suppliers/" + w.theirSupplier + "/evaluations", `{"scores":{}}`},
		{"GET", "/api/v1/suppliers/" + w.theirSupplier + "/screenings", ""},
		{"POST", "/api/v1/suppliers/" + w.theirSupplier + "/screenings", `{}`},
		{"PATCH", "/api/v1/screenings/" + w.theirScreening, `{"responses":{}}`},
		{"GET", "/api/v1/contracts/" + w.theirContract, ""},
		{"PATCH", "/api/v1/contracts/" + w.theirContract, `{"title":"침입"}`},
		{"POST", "/api/v1/contracts/" + w.theirContract + "/submit", `{}`},
		{"GET", "/api/v1/purchase-orders/" + w.theirPO, ""},
		{"PATCH", "/api/v1/purchase-orders/" + w.theirPO, `{"title":"침입"}`},
		{"GET", "/api/v1/documents/" + w.theirDocument + "/download", ""},
		{"GET", "/api/v1/documents/" + w.theirDocument + "/preview", ""},
		{"GET", "/api/v1/documents/" + w.theirDocument + "/signatures", ""},
		{"POST", "/api/v1/documents/" + w.theirDocument + "/signatures", `{"signatureType":"approval"}`},
		{"GET", "/api/v1/sourcing/" + w.theirRFQ + "/participants", ""},
		{"POST", "/api/v1/sourcing/" + w.theirRFQ + "/participants", `{"supplierIds":["` + w.theirSupplier + `"]}`},
		{"GET", "/api/v1/sourcing/" + w.theirRFQ + "/comparison", ""},
		{"GET", "/api/v1/sourcing/" + w.theirRFQ + "/questions", ""},
		{"POST", "/api/v1/sourcing/" + w.theirRFQ + "/questions", `{"question":"침입"}`},
		{"GET", "/api/v1/sourcing/" + w.theirRFQ + "/committee", ""},
		{"POST", "/api/v1/sourcing/" + w.theirRFQ + "/committee", `{"userIds":["` + w.deptUser + `"]}`},
		{"POST", "/api/v1/ai/contracts/" + w.theirContract + "/analyze", `{}`},
	} {
		t.Run(tc.method+" "+strings.TrimPrefix(tc.path, "/api/v1/"), func(t *testing.T) {
			rec := w.call(t, w.deptToken, tc.method, tc.path, tc.body)
			if rec.Code < 400 {
				t.Errorf("status = %d, want 4xx — this record belongs to another department\n  body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// Refusing the direct route is only half of it: the list endpoints must not
// carry the other department's rows either. Each case names a record the caller
// does own, so an endpoint that happens to be empty cannot pass by accident.
func TestListsCarryOwnDepartmentOnly(t *testing.T) {
	w := newScopeWorld(t)
	theirs := map[string]string{
		"supplier": w.theirSupplier, "contract": w.theirContract, "purchase order": w.theirPO,
		"rfq": w.theirRFQ, "document": w.theirDocument, "risk": w.theirRisk,
		"evaluation": w.theirEvaluation, "screening": w.theirScreening, "contact": w.theirContact,
	}
	for _, tc := range []struct{ path, mustContain string }{
		{"/api/v1/suppliers", w.mySupplier},
		{"/api/v1/contracts", w.myContract},
		{"/api/v1/purchase-orders", w.myPO},
		{"/api/v1/rfq", w.myRFQ},
		{"/api/v1/documents", w.myDocument},
		{"/api/v1/risks", w.myRisk},
		{"/api/v1/evaluations", w.myEvaluation},
		{"/api/v1/spend", w.mySupplier},
		{"/api/v1/search?q=SC-", w.mySupplier},
		{"/api/v1/supplier-network", w.mySupplier},
	} {
		t.Run(strings.TrimPrefix(tc.path, "/api/v1/"), func(t *testing.T) {
			rec := w.call(t, w.deptToken, "GET", tc.path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.mustContain) {
				t.Fatalf("the caller's own record is missing, so this proves nothing about isolation\n  body: %s", body)
			}
			for label, id := range theirs {
				if strings.Contains(body, id) {
					t.Errorf("another department's %s appears in the list", label)
				}
			}
		})
	}
}

// `own` is stricter than `department`: a colleague's record in the same
// department is still out of reach.
func TestOwnScopeRefusesAColleaguesRecords(t *testing.T) {
	w := newScopeWorld(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/suppliers/" + w.mySupplier},
		{"GET", "/api/v1/contracts/" + w.myContract},
		{"GET", "/api/v1/documents/" + w.myDocument + "/download"},
	} {
		t.Run(tc.method+" "+strings.TrimPrefix(tc.path, "/api/v1/"), func(t *testing.T) {
			// The department user owns these; the own-scoped user does not.
			if rec := w.call(t, w.deptToken, tc.method, tc.path, ""); rec.Code >= 400 {
				t.Fatalf("the owner was refused their own record (%d), so the probe is meaningless", rec.Code)
			}
			rec := w.call(t, w.ownToken, tc.method, tc.path, "")
			if rec.Code < 400 {
				t.Errorf("status = %d, want 4xx — this record belongs to a colleague", rec.Code)
			}
		})
	}
}
