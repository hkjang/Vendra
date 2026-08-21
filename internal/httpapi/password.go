package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

// bcryptMaxBytes is the hard limit golang.org/x/crypto/bcrypt enforces. It
// matters for Korean input, where a 24-character passphrase already exceeds it.
const bcryptMaxBytes = 72

// passwordPolicy is the administrator-tunable `security.password` setting.
// RequireClasses counts how many of lowercase, uppercase, digit and symbol a
// password must contain; zero disables the requirement.
type passwordPolicy struct {
	MinLength      int `json:"minLength"`
	RequireClasses int `json:"requireClasses"`
}

func defaultPasswordPolicy() passwordPolicy {
	return passwordPolicy{MinLength: 10, RequireClasses: 0}
}

func (p passwordPolicy) normalized() passwordPolicy {
	d := defaultPasswordPolicy()
	if p.MinLength < 8 || p.MinLength > 64 {
		p.MinLength = d.MinLength
	}
	if p.RequireClasses < 0 || p.RequireClasses > 4 {
		p.RequireClasses = d.RequireClasses
	}
	return p
}

func (a *App) passwordPolicy(ctx context.Context) passwordPolicy {
	policy := defaultPasswordPolicy()
	var value []byte
	if a.db.QueryRow(ctx, `SELECT value FROM settings WHERE key='security.password'`).Scan(&value) == nil {
		_ = json.Unmarshal(value, &policy)
	}
	return policy.normalized()
}

var errPasswordPolicy = errors.New("password policy")

// validate reports why a password is unacceptable, in a message the UI can show
// directly. Length is counted in characters so that Korean input is measured
// the way a person counts it, while the bcrypt ceiling is counted in bytes
// because that is what the algorithm limits.
func (p passwordPolicy) validate(password string) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("%w: 비밀번호는 필수입니다", errPasswordPolicy)
	}
	if characters := len([]rune(password)); characters < p.MinLength {
		return fmt.Errorf("%w: 비밀번호는 %d자 이상이어야 합니다", errPasswordPolicy, p.MinLength)
	}
	if len(password) > bcryptMaxBytes {
		return fmt.Errorf("%w: 비밀번호가 너무 깁니다. 최대 %d바이트이며 한글은 한 글자가 3바이트입니다", errPasswordPolicy, bcryptMaxBytes)
	}
	if p.RequireClasses > 0 && characterClasses(password) < p.RequireClasses {
		return fmt.Errorf("%w: 영문 소문자, 대문자, 숫자, 특수문자 중 %d종류 이상을 포함해야 합니다", errPasswordPolicy, p.RequireClasses)
	}
	return nil
}

func characterClasses(password string) int {
	var lower, upper, digit, symbol bool
	for _, r := range password {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		default:
			symbol = true
		}
	}
	count := 0
	for _, present := range []bool{lower, upper, digit, symbol} {
		if present {
			count++
		}
	}
	return count
}

// hashPassword validates against the active policy and returns the bcrypt hash.
func (a *App) hashPassword(ctx context.Context, password string) (string, error) {
	if err := a.passwordPolicy(ctx).validate(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// writePasswordError maps a policy failure to a 400 the UI can display, and
// anything else to a 500 so an internal fault is not blamed on the user.
func writePasswordError(w http.ResponseWriter, err error) {
	if errors.Is(err, errPasswordPolicy) {
		_, message, _ := strings.Cut(err.Error(), ": ")
		writeError(w, 400, "weak_password", message)
		return
	}
	writeError(w, 500, "password_error", "비밀번호를 처리하지 못했습니다")
}
