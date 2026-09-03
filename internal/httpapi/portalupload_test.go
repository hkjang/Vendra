package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type portalFixture struct {
	handler   http.Handler
	token     string
	supplierA string
	supplierB string
	objectOfA string
	objectOfB string
}

// newPortalFixture builds two suppliers, a portal account belonging to the
// first, and one business object under each.
func newPortalFixture(t *testing.T) (*portalFixture, *pgxpool.Pool) {
	t.Helper()
	app, pool := newTestApp(t)
	ctx := context.Background()
	h := app.Handler()

	suffix := fmt.Sprintf("%d", len(t.Name()))
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM documents WHERE name LIKE 'portal-test-%'`)
		// The audit rows go before the account that wrote them. Signing in is
		// itself an audited act, so audit_logs.actor_id holds this user by the
		// time any test body runs, and the delete below fails on that reference
		// — silently, because these are best-effort. The account then survives
		// holding a reference of its own to the supplier, so the supplier
		// survives too, and the next run of whichever test shares this suffix
		// fails seeding on a duplicate supplier number rather than on anything
		// it did.
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE actor_id IN(SELECT id FROM users WHERE email=$1)`, "portal"+suffix+"@vendra.test")
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE email=$1`, "portal"+suffix+"@vendra.test")
		_, _ = pool.Exec(context.Background(), `DELETE FROM roles WHERE code=$1`, "portal_probe_"+suffix)
		_, _ = pool.Exec(context.Background(), `DELETE FROM business_objects WHERE number LIKE 'PT-'||$1||'-%'`, suffix)
		_, _ = pool.Exec(context.Background(), `DELETE FROM suppliers WHERE supplier_number LIKE 'PT-'||$1||'-%'`, suffix)
	}
	cleanup()
	t.Cleanup(cleanup)

	if _, err := pool.Exec(ctx, `UPDATE settings SET value=jsonb_build_object('driver','filesystem','path',$1::text) WHERE key='storage'`, t.TempDir()); err != nil {
		t.Fatalf("configure storage: %v", err)
	}

	supplier := func(tag, name string) string {
		var id string
		num := "PT-" + suffix + "-" + tag
		if err := pool.QueryRow(ctx, `INSERT INTO suppliers(supplier_number,business_number,name,status)
			VALUES($1,$2,$3,'active') RETURNING id`, num, num, name).Scan(&id); err != nil {
			t.Fatalf("supplier %s: %v", name, err)
		}
		return id
	}
	object := func(tag, supplierID string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO business_objects(object_type,number,supplier_id,title,status)
			VALUES('contract','PT-'||$1||'-'||$2,$3,'계약','active') RETURNING id`, suffix, tag, supplierID).Scan(&id); err != nil {
			t.Fatalf("object %s: %v", tag, err)
		}
		return id
	}
	f := &portalFixture{handler: h}
	f.supplierA = supplier("A", "가나상사")
	f.supplierB = supplier("B", "다라상사")
	f.objectOfA = object("OA", f.supplierA)
	f.objectOfB = object("OB", f.supplierB)

	hash, err := bcrypt.GenerateFromPassword([]byte("PortalProbe!2026"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var roleID, userID string
	if err := pool.QueryRow(ctx, `INSERT INTO roles(code,name,permissions,data_scope,system)
		VALUES($1,'포털','["portal.*"]'::jsonb,'own',false) RETURNING id`, "portal_probe_"+suffix).Scan(&roleID); err != nil {
		t.Fatalf("role: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,display_name,user_type,supplier_id,status,password_hash)
		VALUES($1,'A담당','supplier',$2,'active',$3) RETURNING id`, "portal"+suffix+"@vendra.test", f.supplierA, string(hash)).Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, userID, roleID); err != nil {
		t.Fatalf("user_role: %v", err)
	}

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts`)
	w := postLogin(t, h, "portal"+suffix+"@vendra.test", "PortalProbe!2026", "203.0.113.11:1000")
	if w.Code != http.StatusOK {
		t.Fatalf("portal sign-in: %d %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			f.token = c.Value
		}
	}
	if f.token == "" {
		t.Fatal("portal sign-in issued no session cookie")
	}
	return f, pool
}

func (f *portalFixture) upload(t *testing.T, name string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := fw.Write([]byte("내용")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/portal/documents/upload", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, r)
	return rec
}

// A portal account could file a document against any business object, because
// the scope check was skipped for exactly the untrusted user type. The document
// then appeared on another supplier's contract, where the buyer reviews it.
func TestPortalUploadCannotTargetAnotherSuppliersObject(t *testing.T) {
	f, pool := newPortalFixture(t)

	rec := f.upload(t, "portal-test-planted.txt", map[string]string{
		"documentType": "quotation",
		"objectType":   "contract",
		"objectId":     f.objectOfB,
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("upload against another supplier's contract = %d, want 403\n  body: %s", rec.Code, rec.Body.String())
	}
	var planted int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM documents WHERE object_id=$1`, f.objectOfB).Scan(&planted); err != nil {
		t.Fatalf("count: %v", err)
	}
	if planted != 0 {
		t.Errorf("%d document(s) were filed against another supplier's contract", planted)
	}
}

// The portal forced the supplier id by writing into r.MultipartForm, which
// r.FormValue never reads: every portal upload was stored with no supplier and
// vanished from the uploader's own list.
func TestPortalUploadIsFiledUnderTheUploader(t *testing.T) {
	f, pool := newPortalFixture(t)

	rec := f.upload(t, "portal-test-mine.txt", map[string]string{"documentType": "certificate"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("own upload = %d, want 201\n  body: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var supplierID *string
	if err := pool.QueryRow(context.Background(), `SELECT supplier_id::text FROM documents WHERE id=$1`, created.ID).Scan(&supplierID); err != nil {
		t.Fatalf("read document: %v", err)
	}
	if supplierID == nil || *supplierID != f.supplierA {
		t.Errorf("document supplier = %v, want %s — an untagged upload is invisible to its owner", supplierID, f.supplierA)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/portal/documents", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
	list := httptest.NewRecorder()
	f.handler.ServeHTTP(list, r)
	if !strings.Contains(list.Body.String(), "portal-test-mine.txt") {
		t.Errorf("the uploader cannot see their own document\n  list: %s", list.Body.String())
	}
}

// Filing against one's own record stays allowed.
func TestPortalUploadAcceptsOwnObject(t *testing.T) {
	f, _ := newPortalFixture(t)
	rec := f.upload(t, "portal-test-own-object.txt", map[string]string{
		"documentType": "quotation",
		"objectType":   "contract",
		"objectId":     f.objectOfA,
	})
	if rec.Code != http.StatusCreated {
		t.Errorf("upload against own contract = %d, want 201\n  body: %s", rec.Code, rec.Body.String())
	}
}

// TestAPortalWriteNamesAMalformedRecordID is the record-id sweep at the door
// outsiders come through.
//
// A supplier files a delivery against the order it fulfils and an invoice
// against the delivery, and attaches the paperwork to the same record — three
// writes that each carry an id the portal user copied off a screen or an email.
// A malformed one used to fail the insert as "업무를 등록하지 못했습니다", or,
// on the upload, come back as 403 "문서 대상 업무가 데이터 접근 범위를
// 벗어났습니다": the supplier told they may not reach their own buyer's record,
// after the file had already been streamed and hashed, so every retry spent the
// upload again.
func TestAPortalWriteNamesAMalformedRecordID(t *testing.T) {
	f, _ := newPortalFixture(t)

	rec := f.upload(t, "portal-test-badid.txt", map[string]string{
		"documentType": "quotation",
		"objectType":   "contract",
		"objectId":     "PO-2026-0001",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an upload filed against an order number = %d, want 400\n  body: %s", rec.Code, rec.Body.String())
	}
	if code, msg := errorCodeAndMessage(t, rec); code != "validation_error" || !strings.Contains(msg, "업무 ID") {
		t.Errorf("the rejected upload answered %q/%q, want validation_error naming 업무 ID", code, msg)
	}

	body := `{"title":"납품 등록","parentId":"PO-2026-0001"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/portal/deliveries", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("a delivery filed against an order number = %d, want 400\n  body: %s", w.Code, w.Body.String())
	}
	if code, msg := errorCodeAndMessage(t, w); code != "validation_error" || !strings.Contains(msg, "상위 업무 ID") {
		t.Errorf("the rejected delivery answered %q/%q, want validation_error naming 상위 업무 ID", code, msg)
	}
}

// TestAPortalWriteNamesAMalformedEmail is the address sweep at the door
// outsiders come through.
//
// The supplier maintains their own contact block and their own people, and both
// are how the buyer reaches them: the representative address on the register and
// the contact an RFQ, a delivery notice and a verification link are sent to.
// Nothing looked at either, so a name typed into the address box saved as one,
// and the first anyone knew of it was mail that never arrived.
func TestAPortalWriteNamesAMalformedEmail(t *testing.T) {
	f, pool := newPortalFixture(t)
	post := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	patch := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.token})
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}

	w := patch("/api/v1/portal/profile", `{"email":"영업부"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("a profile reachable at a department = %d, want 400\n  body: %s", w.Code, w.Body.String())
	}
	if code, msg := errorCodeAndMessage(t, w); code != "validation_error" || !strings.Contains(msg, "이메일") {
		t.Errorf("the rejected profile answered %q/%q, want validation_error naming 이메일", code, msg)
	}

	w = post("/api/v1/portal/contacts", `{"name":"이영업","email":"이영업"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("a contact reachable at a name = %d, want 400\n  body: %s", w.Code, w.Body.String())
	}
	if code, msg := errorCodeAndMessage(t, w); code != "validation_error" || !strings.Contains(msg, "담당자 이메일") {
		t.Errorf("the rejected contact answered %q/%q, want validation_error naming 담당자 이메일", code, msg)
	}

	var contacts int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM supplier_contacts WHERE supplier_id=$1`, f.supplierA).Scan(&contacts); err != nil {
		t.Fatalf("count contacts: %v", err)
	}
	if contacts != 0 {
		t.Errorf("a rejected write left %d contacts behind", contacts)
	}

	// A real address, pasted as it came, is stored as sign-in and the contact
	// list read it — not as the cell it was copied out of.
	if w := post("/api/v1/portal/contacts", `{"name":"이영업","email":" Lee.YS@Acme.CO.KR "}`); w.Code != http.StatusCreated {
		t.Fatalf("a pasted contact address was refused: %d %s", w.Code, w.Body.String())
	}
	var stored string
	if err := pool.QueryRow(context.Background(), `SELECT email FROM supplier_contacts WHERE supplier_id=$1`, f.supplierA).Scan(&stored); err != nil {
		t.Fatalf("read the contact: %v", err)
	}
	if stored != "lee.ys@acme.co.kr" {
		t.Errorf("the contact was stored as %q", stored)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM supplier_contacts WHERE supplier_id=$1`, f.supplierA)
	})
}
