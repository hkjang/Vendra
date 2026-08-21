package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hkjang/Vendra/internal/config"
	"github.com/hkjang/Vendra/internal/db"
)

// newTestApp brings up the real application against the PostgreSQL instance in
// VENDRA_TEST_DSN. Without that variable the test is skipped, so the default
// `go test ./...` still runs without any infrastructure.
func newTestApp(t *testing.T) (*App, *pgxpool.Pool) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("VENDRA_TEST_DSN"))
	if dsn == "" {
		t.Skip("set VENDRA_TEST_DSN to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithCancel(context.Background())
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		cancel()
		t.Fatalf("open test database: %v", err)
	}
	// Stop the background worker before the pool it queries goes away.
	t.Cleanup(func() {
		cancel()
		pool.Close()
	})
	key := make([]byte, 32)
	app, err := New(ctx, pool, config.Config{
		BootstrapAdmin:         testAdminEmail,
		BootstrapAdminPassword: testAdminPassword,
		EncryptionKey:          key,
	}, t.TempDir())
	if err != nil {
		t.Fatalf("start application: %v", err)
	}
	return app, pool
}

const (
	testAdminEmail    = "integration-admin@vendra.test"
	testAdminPassword = "IntegrationTest!2026"
)

func postLogin(t *testing.T, handler http.Handler, email, password, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(string(body)))
	r.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestLoginLocksAccountAfterRepeatedFailures(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail); err != nil {
		t.Fatalf("reset attempts: %v", err)
	}
	guard := app.auth.loginProtection(ctx)
	handler := app.Handler()

	for i := 0; i < guard.MaxFailures; i++ {
		if got := postLogin(t, handler, testAdminEmail, "wrong-password", "203.0.113.9:40000").Code; got != http.StatusUnauthorized {
			t.Fatalf("failure %d: status = %d, want 401", i+1, got)
		}
	}
	locked := postLogin(t, handler, testAdminEmail, "wrong-password", "203.0.113.9:40000")
	if locked.Code != http.StatusTooManyRequests {
		t.Fatalf("status after %d failures = %d, want 429", guard.MaxFailures, locked.Code)
	}
	if locked.Header().Get("Retry-After") == "" {
		t.Error("lockout response carried no Retry-After header")
	}
	// The correct password must not bypass an active lockout.
	if got := postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.9:40000").Code; got != http.StatusTooManyRequests {
		t.Fatalf("valid credentials during lockout returned %d, want 429", got)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail); err != nil {
		t.Fatalf("clear lockout: %v", err)
	}
	success := postLogin(t, handler, testAdminEmail, testAdminPassword, "[2001:db8::1]:40000")
	if success.Code != http.StatusOK {
		t.Fatalf("sign-in after lockout expiry returned %d: %s", success.Code, success.Body.String())
	}
	if !strings.Contains(success.Header().Get("Set-Cookie"), sessionCookie) {
		t.Error("successful sign-in issued no session cookie")
	}
	// An IPv6 peer must be stored, not dropped or mangled.
	var storedIP *string
	if err := pool.QueryRow(ctx, `SELECT host(ip) FROM sessions WHERE user_id=(SELECT id FROM users WHERE email=$1) ORDER BY created_at DESC LIMIT 1`, testAdminEmail).Scan(&storedIP); err != nil {
		t.Fatalf("read session ip: %v", err)
	}
	if storedIP == nil || *storedIP != "2001:db8::1" {
		t.Fatalf("session ip = %v, want 2001:db8::1", storedIP)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM login_attempts WHERE email=$1 AND NOT succeeded`, testAdminEmail).Scan(&remaining); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if remaining != 0 {
		t.Errorf("successful sign-in left %d failure rows, so the account stays throttled", remaining)
	}
}

func TestPurgeExpiredRemovesOnlyExpiredRows(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&userID); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1,$2,now()-interval '90 days'),($1,$3,now()+interval '1 day')`,
		userID, []byte("purge-expired-token-hash"), []byte("purge-live-token-hash")); err != nil {
		t.Fatalf("seed sessions: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash IN ($1,$2)`, []byte("purge-expired-token-hash"), []byte("purge-live-token-hash"))
	})
	if err := app.purgeExpired(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
	var expiredLeft, liveLeft int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE token_hash=$1), count(*) FILTER (WHERE token_hash=$2) FROM sessions`,
		[]byte("purge-expired-token-hash"), []byte("purge-live-token-hash")).Scan(&expiredLeft, &liveLeft); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if expiredLeft != 0 {
		t.Error("expired session survived the retention sweep")
	}
	if liveLeft != 1 {
		t.Error("retention sweep deleted a live session")
	}
}

func TestChangePasswordRotatesCredentialAndEvictsSessions(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()
	const rotated = "RotatedPassphrase!2026"

	reset := func(password string) {
		hash, err := app.hashPassword(ctx, password)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE email=$1`, testAdminEmail, hash); err != nil {
			t.Fatalf("restore password: %v", err)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	}
	reset(testAdminPassword)
	t.Cleanup(func() { reset(testAdminPassword) })

	// Two signed-in sessions: the one that rotates, and one that must be evicted.
	current := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.20:5000"))
	other := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.21:5000"))

	if got := postPassword(t, handler, current, map[string]string{"currentPassword": "not-the-password", "newPassword": rotated}).Code; got != http.StatusUnauthorized {
		t.Fatalf("a wrong current password returned %d, want 401", got)
	}
	if got := postPassword(t, handler, current, map[string]string{"currentPassword": testAdminPassword, "newPassword": "짧다"}).Code; got != http.StatusBadRequest {
		t.Fatalf("a policy-violating password returned %d, want 400", got)
	}
	ok := postPassword(t, handler, current, map[string]string{"currentPassword": testAdminPassword, "newPassword": rotated})
	if ok.Code != http.StatusOK {
		t.Fatalf("rotation returned %d: %s", ok.Code, ok.Body.String())
	}

	// The old password must no longer work, the new one must.
	if got := postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.22:5000").Code; got != http.StatusUnauthorized {
		t.Error("the previous password still authenticates")
	}
	if got := postLogin(t, handler, testAdminEmail, rotated, "203.0.113.22:5000").Code; got != http.StatusOK {
		t.Errorf("the rotated password does not authenticate: %d", got)
	}
	// The other session must be gone; the rotating session must survive.
	if got := getMe(t, handler, other).Code; got != http.StatusUnauthorized {
		t.Errorf("a session that existed before the rotation still works: %d", got)
	}
	if got := getMe(t, handler, current).Code; got != http.StatusOK {
		t.Errorf("the session that rotated the password was logged out: %d", got)
	}
}

func sessionCookieFrom(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in failed with %d: %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatal("sign-in returned no session cookie")
	return ""
}

func postPassword(t *testing.T, handler http.Handler, token string, payload map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/me/password", strings.NewReader(string(body)))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func getMe(t *testing.T, handler http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestAdminResetPasswordEvictsEverySessionOfTheTarget(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()
	const targetEmail = "reset-target@vendra.test"
	const targetPassword = "TargetPassphrase!2026"
	const issuedPassword = "AdminIssued!2026"

	hash, err := app.hashPassword(ctx, targetPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var targetID string
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,user_type,status) VALUES($1,'Reset Target',$2,'internal','active')
		ON CONFLICT(email) DO UPDATE SET password_hash=excluded.password_hash,status='active' RETURNING id`, targetEmail, hash).Scan(&targetID); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE email=$1`, targetEmail) })

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email IN ($1,$2)`, targetEmail, testAdminEmail)
	targetSession := sessionCookieFrom(t, postLogin(t, handler, targetEmail, targetPassword, "203.0.113.30:5000"))
	adminSession := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.31:5000"))

	body, _ := json.Marshal(map[string]string{"newPassword": issuedPassword})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+targetID+"/password", strings.NewReader(string(body)))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: adminSession})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("reset returned %d: %s", w.Code, w.Body.String())
	}

	if got := getMe(t, handler, targetSession).Code; got != http.StatusUnauthorized {
		t.Errorf("the target's session survived an administrator reset: %d", got)
	}
	if got := getMe(t, handler, adminSession).Code; got != http.StatusOK {
		t.Errorf("the administrator was logged out by resetting someone else: %d", got)
	}
	if got := postLogin(t, handler, targetEmail, issuedPassword, "203.0.113.32:5000").Code; got != http.StatusOK {
		t.Errorf("the issued password does not authenticate: %d", got)
	}
	if got := postLogin(t, handler, targetEmail, targetPassword, "203.0.113.32:5000").Code; got != http.StatusUnauthorized {
		t.Error("the target's previous password still authenticates")
	}
	// A non-administrator must not be able to reset anyone.
	forbidden := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+targetID+"/password", strings.NewReader(string(body)))
	forbidden.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionCookieFrom(t, postLogin(t, handler, targetEmail, issuedPassword, "203.0.113.33:5000"))})
	fw := httptest.NewRecorder()
	handler.ServeHTTP(fw, forbidden)
	if fw.Code != http.StatusForbidden {
		t.Errorf("a user without the admin permission got %d, want 403", fw.Code)
	}
}
