package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hkjang/Vendra/internal/mail"
)

// recordedMail stands in for the relay so the walk below can read what would
// have gone out. The transport itself is proven against a fake SMTP server
// in internal/mail.
type recordedMail struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (r *recordedMail) send(_ context.Context, _ mail.Config, message mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, message)
	return nil
}

func (r *recordedMail) messages() []mail.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]mail.Message(nil), r.sent...)
}

// TestMailIsOffUntilAnAdministratorTurnsItOn walks the feature through the
// API: the seeded rows are off, a relay setting the transport could not use
// is refused, the password never comes back out, an approval writes to the
// people whose turn it is and never to the person who acted, a muted event
// stays quiet, a dead relay fails the mail and not the request, and the
// background pass bundles what it raised into one mail per person.
func TestMailIsOffUntilAnAdministratorTurnsItOn(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()
	const approverEmail, disabledEmail = "mail-approver@vendra.test", "mail-disabled@vendra.test"

	t.Cleanup(func() {
		app.mail.Wait()
		app.mail.SetSender(mail.Deliver)
		_, _ = pool.Exec(ctx, `DELETE FROM mail_deliveries`)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_instances WHERE object_id IN (SELECT id FROM business_objects WHERE number LIKE 'MAIL-%')`)
		_, _ = pool.Exec(ctx, `DELETE FROM notifications WHERE object_id IN (SELECT id FROM business_objects WHERE number LIKE 'MAIL-%')`)
		_, _ = pool.Exec(ctx, `DELETE FROM business_objects WHERE number LIKE 'MAIL-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM workflow_definitions WHERE object_type='payment'`)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE actor_id IN (SELECT id FROM users WHERE email IN($1,$2))`, approverEmail, disabledEmail)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email IN($1,$2)`, approverEmail, disabledEmail)
		for _, setting := range mail.Settings {
			_, _ = pool.Exec(ctx, `UPDATE settings SET value=$2,secret_value=NULL WHERE key=$1`, setting.Key, raw(setting.Default))
		}
	})
	_, _ = pool.Exec(ctx, `DELETE FROM mail_deliveries`)
	_, _ = pool.Exec(ctx, `DELETE FROM workflow_definitions WHERE object_type='payment'`)
	if _, err := pool.Exec(ctx, `INSERT INTO settings(key,value,category) VALUES('workflow.approval_enabled','true','workflow') ON CONFLICT(key) DO UPDATE SET value='true'`); err != nil {
		t.Fatalf("turn the approval process on: %v", err)
	}

	// Fresh: every row is installed, off, and the password is not set.
	for _, setting := range mail.Settings {
		var value []byte
		var secret bool
		var cipher *string
		if err := pool.QueryRow(ctx, `SELECT value,secret,secret_value FROM settings WHERE key=$1`, setting.Key).Scan(&value, &secret, &cipher); err != nil {
			t.Fatalf("the migration did not install %s: %v", setting.Key, err)
		}
		if secret != setting.Secret || cipher != nil {
			t.Errorf("%s: secret=%v cipher=%v", setting.Key, secret, cipher)
		}
	}
	if config, err := app.mail.Config(ctx); err != nil || config.Enabled || config.Port != 25 || config.Security != mail.SecurityAuto {
		t.Fatalf("a fresh installation reads as %+v (%v), want off on port 25", config, err)
	}

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.31:5000"))
	as := func(cookie, method, path string, payload any) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(payload)
		r := httptest.NewRequest(method, path, strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	putSetting := func(key string, value any, secretValue string) *httptest.ResponseRecorder {
		t.Helper()
		payload := map[string]any{"value": value, "category": mail.SettingCategory}
		if secretValue != "" {
			payload["secretValue"] = secretValue
		}
		return as(admin, http.MethodPut, "/api/v1/admin/settings/"+key, payload)
	}

	// A value the transport could never use is refused with the row named.
	for key, bad := range map[string]any{mail.KeyPort: 70000, mail.KeySecurity: "quantum", mail.KeyFromAddress: "nobody", mail.KeyTimeout: 0} {
		if w := putSetting(key, bad, ""); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), key) {
			t.Errorf("%s=%v was answered %d %s", key, bad, w.Code, w.Body.String())
		}
	}

	// Configure and switch on. The password goes in through secretValue …
	for key, value := range map[string]any{mail.KeyHost: "relay.internal", mail.KeyFromAddress: "vendra@corp.example", mail.KeyBaseURL: "https://vendra.corp.example/", mail.KeyEnabled: true, mail.KeyUsername: "vendra"} {
		if w := putSetting(key, value, ""); w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", key, w.Code, w.Body.String())
		}
	}
	const relayPassword = "relay-pw"
	if w := putSetting(mail.KeyPassword, "", relayPassword); w.Code != http.StatusOK {
		t.Fatalf("password: %d %s", w.Code, w.Body.String())
	}
	// … and never comes back out: the settings list says "configured" and
	// nothing more, and only the transport gets the decrypted value.
	listed := as(admin, http.MethodGet, "/api/v1/admin/settings", nil)
	if strings.Contains(listed.Body.String(), relayPassword) {
		t.Fatal("the settings API returned the SMTP password")
	}
	var settings struct {
		Items []struct {
			Key              string `json:"key"`
			Value            any    `json:"value"`
			Secret           bool   `json:"secret"`
			SecretConfigured bool   `json:"secretConfigured"`
		} `json:"items"`
	}
	_ = json.Unmarshal(listed.Body.Bytes(), &settings)
	found := false
	for _, item := range settings.Items {
		if item.Key == mail.KeyPassword {
			found = true
			if !item.Secret || !item.SecretConfigured || item.Value != "" {
				t.Errorf("the password row lists as %+v, want secret, configured, empty", item)
			}
		}
	}
	if !found {
		t.Fatal("the password row is not listed")
	}
	config, err := app.mail.Config(ctx)
	if err != nil || !config.Enabled || config.Password != relayPassword || config.Host != "relay.internal" || config.BaseURL != "https://vendra.corp.example" {
		t.Fatalf("stored configuration = %+v (%v)", config, err)
	}
	var auditLeak int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE after::text LIKE '%'||$1||'%'`, relayPassword).Scan(&auditLeak)
	if auditLeak != 0 {
		t.Fatal("the audit trail carries the SMTP password")
	}

	recorder := &recordedMail{}
	app.mail.SetSender(recorder.send)

	// Two approvers by role, one of them disabled; the rule routes 지급 to
	// that role.
	for _, user := range []struct{ email, status string }{{approverEmail, "active"}, {disabledEmail, "disabled"}} {
		w := as(admin, http.MethodPost, "/api/v1/admin/users", map[string]any{"email": user.email, "displayName": "결재자 " + user.status, "password": testAdminPassword, "userType": "internal", "status": user.status, "roleCodes": []string{"procurement_manager"}})
		if w.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", user.email, w.Code, w.Body.String())
		}
	}
	if w := as(admin, http.MethodPost, "/api/v1/workflows", map[string]any{"name": "지급 메일 검증", "objectType": "payment", "enabled": true, "conditions": map[string]any{}, "steps": []map[string]any{{"name": "구매 관리자 승인", "role": "procurement_manager"}}}); w.Code != http.StatusCreated {
		t.Fatalf("create the rule: %d %s", w.Code, w.Body.String())
	}
	seedPayment := func(number string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO business_objects(object_type,number,title,status,amount,currency,owner_id,created_by) VALUES('payment',$1,'메일 검증 지급','draft',2500000,'KRW',(SELECT id FROM users WHERE email=$2),(SELECT id FROM users WHERE email=$2)) RETURNING id`, number, testAdminEmail).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", number, err)
		}
		return id
	}
	recipients := func(from int) []string {
		var out []string
		for _, m := range recorder.messages()[from:] {
			out = append(out, m.To)
		}
		sort.Strings(out)
		return out
	}

	// Submitting writes to the active approver — not the disabled one, and
	// not the requester.
	first := seedPayment("MAIL-1")
	if w := as(admin, http.MethodPost, "/api/v1/payments/"+first+"/submit", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	app.mail.Wait()
	if got := recipients(0); strings.Join(got, ",") != approverEmail {
		t.Fatalf("after submit, mail went to %v, want only the active approver", got)
	}
	requested := recorder.messages()[0]
	for _, want := range []string{"결재 요청", "지급", "메일 검증 지급", "MAIL-1", "구매 관리자 승인", "2500000 KRW", "https://vendra.corp.example/approvals"} {
		if !strings.Contains(requested.Subject+"\n"+requested.Body, want) {
			t.Errorf("the request mail lacks %q:\nsubject=%s\n%s", want, requested.Subject, requested.Body)
		}
	}
	var recorded struct {
		Event, Recipient, Status, ObjectID string
		Attempts                           int
	}
	if err := pool.QueryRow(ctx, `SELECT event,recipient,status,object_id::text,attempts FROM mail_deliveries ORDER BY created_at DESC LIMIT 1`).Scan(&recorded.Event, &recorded.Recipient, &recorded.Status, &recorded.ObjectID, &recorded.Attempts); err != nil {
		t.Fatalf("read the delivery: %v", err)
	}
	if recorded.Event != mail.EventApprovalRequested || recorded.Recipient != approverEmail || recorded.Status != mail.StatusSent || recorded.ObjectID != first || recorded.Attempts != 1 {
		t.Fatalf("delivery = %+v", recorded)
	}

	// The approver decides; the requester hears, the approver does not.
	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, approverEmail)
	approver := sessionCookieFrom(t, postLogin(t, handler, approverEmail, testAdminPassword, "203.0.113.32:5000"))
	var instanceID string
	if err := pool.QueryRow(ctx, `SELECT id FROM workflow_instances WHERE object_id=$1 AND status='pending'`, first).Scan(&instanceID); err != nil {
		t.Fatalf("find the instance: %v", err)
	}
	if w := as(approver, http.MethodPost, "/api/v1/approvals/"+instanceID+"/actions", map[string]any{"action": "approve", "comment": "예산 범위 내"}); w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	app.mail.Wait()
	if got := recipients(1); strings.Join(got, ",") != testAdminEmail {
		t.Fatalf("after the decision, mail went to %v, want only the requester", got)
	}
	decided := recorder.messages()[1]
	for _, want := range []string{"승인: 지급", "최종 승인되었습니다", "> 예산 범위 내", "https://vendra.corp.example/payments"} {
		if !strings.Contains(decided.Subject+"\n"+decided.Body, want) {
			t.Errorf("the decision mail lacks %q:\nsubject=%s\n%s", want, decided.Subject, decided.Body)
		}
	}

	// A muted event stays quiet while the others go on.
	if w := putSetting(mail.EventSwitches[mail.EventApprovalDecided], false, ""); w.Code != http.StatusOK {
		t.Fatalf("mute: %d %s", w.Code, w.Body.String())
	}
	second := seedPayment("MAIL-2")
	if w := as(admin, http.MethodPost, "/api/v1/payments/"+second+"/submit", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM workflow_instances WHERE object_id=$1 AND status='pending'`, second).Scan(&instanceID); err != nil {
		t.Fatalf("find the instance: %v", err)
	}
	if w := as(approver, http.MethodPost, "/api/v1/approvals/"+instanceID+"/actions", map[string]any{"action": "reject", "comment": "근거 부족"}); w.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", w.Code, w.Body.String())
	}
	app.mail.Wait()
	if got := recipients(2); strings.Join(got, ",") != approverEmail {
		t.Fatalf("with decisions muted, mail went to %v, want only the approver's request mail", got)
	}
	_ = putSetting(mail.EventSwitches[mail.EventApprovalDecided], true, "")

	// Off means off: nothing goes out and nothing is recorded.
	_ = putSetting(mail.KeyEnabled, false, "")
	third := seedPayment("MAIL-3")
	if w := as(admin, http.MethodPost, "/api/v1/payments/"+third+"/submit", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	app.mail.Wait()
	if len(recorder.messages()) != 3 {
		t.Fatalf("mail went out while the feature is off: %d messages", len(recorder.messages()))
	}
	var offCount int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM mail_deliveries WHERE object_id=$1`, third).Scan(&offCount)
	if offCount != 0 {
		t.Fatal("a delivery was recorded while the feature is off")
	}
	_ = putSetting(mail.KeyEnabled, true, "")

	// The background pass bundles what it raised for one person into one
	// mail, and only on the pass that raised it.
	if _, err := pool.Exec(ctx, `INSERT INTO business_objects(object_type,number,title,status,end_date,owner_id,created_by) VALUES
		('contract','MAIL-C1','연간 유지보수','active',current_date+5,(SELECT id FROM users WHERE email=$1),(SELECT id FROM users WHERE email=$1)),
		('contract','MAIL-C2','라이선스 갱신','active',current_date+20,(SELECT id FROM users WHERE email=$1),(SELECT id FROM users WHERE email=$1))`, approverEmail); err != nil {
		t.Fatalf("seed the contracts: %v", err)
	}
	if err := app.scheduleNotifications(ctx); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	app.mail.Wait()
	if got := recipients(3); strings.Join(got, ",") != approverEmail {
		t.Fatalf("the expiry pass wrote to %v, want one mail to the owner", got)
	}
	digest := recorder.messages()[3]
	if !strings.Contains(digest.Subject, "만료 임박 알림 (2건)") || !strings.Contains(digest.Body, "MAIL-C1") || !strings.Contains(digest.Body, "MAIL-C2") {
		t.Fatalf("digest:\nsubject=%s\n%s", digest.Subject, digest.Body)
	}
	if err := app.scheduleNotifications(ctx); err != nil {
		t.Fatalf("schedule again: %v", err)
	}
	app.mail.Wait()
	if len(recorder.messages()) != 4 {
		t.Fatalf("the second pass mailed the same rows again: %d messages", len(recorder.messages()))
	}

	// The test button sends one real message and records it like any other.
	if w := as(admin, http.MethodPost, "/api/v1/admin/mail/test", map[string]any{"recipient": "Ops@corp.example"}); w.Code != http.StatusOK {
		t.Fatalf("test send: %d %s", w.Code, w.Body.String())
	}
	if got := recipients(4); strings.Join(got, ",") != "ops@corp.example" {
		t.Fatalf("the test mail went to %v", got)
	}
	if w := as(admin, http.MethodPost, "/api/v1/admin/mail/test", map[string]any{"recipient": "not-an-address"}); w.Code != http.StatusBadRequest {
		t.Errorf("a bad recipient was answered %d", w.Code)
	}

	// A dead relay: the test button says so, and a submission is answered
	// as if nothing were wrong — the failure is the mail's, recorded, not
	// the request's.
	app.mail.SetSender(mail.Deliver)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, closedPort, _ := net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	var port int
	_, _ = fmt.Sscanf(closedPort, "%d", &port)
	for key, value := range map[string]any{mail.KeyHost: "127.0.0.1", mail.KeyPort: port, mail.KeyTimeout: 1, mail.KeyUsername: ""} {
		if w := putSetting(key, value, ""); w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", key, w.Code, w.Body.String())
		}
	}
	if w := as(admin, http.MethodPost, "/api/v1/admin/mail/test", map[string]any{}); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "mail_send_failed") || !strings.Contains(w.Body.String(), "SMTP 연결 실패") {
		t.Fatalf("test send against a dead relay: %d %s", w.Code, w.Body.String())
	}
	fourth := seedPayment("MAIL-4")
	started := time.Now()
	if w := as(admin, http.MethodPost, "/api/v1/payments/"+fourth+"/submit", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("submit with the relay down: %d %s", w.Code, w.Body.String())
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("the submission waited %s on a dead relay", time.Since(started))
	}
	if w := as(admin, http.MethodGet, "/api/v1/me", nil); w.Code != http.StatusOK {
		t.Fatalf("the application is not answering while the relay is down: %d", w.Code)
	}
	app.mail.Wait()
	page := as(admin, http.MethodGet, "/api/v1/admin/mail/deliveries?status=failed", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("deliveries: %d %s", page.Code, page.Body.String())
	}
	var deliveries mail.Page
	_ = json.Unmarshal(page.Body.Bytes(), &deliveries)
	failed := map[string]mail.Delivery{}
	for _, item := range deliveries.Items {
		failed[item.Event] = item
	}
	if item, ok := failed[mail.EventApprovalRequested]; !ok || item.ObjectID != fourth || item.Attempts != 2 || !strings.Contains(item.ErrorMessage, "SMTP 연결 실패") {
		t.Errorf("the request mail against a dead relay is recorded as %+v", item)
	}
	if item, ok := failed[mail.EventTest]; !ok || item.Attempts != 1 || item.Recipient != testAdminEmail {
		t.Errorf("the test mail against a dead relay is recorded as %+v", item)
	}
	if deliveries.Status[mail.StatusSent] < 5 || deliveries.Status[mail.StatusFailed] < 2 || strings.Contains(page.Body.String(), "\"body\"") {
		t.Errorf("summary = %+v, and no body may be in the log", deliveries.Status)
	}
}
