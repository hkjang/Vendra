package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestSessionListAndRevocation(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()
	_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=(SELECT id FROM users WHERE email=$1)`, testAdminEmail)
	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)

	current := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.40:5000"))
	laptop := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.41:5000"))
	phone := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "[2001:db8::99]:5000"))

	sessions := listSessions(t, handler, current)
	if len(sessions) != 3 {
		t.Fatalf("listed %d sessions, want 3", len(sessions))
	}
	currentCount, addresses := 0, map[string]bool{}
	for _, s := range sessions {
		if s.Current {
			currentCount++
		}
		if s.IP != nil {
			addresses[*s.IP] = true
		}
	}
	if currentCount != 1 {
		t.Errorf("%d sessions claimed to be current, want exactly 1", currentCount)
	}
	// The IPv6 peer must be listed as itself, not dropped or mangled.
	if !addresses["2001:db8::99"] {
		t.Errorf("IPv6 session address missing from %v", addresses)
	}

	// Revoking the current session must be refused; that is what logout is for.
	var currentID string
	for _, s := range sessions {
		if s.Current {
			currentID = s.ID
		}
	}
	if got := doRequest(t, handler, http.MethodDelete, "/api/v1/me/sessions/"+currentID, current).Code; got != http.StatusBadRequest {
		t.Errorf("revoking the current session returned %d, want 400", got)
	}

	// Revoke one other device and confirm only that one died.
	var laptopID string
	for _, s := range sessions {
		if !s.Current && s.IP != nil && *s.IP == "203.0.113.41" {
			laptopID = s.ID
		}
	}
	if got := doRequest(t, handler, http.MethodDelete, "/api/v1/me/sessions/"+laptopID, current).Code; got != http.StatusOK {
		t.Fatalf("revoking a session returned %d", got)
	}
	if got := getMe(t, handler, laptop).Code; got != http.StatusUnauthorized {
		t.Errorf("the revoked session still works: %d", got)
	}
	if got := getMe(t, handler, phone).Code; got != http.StatusOK {
		t.Errorf("an unrelated session was revoked: %d", got)
	}

	// Sweep the rest.
	if got := doRequest(t, handler, http.MethodPost, "/api/v1/me/sessions/revoke-others", current).Code; got != http.StatusOK {
		t.Fatalf("revoke-others returned %d", got)
	}
	if got := getMe(t, handler, phone).Code; got != http.StatusUnauthorized {
		t.Errorf("revoke-others left another session alive: %d", got)
	}
	if got := getMe(t, handler, current).Code; got != http.StatusOK {
		t.Errorf("revoke-others logged out the caller: %d", got)
	}
	if remaining := listSessions(t, handler, current); len(remaining) != 1 {
		t.Errorf("%d sessions remain, want only the current one", len(remaining))
	}
}

type listedSession struct {
	ID      string  `json:"id"`
	IP      *string `json:"ip"`
	Current bool    `json:"current"`
}

func listSessions(t *testing.T, handler http.Handler, token string) []listedSession {
	t.Helper()
	w := doRequest(t, handler, http.MethodGet, "/api/v1/me/sessions", token)
	if w.Code != http.StatusOK {
		t.Fatalf("listing sessions returned %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Items []listedSession `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	return body.Items
}

func doRequest(t *testing.T, handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

// TestEditingAWorkflowDoesNotStrandInFlightApprovals reproduces the failure an
// administrator would cause by trimming a workflow while requests are pending.
func TestEditingAWorkflowDoesNotStrandInFlightApprovals(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	var adminID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&adminID); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	twoSteps := `[{"name":"팀장 승인","role":"","order":0},{"name":"재무 승인","role":"","order":1}]`
	oneStep := `[{"name":"재무 승인","role":"","order":0}]`

	var definitionID string
	if err := pool.QueryRow(ctx, `INSERT INTO workflow_definitions(name,object_type,enabled,steps,created_by) VALUES('스냅샷 검증','contract',true,$1,$2) RETURNING id`, twoSteps, adminID).Scan(&definitionID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	var objectID string
	if err := pool.QueryRow(ctx, `INSERT INTO business_objects(object_type,number,title,status,owner_id,created_by) VALUES('contract','SNAPSHOT-TEST-1','스냅샷 검증 계약','pending_approval',$1,$1) RETURNING id`, adminID).Scan(&objectID); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	// The instance is already one step in, exactly like a request that cleared
	// its first approver before the workflow was edited.
	var instanceID string
	if err := pool.QueryRow(ctx, `INSERT INTO workflow_instances(definition_id,object_type,object_id,requested_by,current_step,context) VALUES($1,'contract',$2,$3,1,$4) RETURNING id`,
		definitionID, objectID, adminID, `{"steps":`+twoSteps+`}`).Scan(&instanceID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_instances WHERE id=$1`, instanceID)
		_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE id=$1`, objectID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_definitions WHERE id=$1`, definitionID)
	})

	// The administrator trims the workflow to a single step.
	if _, err := pool.Exec(ctx, `UPDATE workflow_definitions SET steps=$2,version=version+1 WHERE id=$1`, definitionID, oneStep); err != nil {
		t.Fatalf("edit workflow: %v", err)
	}

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.50:5000"))

	body, _ := json.Marshal(map[string]string{"action": "approve", "comment": "확인"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+instanceID+"/actions", strings.NewReader(string(body)))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("approval returned %d (%s); reading the live definition strands it at 409", w.Code, w.Body.String())
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM workflow_instances WHERE id=$1`, instanceID).Scan(&status); err != nil {
		t.Fatalf("read instance: %v", err)
	}
	// Step 1 was the last of the two it was submitted under, so it completes.
	if status != "approved" {
		t.Errorf("instance status = %q, want approved", status)
	}
	var objectStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM business_objects WHERE id=$1`, objectID).Scan(&objectStatus); err != nil {
		t.Fatalf("read object: %v", err)
	}
	if objectStatus != "approved" {
		t.Errorf("object status = %q, want approved", objectStatus)
	}
}

func TestSeparationOfDutiesBlocksSelfApprovalWhenEnabled(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	var adminID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&adminID); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	steps := `[{"name":"재무 승인","role":"","order":0}]`
	var definitionID, objectID, instanceID string
	if err := pool.QueryRow(ctx, `INSERT INTO workflow_definitions(name,object_type,enabled,steps,created_by) VALUES('자기결재 검증','contract',true,$1,$2) RETURNING id`, steps, adminID).Scan(&definitionID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO business_objects(object_type,number,title,status,owner_id,created_by) VALUES('contract','SOD-TEST-1','자기결재 검증','pending_approval',$1,$1) RETURNING id`, adminID).Scan(&objectID); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	// The administrator is both the requester and the only approver.
	if err := pool.QueryRow(ctx, `INSERT INTO workflow_instances(definition_id,object_type,object_id,requested_by,context) VALUES($1,'contract',$2,$3,$4) RETURNING id`,
		definitionID, objectID, adminID, `{"steps":`+steps+`}`).Scan(&instanceID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	restore := func() {
		_, _ = pool.Exec(ctx, `UPDATE settings SET value='{"blockSelfApproval":false}' WHERE key='workflow.separation_of_duties'`)
	}
	t.Cleanup(func() {
		restore()
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_instances WHERE id=$1`, instanceID)
		_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE id=$1`, objectID)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_definitions WHERE id=$1`, definitionID)
	})

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.60:5000"))
	act := func(action string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"action": action, "comment": "확인"})
		r := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+instanceID+"/actions", strings.NewReader(string(body)))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// Off by default: existing deployments keep working after an upgrade.
	if got := act("approve").Code; got != http.StatusOK {
		t.Fatalf("self-approval with the control off returned %d, want 200", got)
	}
	_, _ = pool.Exec(ctx, `UPDATE workflow_instances SET status='pending',current_step=0,completed_at=NULL WHERE id=$1`, instanceID)

	if _, err := pool.Exec(ctx, `UPDATE settings SET value='{"blockSelfApproval":true}' WHERE key='workflow.separation_of_duties'`); err != nil {
		t.Fatalf("enable control: %v", err)
	}
	if got := act("approve").Code; got != http.StatusForbidden {
		t.Errorf("self-approval with the control on returned %d, want 403", got)
	}
	if got := act("reject").Code; got != http.StatusForbidden {
		t.Errorf("self-rejection with the control on returned %d, want 403", got)
	}
	// Returning for revision hands the request back rather than deciding it.
	if got := act("return").Code; got != http.StatusOK {
		t.Errorf("returning one's own request was blocked with %d, want 200", got)
	}
}

// TestMCPRefusesSupplierPortalAccounts guards the portal isolation the product
// depends on. MCP tools scope results by organisation, which portal accounts do
// not use, so a supplier that reached them would see other suppliers' data.
func TestMCPRefusesSupplierPortalAccounts(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()
	const portalEmail = "mcp-portal@vendra.test"
	const portalPassword = "PortalPassphrase!2026"

	var supplierID string
	if err := pool.QueryRow(ctx, `INSERT INTO suppliers(supplier_number,name,business_number,status) VALUES('SUP-MCP-TEST','MCP 검증 공급사','000-00-00000','active')
		ON CONFLICT(supplier_number) DO UPDATE SET name=excluded.name RETURNING id`).Scan(&supplierID); err != nil {
		t.Fatalf("seed supplier: %v", err)
	}
	hash, err := app.hashPassword(ctx, portalPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var portalID string
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,user_type,supplier_id,status) VALUES($1,'MCP 포털 사용자',$2,'supplier',$3,'active')
		ON CONFLICT(email) DO UPDATE SET password_hash=excluded.password_hash,user_type='supplier',supplier_id=excluded.supplier_id,status='active' RETURNING id`, portalEmail, hash, supplierID).Scan(&portalID); err != nil {
		t.Fatalf("seed portal user: %v", err)
	}
	// Deliberately over-grant: an administrator hands this portal account a
	// company-wide read role. Only the user type should keep it out of MCP.
	var roleID string
	if err := pool.QueryRow(ctx, `INSERT INTO roles(code,name,permissions,data_scope,system) VALUES('mcp_test_reader','MCP 검증 조회','["supplier.read"]','company',false)
		ON CONFLICT(code) DO UPDATE SET permissions=excluded.permissions,data_scope='company' RETURNING id`).Scan(&roleID); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, portalID, roleID); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email=$1`, portalEmail)
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE code='mcp_test_reader'`)
		_, _ = pool.Exec(ctx, `DELETE FROM suppliers WHERE supplier_number='SUP-MCP-TEST'`)
	})

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email IN ($1,$2)`, portalEmail, testAdminEmail)
	portal := sessionCookieFrom(t, postLogin(t, handler, portalEmail, portalPassword, "203.0.113.70:5000"))

	call := func(token string) *httptest.ResponseRecorder {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_suppliers","arguments":{"query":""}}}`
		r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	w := call(portal)
	if w.Code != http.StatusOK {
		t.Fatalf("MCP returned HTTP %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "error") {
		t.Fatalf("a supplier portal account reached an MCP tool: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "MCP 검증 공급사") {
		t.Fatal("supplier data was returned to a portal account through MCP")
	}

	// An internal administrator must still be able to use the same tool.
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.71:5000"))
	adminResult := call(admin)
	if adminResult.Code != http.StatusOK || strings.Contains(adminResult.Body.String(), `"isError":true`) {
		t.Fatalf("MCP broke for an administrator: %d %s", adminResult.Code, adminResult.Body.String())
	}
	if !strings.Contains(adminResult.Body.String(), "MCP 검증 공급사") {
		t.Errorf("administrator did not receive supplier results: %s", adminResult.Body.String())
	}
}

func TestListUsersIsBoundedSearchableAndHonestAboutTruncation(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	hash, err := app.hashPassword(ctx, "SeededUser!2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO users(email,display_name,password_hash,user_type,status) VALUES($1,$2,$3,'internal','active') ON CONFLICT(email) DO NOTHING`,
			fmt.Sprintf("bulk-%02d@vendra.test", i), fmt.Sprintf("대량 사용자 %02d", i), hash); err != nil {
			t.Fatalf("seed user %d: %v", i, err)
		}
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE email LIKE 'bulk-%@vendra.test'`) })

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.80:5000"))

	read := func(query string) (items int, truncated bool, limit int) {
		t.Helper()
		w := doRequest(t, handler, http.MethodGet, "/api/v1/admin/users?"+query, admin)
		if w.Code != http.StatusOK {
			t.Fatalf("listing users returned %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			Items     []map[string]any `json:"items"`
			Truncated bool             `json:"truncated"`
			Limit     int              `json:"limit"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return len(body.Items), body.Truncated, body.Limit
	}

	// A small page must report itself as cut off rather than looking complete.
	items, truncated, limit := read("limit=5")
	if items != 5 || limit != 5 {
		t.Fatalf("got %d items with limit %d, want 5/5", items, limit)
	}
	if !truncated {
		t.Error("a truncated page reported truncated=false; the operator would think they saw everyone")
	}
	// The extra probe row must never leak into the results.
	if items > limit {
		t.Errorf("returned %d items for a limit of %d", items, limit)
	}

	// Search narrows the set, and a complete page is not flagged as cut off.
	items, truncated, _ = read("q=" + url.QueryEscape("bulk-03@vendra.test"))
	if items != 1 {
		t.Errorf("search returned %d users, want 1", items)
	}
	if truncated {
		t.Error("a complete result was reported as truncated")
	}
	if items, _, _ = read("q=" + url.QueryEscape("존재하지 않는 사용자")); items != 0 {
		t.Errorf("an unmatched search returned %d users", items)
	}
}
