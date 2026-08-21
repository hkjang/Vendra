package httpapi

import (
	"strings"
	"testing"
)

func TestPasswordPolicyCountsCharactersNotBytes(t *testing.T) {
	policy := defaultPasswordPolicy()
	// Four Korean characters are twelve bytes. The previous byte-based check
	// accepted them as "10자 이상".
	if err := policy.validate("가나다라"); err == nil {
		t.Fatal("a four-character password satisfied a ten-character minimum")
	}
	if err := policy.validate(strings.Repeat("가", 10)); err != nil {
		t.Fatalf("a ten-character Korean password was rejected: %v", err)
	}
}

func TestPasswordPolicyRejectsWhatBcryptCannotHash(t *testing.T) {
	policy := defaultPasswordPolicy()
	// 24 Korean characters are exactly 72 bytes, the bcrypt limit.
	if err := policy.validate(strings.Repeat("가", 24)); err != nil {
		t.Fatalf("a 72-byte password was rejected: %v", err)
	}
	err := policy.validate(strings.Repeat("가", 25))
	if err == nil {
		t.Fatal("a 75-byte password was accepted; bcrypt would fail on it")
	}
	if !strings.Contains(err.Error(), "바이트") {
		t.Errorf("length error does not explain the byte limit: %v", err)
	}
}

func TestPasswordPolicyRejectsBlank(t *testing.T) {
	if defaultPasswordPolicy().validate("          ") == nil {
		t.Fatal("a whitespace-only password was accepted")
	}
}

func TestPasswordPolicyCharacterClasses(t *testing.T) {
	policy := passwordPolicy{MinLength: 10, RequireClasses: 3}.normalized()
	if policy.validate("alllowercase") == nil {
		t.Fatal("a single-class password satisfied a three-class requirement")
	}
	if err := policy.validate("Passw0rdLong"); err != nil {
		t.Fatalf("a three-class password was rejected: %v", err)
	}
	if got := characterClasses("Aa1!"); got != 4 {
		t.Fatalf("characterClasses = %d, want 4", got)
	}
	// Korean letters are neither upper nor lower case, so they count as the
	// symbol class rather than silently counting as nothing.
	if got := characterClasses("가나다"); got != 1 {
		t.Fatalf("characterClasses for Korean = %d, want 1", got)
	}
}

func TestPasswordPolicyNormalizedClampsBadInput(t *testing.T) {
	d := defaultPasswordPolicy()
	if got := (passwordPolicy{MinLength: 2, RequireClasses: 9}).normalized(); got != d {
		t.Fatalf("malformed policy was not repaired: %#v", got)
	}
	if got := (passwordPolicy{MinLength: 200, RequireClasses: -1}).normalized(); got != d {
		t.Fatalf("out-of-range policy was not repaired: %#v", got)
	}
	if got := (passwordPolicy{MinLength: 16, RequireClasses: 4}).normalized(); got.MinLength != 16 || got.RequireClasses != 4 {
		t.Fatalf("a valid policy was overwritten: %#v", got)
	}
}
