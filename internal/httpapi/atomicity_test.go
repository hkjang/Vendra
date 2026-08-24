package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A handler that writes several tables must not report success when only some
// of the writes landed. A trigger fails the follow-up update while leaving the
// first insert working, which is what a constraint violation or a lock timeout
// looks like in production.
func TestScreeningCreationIsAllOrNothing(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := t.Context()
	h := app.Handler()

	_, _ = pool.Exec(context.Background(), `DELETE FROM login_attempts`)
	w := postLogin(t, h, testAdminEmail, testAdminPassword, "203.0.113.9:1000")
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var token string
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			token = c.Value
		}
	}
	call := func(method, path, body string) (int, string) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}

	var supplierID string
	if err := pool.QueryRow(ctx, `INSERT INTO suppliers(supplier_number,business_number,name,status)
		VALUES('SUP-ATOMIC','000-00-99999','원자성상사','active') RETURNING id`).Scan(&supplierID); err != nil {
		t.Fatalf("seed supplier: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM supplier_screenings WHERE supplier_id=$1`, supplierID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM suppliers WHERE id=$1`, supplierID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO screening_templates(name,active,items,result_rules,required_document_types)
		VALUES('기본','true','[]'::jsonb,'{}'::jsonb,'[]'::jsonb)`); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	if _, err := pool.Exec(ctx, `CREATE OR REPLACE FUNCTION vendra_test_block() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'blocked by test'; END $$`); err != nil {
		t.Skipf("cannot create the blocking function: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER vendra_test_block_suppliers BEFORE UPDATE ON suppliers
		FOR EACH ROW EXECUTE FUNCTION vendra_test_block()`); err != nil {
		t.Skipf("cannot create the blocking trigger: %v", err)
	}
	restore := func() {
		// t.Context() is already cancelled once cleanup runs.
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS vendra_test_block_suppliers ON suppliers`)
	}
	t.Cleanup(func() {
		restore()
		_, _ = pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS vendra_test_block()`)
	})
	if _, err := pool.Exec(ctx, `UPDATE suppliers SET updated_at=now() WHERE id=$1`, supplierID); err == nil {
		restore()
		t.Skip("the blocking trigger did not take effect")
	}

	code, body := call("POST", "/api/v1/suppliers/"+supplierID+"/screenings", `{}`)
	if code < 400 {
		t.Errorf("screening creation reported %d while the supplier could not be moved into screening\n  body: %s", code, body)
	}

	restore()
	var screenings int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM supplier_screenings WHERE supplier_id=$1`, supplierID).Scan(&screenings); err != nil {
		t.Fatalf("count screenings: %v", err)
	}
	if screenings != 0 {
		t.Errorf("%d screening row(s) survived a failed creation; the supplier is still not in screening", screenings)
	}
	var status string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM suppliers WHERE id=$1`, supplierID).Scan(&status)
	fmt.Printf("응답 %d, 남은 심사 %d건, 공급업체 상태 %q\n", code, screenings, status)
}
