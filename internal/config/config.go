package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"
)

const (
	// bcryptMaxPasswordBytes mirrors the hard limit in
	// golang.org/x/crypto/bcrypt.
	bcryptMaxPasswordBytes = 72
	// minBootstrapPasswordCharacters matches the default `security.password`
	// policy applied to every other account.
	minBootstrapPasswordCharacters = 10
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
	// bcrypt refuses anything longer than 72 bytes, which a 24-character Korean
	// passphrase already exceeds. Reject it here with an actionable message
	// instead of failing later with a raw hashing error.
	if len(c.BootstrapAdminPassword) > bcryptMaxPasswordBytes {
		return Config{}, fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must be at most %d bytes (got %d); note that one Korean character is 3 bytes", bcryptMaxPasswordBytes, len(c.BootstrapAdminPassword))
	}
	// A weak bootstrap password is only a warning: it is used once, at account
	// creation, and refusing to start would break existing deployments on
	// upgrade. Operators rotate it from the profile screen.
	if characters := utf8.RuneCountInString(c.BootstrapAdminPassword); characters < minBootstrapPasswordCharacters {
		slog.Warn("BOOTSTRAP_ADMIN_PASSWORD is shorter than the recommended minimum",
			"characters", characters, "recommended_minimum", minBootstrapPasswordCharacters)
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
