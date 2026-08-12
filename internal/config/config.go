package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Config deliberately exposes only the four deployment-time inputs supported by
// Vendra. Every mutable operational setting belongs in the administrator UI and
// is persisted in PostgreSQL.
type Config struct {
	PostgresDSN            string
	BootstrapAdmin         string
	BootstrapAdminPassword string
	EncryptionKey          []byte
}

func Load() (Config, error) {
	c := Config{
		PostgresDSN:            strings.TrimSpace(os.Getenv("POSTGRES_DSN")),
		BootstrapAdmin:         strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN")),
		BootstrapAdminPassword: os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
	}
	if c.PostgresDSN == "" || c.BootstrapAdmin == "" || c.BootstrapAdminPassword == "" {
		return Config{}, errors.New("POSTGRES_DSN, BOOTSTRAP_ADMIN, and BOOTSTRAP_ADMIN_PASSWORD are required")
	}
	keyText := strings.TrimSpace(os.Getenv("ENCRYPTION_KEY"))
	if keyText == "" {
		return Config{}, errors.New("ENCRYPTION_KEY is required")
	}
	key, err := base64.StdEncoding.DecodeString(keyText)
	if err != nil || len(key) != 32 {
		return Config{}, fmt.Errorf("ENCRYPTION_KEY must be a base64-encoded 32-byte key")
	}
	c.EncryptionKey = key
	return c, nil
}
