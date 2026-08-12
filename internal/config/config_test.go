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
