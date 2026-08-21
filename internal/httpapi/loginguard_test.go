package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginProtectionNormalizedClampsBadInput(t *testing.T) {
	d := defaultLoginProtection()
	got := loginProtection{MaxFailures: -1, WindowMinutes: 0, LockoutMinutes: 99999, MaxAddressFailures: -5}.normalized()
	if got != d {
		t.Fatalf("malformed setting was not repaired: %#v", got)
	}
	if got := (loginProtection{MaxFailures: 0, WindowMinutes: 5, LockoutMinutes: 5}).normalized(); got.MaxFailures != 0 {
		t.Fatalf("administrator could not disable the account limit: %#v", got)
	}
	if got := (loginProtection{MaxFailures: 5000, WindowMinutes: 5, LockoutMinutes: 5}).normalized(); got.MaxFailures != 1000 {
		t.Fatalf("account limit was not capped: %#v", got)
	}
}

func TestLoginProtectionRetryAfter(t *testing.T) {
	guard := defaultLoginProtection()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	if _, locked := guard.retryAfter(4, now.Add(-time.Minute), guard.MaxFailures, now); locked {
		t.Fatal("locked out below the failure threshold")
	}
	retryAfter, locked := guard.retryAfter(5, now.Add(-5*time.Minute), guard.MaxFailures, now)
	if !locked || retryAfter != 10*time.Minute {
		t.Fatalf("expected 10m lockout remaining, got %v (locked=%v)", retryAfter, locked)
	}
	if _, locked := guard.retryAfter(9, now.Add(-time.Hour), guard.MaxFailures, now); locked {
		t.Fatal("lockout did not expire")
	}
	if _, locked := guard.retryAfter(100, now, 0, now); locked {
		t.Fatal("a disabled limit still locked the account")
	}
}

func TestWriteLoginLockedSendsRetryAfter(t *testing.T) {
	w := httptest.NewRecorder()
	writeLoginLocked(w, 90*time.Second)
	if w.Code != 429 {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "90" {
		t.Fatalf("Retry-After = %q, want 90", got)
	}
}

func TestClientIPHandlesIPv6AndMissingPort(t *testing.T) {
	tests := map[string]string{
		"203.0.113.7:51234":  "203.0.113.7",
		"[2001:db8::1]:8080": "2001:db8::1",
		"203.0.113.7":        "203.0.113.7",
		"":                   "",
	}
	for remote, want := range tests {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = remote
		if got := clientIP(r); got != want {
			t.Errorf("clientIP(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestClientIPValueRejectsUnparseablePeers(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "@"
	if got := clientIPValue(r); got != nil {
		t.Fatalf("non-IP peer produced %v, want nil so the inet column stays NULL", got)
	}
	r.RemoteAddr = "[2001:db8::1]:443"
	if got := clientIPValue(r); got == nil {
		t.Fatal("IPv6 peer was discarded")
	}
}

func TestRetentionPolicyNormalized(t *testing.T) {
	d := defaultRetentionPolicy()
	negative := retentionPolicy{ExpiredSessionDays: -1, LoginAttemptDays: -1, FormDraftDays: -1}
	if got := negative.normalized(); got != d {
		t.Fatalf("negative retention was not repaired: %#v", got)
	}
	// Zero is a deliberate value: it turns a sweep off rather than being invalid.
	zeroed := retentionPolicy{ExpiredSessionDays: 0, LoginAttemptDays: 0, FormDraftDays: 0}
	if got := zeroed.normalized(); got != zeroed {
		t.Fatalf("administrator could not disable a sweep: %#v", got)
	}
	huge := retentionPolicy{ExpiredSessionDays: 99999, LoginAttemptDays: 99999, FormDraftDays: 99999}
	if got := huge.normalized(); got.ExpiredSessionDays != 3650 || got.LoginAttemptDays != 3650 || got.FormDraftDays != 3650 {
		t.Fatalf("retention was not capped: %#v", got)
	}
	if d.FormDraftDays <= 0 {
		t.Error("abandoned autosave drafts have no default retention")
	}
}

func TestPasswordMatchesSpendsWorkOnUnknownAccounts(t *testing.T) {
	if passwordMatches("", "anything") {
		t.Fatal("an empty hash authenticated a request")
	}
	if passwordMatches("not-a-bcrypt-hash", "anything") {
		t.Fatal("a corrupt hash authenticated a request")
	}
	start := time.Now()
	passwordMatches("", "anything")
	if elapsed := time.Since(start); elapsed < 5*time.Millisecond {
		t.Fatalf("unknown accounts returned in %v, which leaks account existence by timing", elapsed)
	}
}
