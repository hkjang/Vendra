package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TestEveryWayAnInvitationStopsWorkingIsReadAtRedemption binds the statement
// that redeems an invitation to the list of endings the application shows.
//
// An invitation link is a bearer credential: whoever holds it registers a
// portal account bound to a supplier, and that account then reads the
// supplier's contracts, orders, deliveries and evaluations. There are three
// ways one stops working — it was used, it was called back, its time ran out —
// and the redemption has to read all three. A recall the redemption did not
// read would be a 회수 button that answers 204, moves the badge to 회수됨, and
// leaves the link working for the rest of its fortnight: worse than no button,
// because somebody would stop worrying about it.
//
// The endings are taken from invitationStanding rather than listed here, so a
// fourth one added to the list fails here until the redemption reads it too.
func TestEveryWayAnInvitationStopsWorkingIsReadAtRedemption(t *testing.T) {
	columns := regexp.MustCompile(`[a-z_]+_at`).FindAllString(invitationStanding, -1)
	seen := map[string]bool{}
	endings := []string{}
	for _, column := range columns {
		if !seen[column] {
			seen[column] = true
			endings = append(endings, column)
		}
	}
	if len(endings) < 3 {
		t.Fatalf("only %v was read out of invitationStanding; this test has gone stale", endings)
	}

	body := supplierStatementBody(t, "registerSupplierUser")
	redemption := regexp.MustCompile("SELECT [^`]*FROM invitations WHERE token_hash=[^`]*").FindString(body)
	if redemption == "" {
		t.Fatal("the redemption's SELECT FROM invitations was not found; this test is no longer watching it")
	}
	for _, ending := range endings {
		if !strings.Contains(redemption, ending) {
			t.Errorf("초대 상태는 %s로 끝날 수 있는데 가입 시점의 조회가 그 컬럼을 읽지 않는다: %s", ending, redemption)
		}
	}
}

// TestARevokedInvitationCannotBeRegisteredWith walks the recall through the
// endpoints, which is where there was nothing at all.
//
// Issuing the link was the whole of the feature. It was handed over as a
// one-time URL and after the modal closed nothing in the application said who
// had been invited, whether they had used it, or when it ran out — so an
// invitation sent to a mistyped address, or to the person who has since left
// that supplier, stayed live for up to a fortnight with no way to stop it and
// no screen that would have shown it was outstanding.
func TestARevokedInvitationCannotBeRegisteredWith(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	var supplierID string
	if err := pool.QueryRow(ctx, `INSERT INTO suppliers(supplier_number,name,business_number,status,risk_level)
		VALUES('SUP-INVITE-A','초대 회수 업체','888-88-88888','active','LOW')
		ON CONFLICT(supplier_number) DO UPDATE SET name=excluded.name RETURNING id`).Scan(&supplierID); err != nil {
		t.Fatalf("seed the supplier: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), not t.Context(): the test context is already
		// cancelled by the time cleanup runs and these would silently no-op.
		// audit_logs first — it points at the account by actor_id.
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_email LIKE 'invite-%@vendra.test'
			OR actor_id IN(SELECT id FROM users WHERE email LIKE 'invite-%@vendra.test')
			OR (object_type='invitation' AND object_id IN(SELECT id::text FROM invitations WHERE supplier_id=$1))`, supplierID)
		_, _ = pool.Exec(ctx, `DELETE FROM email_verifications WHERE email LIKE 'invite-%@vendra.test'`)
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id IN(SELECT id FROM users WHERE email LIKE 'invite-%@vendra.test')`)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email LIKE 'invite-%@vendra.test'`)
		_, _ = pool.Exec(ctx, `DELETE FROM invitations WHERE supplier_id=$1`, supplierID)
		_, _ = pool.Exec(ctx, `DELETE FROM suppliers WHERE supplier_number='SUP-INVITE-A'`)
	})
	// A clean slate: the addresses from an earlier run would already have
	// accounts, and the invitations from one would already be in the list.
	_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_email LIKE 'invite-%@vendra.test'`)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email LIKE 'invite-%@vendra.test'`)
	_, _ = pool.Exec(ctx, `DELETE FROM invitations WHERE supplier_id=$1`, supplierID)

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.77:5000"))
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	invite := func(email string) (id, token string) {
		t.Helper()
		w := call(http.MethodPost, "/api/v1/invitations", `{"email":"`+email+`","supplierId":"`+supplierID+`","expiresInDays":7}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("issuing an invitation for %s returned %d: %s", email, w.Code, w.Body.String())
		}
		var out struct{ ID, InvitationURL string }
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode the issued invitation: %v", err)
		}
		return out.ID, strings.TrimPrefix(out.InvitationURL, "/register?token=")
	}
	register := func(token string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"token": token, "displayName": "초대받은 담당자", "password": "InvitationTest!2026"})
		r := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	standings := func() map[string]string {
		t.Helper()
		w := call(http.MethodGet, "/api/v1/invitations?supplierId="+supplierID, "")
		if w.Code != http.StatusOK {
			t.Fatalf("the invitation list returned %d: %s", w.Code, w.Body.String())
		}
		var out struct {
			Items []struct{ Email, Status string }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode the invitation list: %v", err)
		}
		by := map[string]string{}
		for _, item := range out.Items {
			by[item.Email] = item.Status
		}
		return by
	}

	// Sent to the wrong address. Before the list there was nowhere that showed
	// it had gone out, and nowhere it could be stopped.
	wrongID, wrongToken := invite("invite-typo@vendra.test")
	if got := standings()["invite-typo@vendra.test"]; got != "pending" {
		t.Fatalf("a freshly issued invitation reads %q, want pending", got)
	}
	if w := call(http.MethodDelete, "/api/v1/invitations/"+wrongID, ""); w.Code != http.StatusNoContent {
		t.Fatalf("recalling the invitation returned %d, want 204: %s", w.Code, w.Body.String())
	}
	if got := standings()["invite-typo@vendra.test"]; got != "revoked" {
		t.Errorf("a recalled invitation reads %q, want revoked", got)
	}
	// The point of the button: the link itself has to stop working. A badge
	// that says 회수됨 over a link that still signs someone up is worse than no
	// button, because it is the thing that stops anybody worrying about it.
	w := register(wrongToken)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("registering with a recalled link returned %d, want 400: %s", w.Code, w.Body.String())
	}
	if code, msg := errorCodeAndMessage(t, w); code != "invalid_invitation" {
		t.Errorf("a recalled link answered %q, want invalid_invitation: %s", code, msg)
	}
	// Recalling it again is where the caller already asked for it to be.
	if w := call(http.MethodDelete, "/api/v1/invitations/"+wrongID, ""); w.Code != http.StatusNoContent {
		t.Errorf("recalling an already recalled invitation returned %d, want 204: %s", w.Code, w.Body.String())
	}

	// The invitation that was meant to go out. It still works, and once it has
	// been used the list says so rather than offering to call it back.
	rightID, rightToken := invite("invite-right@vendra.test")
	if w := register(rightToken); w.Code != http.StatusCreated {
		t.Fatalf("registering with a live link returned %d, want 201: %s", w.Code, w.Body.String())
	}
	if got := standings()["invite-right@vendra.test"]; got != "accepted" {
		t.Errorf("a redeemed invitation reads %q, want accepted", got)
	}
	w = call(http.MethodDelete, "/api/v1/invitations/"+rightID, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("recalling a redeemed invitation returned %d, want 409: %s", w.Code, w.Body.String())
	}
	code, msg := errorCodeAndMessage(t, w)
	if code != "invitation_accepted" {
		t.Errorf("recalling a redeemed invitation answered %q, want invitation_accepted: %s", code, msg)
	}
	// There is no link left to call back, so the refusal has to say what does
	// stop the account that now exists.
	if !strings.Contains(msg, "비활성화") {
		t.Errorf("the refusal does not say what to do instead: %s", msg)
	}
}
