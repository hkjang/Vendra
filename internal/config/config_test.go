package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadOnlyRequiredDeploymentInputs(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://example/vendra")
	t.Setenv("BOOTSTRAP_ADMIN", "admin@example.com")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "a-secure-bootstrap-password")
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.BootstrapAdmin != "admin@example.com" || len(c.EncryptionKey) != 32 {
		t.Fatalf("unexpected config: %#v", c)
	}
}

func TestLoadRejectsInvalidEncryptionKey(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://example/vendra")
	t.Setenv("BOOTSTRAP_ADMIN", "admin@example.com")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "password")
	t.Setenv("ENCRYPTION_KEY", "short")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid encryption key error")
	}
}

func TestLoadRejectsBootstrapPasswordBcryptCannotHash(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://example/vendra")
	t.Setenv("BOOTSTRAP_ADMIN", "admin@example.com")
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	// 25 Korean characters are 75 bytes, past the bcrypt limit. Without this
	// check the application starts and then dies while hashing.
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", strings.Repeat("가", 25))
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error for a password bcrypt cannot hash")
	}
	if !strings.Contains(err.Error(), "BOOTSTRAP_ADMIN_PASSWORD") {
		t.Fatalf("error does not name the variable to fix: %v", err)
	}
	// 24 Korean characters are 72 bytes, which bcrypt accepts.
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", strings.Repeat("가", 24))
	if _, err := Load(); err != nil {
		t.Fatalf("a 72-byte password was rejected: %v", err)
	}
}

func TestLoadWarnsButAcceptsShortBootstrapPassword(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://example/vendra")
	t.Setenv("BOOTSTRAP_ADMIN", "admin@example.com")
	t.Setenv("ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "short")
	if _, err := Load(); err != nil {
		t.Fatalf("an existing deployment with a short password must still start: %v", err)
	}
}
