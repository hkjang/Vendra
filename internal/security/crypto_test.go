package security

import (
	"strings"
	"testing"
)

func TestVaultRoundTripAndTamperDetection(t *testing.T) {
	v, err := NewVault([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := v.Encrypt("sensitive-bank-account")
	if err != nil {
		t.Fatal(err)
	}
	if cipher == "sensitive-bank-account" || !strings.HasPrefix(cipher, "enc:v1:") {
		t.Fatalf("not encrypted: %q", cipher)
	}
	plain, err := v.Decrypt(cipher)
	if err != nil || plain != "sensitive-bank-account" {
		t.Fatalf("round trip failed: %q %v", plain, err)
	}
	tampered := cipher[:len(cipher)-1] + "A"
	if _, err = v.Decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext should fail")
	}
}

func TestTokenHashIsStableAndDoesNotExposeToken(t *testing.T) {
	a := TokenHash("vnd_secret")
	b := TokenHash("vnd_secret")
	if string(a) != string(b) {
		t.Fatal("hash is not stable")
	}
	if strings.Contains(string(a), "secret") {
		t.Fatal("hash exposes token")
	}
}
