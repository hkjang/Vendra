package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Creating a supplier refuses a name close to one already registered. The check
// reads the nearest name out of a trigram index rather than scoring every
// supplier, so these cases guard the meaning while the plan changed underneath.
func TestSimilarSupplierNameIsRefused(t *testing.T) {
	app, pool := newTestApp(t)
	h := app.Handler()
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM login_attempts`,
		`DELETE FROM suppliers WHERE supplier_number LIKE 'SIM-%'`,
		`DELETE FROM roles WHERE code='similar_probe'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	t.Cleanup(func() {
		// t.Context() is already cancelled once cleanup runs.
		_, _ = pool.Exec(context.Background(), `DELETE FROM suppliers WHERE supplier_number LIKE 'SIM-%'`)
	})
	var roleID, adminID string
	if err := pool.QueryRow(ctx, `INSERT INTO roles(code,name,permissions,data_scope,system)
		VALUES('similar_probe','유사도','["*"]','company',false) RETURNING id`).Scan(&roleID); err != nil {
		t.Fatalf("role: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&adminID); err != nil {
		t.Fatalf("admin: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, adminID, roleID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	token := sessionCookieFrom(t, postLogin(t, h, testAdminEmail, testAdminPassword, "203.0.113.9:1"))

	create := func(name, tag string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"name":%q,"businessNumber":%q,"supplierNumber":%q}`, name, "SIM-"+tag, "SIM-"+tag)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/suppliers", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	if rec := create("대한정밀공업 주식회사", "001"); rec.Code != http.StatusCreated {
		t.Fatalf("the first registration was refused: %d %s", rec.Code, rec.Body.String())
	}
	for _, tc := range []struct{ name, tag string }{
		{"대한정밀공업 주식회사", "002"},
		{"대한정밀공업 주식회사 A", "003"},
	} {
		rec := create(tc.name, tc.tag)
		if rec.Code != http.StatusConflict {
			t.Errorf("%q was accepted (%d) although a close name is registered", tc.name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "similar_supplier") {
			t.Errorf("%q: body does not name the conflict: %s", tc.name, rec.Body.String())
		}
	}
	if rec := create("전혀 다른 회사명 XYZ", "004"); rec.Code != http.StatusCreated {
		t.Errorf("an unrelated name was refused: %d %s", rec.Code, rec.Body.String())
	}
}

// The lookup returns no row when nothing is registered yet, which must read as
// "no duplicate" rather than as a failure.
func TestFirstSupplierInAnEmptyRegisterIsAccepted(t *testing.T) {
	app, pool := newTestApp(t)
	h := app.Handler()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM login_attempts`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	var roleID, adminID string
	_ = pool.QueryRow(ctx, `INSERT INTO roles(code,name,permissions,data_scope,system)
		VALUES('similar_empty','유사도','["*"]','company',false)
		ON CONFLICT(code) DO UPDATE SET permissions='["*"]' RETURNING id`).Scan(&roleID)
	_ = pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&adminID)
	_, _ = pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, adminID, roleID)
	token := sessionCookieFrom(t, postLogin(t, h, testAdminEmail, testAdminPassword, "203.0.113.9:2"))

	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM suppliers WHERE deleted_at IS NULL`).Scan(&live); err != nil {
		t.Fatalf("count: %v", err)
	}
	if live > 0 {
		t.Skip("the register already holds suppliers")
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/suppliers",
		strings.NewReader(`{"name":"빈 등록부 첫 업체","businessNumber":"SIM-EMPTY","supplierNumber":"SIM-EMPTY"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM suppliers WHERE supplier_number='SIM-EMPTY'`)
	})
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
}
