package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type grantWorld struct {
	*scopeWorld
	pool     *pgxpool.Pool
	theirOrg string
}

func newGrantWorld(t *testing.T) *grantWorld {
	t.Helper()
	w := newScopeWorld(t)
	_, pool := newTestApp(t)
	g := &grantWorld{scopeWorld: w, pool: pool}
	if err := pool.QueryRow(context.Background(), `SELECT organization_id::text FROM suppliers WHERE id=$1`, w.theirSupplier).Scan(&g.theirOrg); err != nil {
		t.Fatalf("read organisation: %v", err)
	}
	return g
}

// delegate replaces the caller's delegations and returns a fresh session,
// because grants are resolved when the session is.
func (g *grantWorld) delegate(t *testing.T, permission, resourceType, resourceID string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := g.pool.Exec(ctx, `DELETE FROM access_grants WHERE user_id=$1`, g.deptUser); err != nil {
		t.Fatalf("clear grants: %v", err)
	}
	if permission != "" {
		if _, err := g.pool.Exec(ctx, `INSERT INTO access_grants(user_id,permission,resource_type,resource_id,conditions,valid_from)
			VALUES($1,$2,NULLIF($3,''),NULLIF($4,'')::uuid,'{}'::jsonb,now()-interval '1 hour')`,
			g.deptUser, permission, resourceType, resourceID); err != nil {
			t.Fatalf("grant: %v", err)
		}
	}
	if _, err := g.pool.Exec(ctx, `DELETE FROM login_attempts`); err != nil {
		t.Fatalf("reset attempts: %v", err)
	}
	return signInToken(t, g.handler, "scope-dept@vendra.test")
}

// A delegation grants a permission. It does not widen the data scope that
// permission runs inside. Naming only a resource *type* used to raise the same
// bypass flag as naming a record, which handed over the whole domain: every
// contract, every supplier, every document in the company.
func TestTypeOnlyDelegationDoesNotWidenDataScope(t *testing.T) {
	g := newGrantWorld(t)

	for _, tc := range []struct {
		name       string
		permission string
		resource   string
		probe      func(t *testing.T, token string) *httptest.ResponseRecorder
	}{
		{"read another department's contract", "contract.read", "contract", func(t *testing.T, token string) *httptest.ResponseRecorder {
			return g.call(t, token, "GET", "/api/v1/contracts/"+g.theirContract, "")
		}},
		{"read another department's supplier", "supplier.read", "supplier", func(t *testing.T, token string) *httptest.ResponseRecorder {
			return g.call(t, token, "GET", "/api/v1/suppliers/"+g.theirSupplier, "")
		}},
		{"download another department's document", "document.read", "document", func(t *testing.T, token string) *httptest.ResponseRecorder {
			return g.call(t, token, "GET", "/api/v1/documents/"+g.theirDocument+"/download", "")
		}},
		{"file an order into another organisation", "purchase_order.create", "purchase_order", func(t *testing.T, token string) *httptest.ResponseRecorder {
			return g.call(t, token, "POST", "/api/v1/purchase-orders",
				fmt.Sprintf(`{"number":"GRANT-ORG","title":"침입","organizationId":%q}`, g.theirOrg))
		}},
		{"file an order against another department's supplier", "purchase_order.create", "purchase_order", func(t *testing.T, token string) *httptest.ResponseRecorder {
			return g.call(t, token, "POST", "/api/v1/purchase-orders",
				fmt.Sprintf(`{"number":"GRANT-SUP","title":"침입","supplierId":%q}`, g.theirSupplier))
		}},
		{"attach a document to another department's contract", "document.create", "document", func(t *testing.T, token string) *httptest.ResponseRecorder {
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			fw, err := mw.CreateFormFile("file", "심은문서.txt")
			if err != nil {
				t.Fatalf("form file: %v", err)
			}
			if _, err := fw.Write([]byte("내용")); err != nil {
				t.Fatalf("write: %v", err)
			}
			for k, v := range map[string]string{"documentType": "quotation", "objectType": "contract", "objectId": g.theirContract} {
				if err := mw.WriteField(k, v); err != nil {
					t.Fatalf("write field: %v", err)
				}
			}
			if err := mw.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/v1/documents/upload", &body)
			r.Header.Set("Content-Type", mw.FormDataContentType())
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			rec := httptest.NewRecorder()
			g.handler.ServeHTTP(rec, r)
			return rec
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Without the delegation this is refused; the delegation must not
			// change that, or the test proves nothing.
			if rec := tc.probe(t, g.delegate(t, "", "", "")); rec.Code < 400 {
				t.Fatalf("refused nothing even without a delegation (%d): %s", rec.Code, rec.Body.String())
			}
			rec := tc.probe(t, g.delegate(t, tc.permission, tc.resource, ""))
			if rec.Code < 400 {
				t.Errorf("status = %d, want 4xx — delegating %q over %q opened the whole domain\n  body: %s",
					rec.Code, tc.permission, tc.resource, rec.Body.String())
			}
		})
	}
}

// The delegation that names a record still reaches that record, and only it.
func TestRecordDelegationReachesThatRecordOnly(t *testing.T) {
	g := newGrantWorld(t)
	token := g.delegate(t, "contract.read", "contract", g.theirContract)

	if rec := g.call(t, token, "GET", "/api/v1/contracts/"+g.theirContract, ""); rec.Code != http.StatusOK {
		t.Errorf("the delegated contract is unreachable (%d): %s", rec.Code, rec.Body.String())
	}
	if rec := g.call(t, token, "GET", "/api/v1/purchase-orders/"+g.theirPO, ""); rec.Code < 400 {
		t.Errorf("a record the delegation did not name returned %d, want 4xx", rec.Code)
	}
}

// An expired delegation is no delegation.
func TestExpiredDelegationGrantsNothing(t *testing.T) {
	g := newGrantWorld(t)
	ctx := context.Background()
	if _, err := g.pool.Exec(ctx, `DELETE FROM access_grants WHERE user_id=$1`, g.deptUser); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := g.pool.Exec(ctx, `INSERT INTO access_grants(user_id,permission,resource_type,resource_id,conditions,valid_from,valid_until)
		VALUES($1,'contract.read','contract',$2,'{}'::jsonb,now()-interval '2 hours',now()-interval '1 hour')`, g.deptUser, g.theirContract); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := g.pool.Exec(ctx, `DELETE FROM login_attempts`); err != nil {
		t.Fatalf("reset attempts: %v", err)
	}
	token := signInToken(t, g.handler, "scope-dept@vendra.test")
	if rec := g.call(t, token, "GET", "/api/v1/contracts/"+g.theirContract, ""); rec.Code < 400 {
		t.Errorf("an expired delegation still reached the record (%d)", rec.Code)
	}
}
