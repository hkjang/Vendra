package httpapi

import "testing"

func boolPtr(v bool) *bool { return &v }

func TestOIDCRequiresVerifiedEmailByDefault(t *testing.T) {
	// An absent key must mean "on": an upgrade that left the setting untouched
	// should not keep accepting unverified addresses.
	if !(oidcSettings{}).requiresVerifiedEmail() {
		t.Error("verification was off when the key was absent")
	}
	if !(oidcSettings{RequireVerifiedEmail: boolPtr(true)}).requiresVerifiedEmail() {
		t.Error("explicit true was not honoured")
	}
	if (oidcSettings{RequireVerifiedEmail: boolPtr(false)}).requiresVerifiedEmail() {
		t.Error("an administrator could not opt out for a provider that omits the claim")
	}
}

func TestOIDCEmailClaimIsOnlyTrustedWhenItShouldBe(t *testing.T) {
	strict := oidcSettings{}
	relaxed := oidcSettings{RequireVerifiedEmail: boolPtr(false)}
	tests := []struct {
		name          string
		settings      oidcSettings
		alreadyLinked bool
		emailVerified bool
		trusted       bool
	}{
		// The attack: a provider that lets anyone claim an address hands over
		// the existing account that owns it.
		{"unverified email, no link, strict", strict, false, false, false},
		{"verified email, no link, strict", strict, false, true, true},
		// A subject already bound to an account identifies it without the email.
		{"unverified email, already linked", strict, true, false, true},
		// Opt-out for providers that never send the claim.
		{"unverified email, no link, relaxed", relaxed, false, false, true},
	}
	for _, test := range tests {
		got := oidcEmailClaimTrusted(test.settings, test.alreadyLinked, test.emailVerified)
		if got != test.trusted {
			t.Errorf("%s: trusted=%v, want %v", test.name, got, test.trusted)
		}
	}
}
