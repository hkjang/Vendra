package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// unreachablePool points at a port nothing is listening on. pgxpool connects
// lazily, so construction succeeds and every query fails the way it would
// during a database outage.
func unreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), "postgres://vendra:vendra@"+addr+"/vendra?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func errorCode(t *testing.T, body string) string {
	t.Helper()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return payload.Error.Code
}

// A database outage must not be reported as an authentication failure. Doing so
// signs every user out and tells anyone signing in that their password is wrong.
func TestAuthOutageIsNotUnauthenticated(t *testing.T) {
	a := authService{db: unreachablePool(t)}
	guarded := a.middleware(true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler ran without a verified principal")
	}))

	for _, tc := range []struct {
		name    string
		prepare func(*http.Request)
	}{
		{"session cookie", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "opaque-session-token"})
		}},
		{"api key", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer vnd_opaque_key")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			tc.prepare(r)
			w := httptest.NewRecorder()
			guarded.ServeHTTP(w, r)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
			}
			if got := errorCode(t, w.Body.String()); got != "auth_unavailable" {
				t.Errorf("code = %q, want auth_unavailable", got)
			}
			if got := w.Header().Get("Retry-After"); got == "" {
				t.Error("Retry-After header is missing, so clients cannot pace their retries")
			}
		})
	}
}

// Presenting nothing is still an ordinary 401 even while the database is down:
// the request never needed a lookup to be refused.
func TestMissingCredentialsStayUnauthenticated(t *testing.T) {
	a := authService{db: unreachablePool(t)}
	w := httptest.NewRecorder()
	a.middleware(true, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("handler ran for an anonymous request")
	})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// Optional-auth routes stay reachable for anonymous callers during an outage,
// but must not silently serve a request whose credentials could not be checked.
func TestOptionalAuthDuringOutage(t *testing.T) {
	a := authService{db: unreachablePool(t)}
	reached := false
	guarded := a.middleware(false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	w := httptest.NewRecorder()
	guarded.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if !reached {
		t.Error("anonymous request was blocked on an optional-auth route")
	}

	reached = false
	r := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "opaque-session-token"})
	w = httptest.NewRecorder()
	guarded.ServeHTTP(w, r)
	if reached {
		t.Error("handler ran even though the presented session could not be verified")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// Sign-in must say "try again" rather than "wrong password" when the lookup
// itself failed.
func TestLoginDuringOutage(t *testing.T) {
	a := authService{db: unreachablePool(t)}
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@vendra.io","password":"correct-horse"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.login(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if got := errorCode(t, w.Body.String()); got != "auth_unavailable" {
		t.Errorf("code = %q, want auth_unavailable", got)
	}
}
