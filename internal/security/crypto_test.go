package security

import (
	"encoding/base64"
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
	// The tamper has to change the ciphertext bytes, not just the text that
	// encodes them. Replacing the last character with a fixed one left the
	// value untouched whenever it already ended in that character — about one
	// run in nineteen — and base64's final group carries spare bits, so even a
	// different character there can decode to the same bytes. Flipping a byte
	// after decoding is unambiguous.
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cipher, "enc:v1:"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, at := range []int{0, len(sealed) / 2, len(sealed) - 1} {
		altered := make([]byte, len(sealed))
		copy(altered, sealed)
		altered[at] ^= 0x01
		tampered := "enc:v1:" + base64.RawURLEncoding.EncodeToString(altered)
		if tampered == cipher {
			t.Fatalf("byte %d was not altered", at)
		}
		if _, err := v.Decrypt(tampered); err == nil {
			t.Errorf("a ciphertext with byte %d altered was accepted", at)
		}
	}
	// A truncated value must not be accepted either.
	if _, err := v.Decrypt("enc:v1:" + base64.RawURLEncoding.EncodeToString(sealed[:len(sealed)-1])); err == nil {
		t.Error("a truncated ciphertext was accepted")
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
