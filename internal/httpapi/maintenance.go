package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
)

// retentionPolicy bounds how long expired operational rows are kept. A value of
// zero disables that sweep, so an administrator can opt out of any deletion.
type retentionPolicy struct {
	ExpiredSessionDays int `json:"expiredSessionDays"`
	LoginAttemptDays   int `json:"loginAttemptDays"`
}

func defaultRetentionPolicy() retentionPolicy {
	return retentionPolicy{ExpiredSessionDays: 7, LoginAttemptDays: 30}
}

func (p retentionPolicy) normalized() retentionPolicy {
	clamp := func(days, fallback int) int {
		if days < 0 {
			return fallback
		}
		if days > 3650 {
			return 3650
		}
		return days
	}
	d := defaultRetentionPolicy()
	p.ExpiredSessionDays = clamp(p.ExpiredSessionDays, d.ExpiredSessionDays)
	p.LoginAttemptDays = clamp(p.LoginAttemptDays, d.LoginAttemptDays)
	return p
}

func (a *App) retentionPolicy(ctx context.Context) retentionPolicy {
	policy := defaultRetentionPolicy()
	var value []byte
	if a.db.QueryRow(ctx, `SELECT value FROM settings WHERE key='maintenance.retention'`).Scan(&value) == nil {
		_ = json.Unmarshal(value, &policy)
	}
	return policy.normalized()
}

// purgeExpired keeps operational tables from growing without bound. Expired
// sessions can no longer authenticate anyone, so removing them frees storage
// without changing behaviour. Only rows that are already expired are touched,
// and a retention of zero disables the sweep entirely.
func (a *App) purgeExpired(ctx context.Context) error {
	policy := a.retentionPolicy(ctx)
	sweeps := []struct {
		name string
		days int
		sql  string
	}{
		{"sessions", policy.ExpiredSessionDays, `DELETE FROM sessions WHERE expires_at < now()-make_interval(days => $1)`},
		{"login_attempts", policy.LoginAttemptDays, `DELETE FROM login_attempts WHERE created_at < now()-make_interval(days => $1)`},
	}
	for _, sweep := range sweeps {
		if sweep.days <= 0 {
			continue
		}
		tag, err := a.db.Exec(ctx, sweep.sql, sweep.days)
		if err != nil {
			return err
		}
		if removed := tag.RowsAffected(); removed > 0 {
			slog.Info("retention sweep", "table", sweep.name, "removed", removed, "older_than_days", sweep.days)
		}
	}
	return nil
}
