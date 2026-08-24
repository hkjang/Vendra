package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// loginProtection throttles credential stuffing against a single account and
// against a single source address. Administrators tune it from the
// `security.login` setting; setting a limit to zero disables that dimension.
type loginProtection struct {
	MaxFailures        int `json:"maxFailures"`
	WindowMinutes      int `json:"windowMinutes"`
	LockoutMinutes     int `json:"lockoutMinutes"`
	MaxAddressFailures int `json:"maxAddressFailures"`
}

func defaultLoginProtection() loginProtection {
	return loginProtection{MaxFailures: 5, WindowMinutes: 15, LockoutMinutes: 15, MaxAddressFailures: 25}
}

// normalized clamps administrator input so a malformed setting can never lock
// every account out permanently, nor silently disable the guard.
func (p loginProtection) normalized() loginProtection {
	d := defaultLoginProtection()
	if p.MaxFailures < 0 {
		p.MaxFailures = d.MaxFailures
	}
	if p.MaxFailures > 1000 {
		p.MaxFailures = 1000
	}
	if p.MaxAddressFailures < 0 {
		p.MaxAddressFailures = d.MaxAddressFailures
	}
	if p.MaxAddressFailures > 10000 {
		p.MaxAddressFailures = 10000
	}
	if p.WindowMinutes < 1 || p.WindowMinutes > 1440 {
		p.WindowMinutes = d.WindowMinutes
	}
	if p.LockoutMinutes < 1 || p.LockoutMinutes > 1440 {
		p.LockoutMinutes = d.LockoutMinutes
	}
	return p
}

func (p loginProtection) window() time.Duration {
	return time.Duration(p.WindowMinutes) * time.Minute
}

func (p loginProtection) lockout() time.Duration {
	return time.Duration(p.LockoutMinutes) * time.Minute
}

// retryAfter reports how long a subject with the given failure history stays
// locked, and whether it is locked at all.
func (p loginProtection) retryAfter(failures int, last time.Time, limit int, now time.Time) (time.Duration, bool) {
	if limit <= 0 || failures < limit || last.IsZero() {
		return 0, false
	}
	remaining := last.Add(p.lockout()).Sub(now)
	if remaining <= 0 {
		return 0, false
	}
	return remaining, true
}

func (a authService) loginProtection(ctx context.Context) loginProtection {
	settings := defaultLoginProtection()
	var value []byte
	if a.db.QueryRow(ctx, `SELECT value FROM settings WHERE key='security.login'`).Scan(&value) == nil {
		_ = json.Unmarshal(value, &settings)
	}
	return settings.normalized()
}

type failureWindow struct {
	count int
	last  time.Time
}

// recentFailures counts failed attempts inside the protection window for the
// account and for the source address in a single round trip.
func recentFailures(ctx context.Context, db *pgxpool.Pool, email string, ip any, window time.Duration) (account, address failureWindow, err error) {
	var accountLast, addressLast *time.Time
	err = db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE email=$1), max(created_at) FILTER (WHERE email=$1),
		       count(*) FILTER (WHERE $2::inet IS NOT NULL AND ip=$2::inet), max(created_at) FILTER (WHERE $2::inet IS NOT NULL AND ip=$2::inet)
		FROM login_attempts
		WHERE NOT succeeded AND created_at > now()-make_interval(secs => $3)
		  AND (email=$1 OR ($2::inet IS NOT NULL AND ip=$2::inet))`,
		email, ip, window.Seconds()).Scan(&account.count, &accountLast, &address.count, &addressLast)
	if err != nil {
		return failureWindow{}, failureWindow{}, err
	}
	if accountLast != nil {
		account.last = *accountLast
	}
	if addressLast != nil {
		address.last = *addressLast
	}
	return account, address, nil
}

func recordLoginAttempt(ctx context.Context, db *pgxpool.Pool, email string, ip any, userAgent string, succeeded bool) {
	_, _ = db.Exec(ctx, `INSERT INTO login_attempts(email,ip,user_agent,succeeded) VALUES($1,$2,$3,$4)`, email, ip, userAgent, succeeded)
}

func writeLoginLocked(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter.Round(time.Second).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	minutes := (seconds + 59) / 60
	writeError(w, http.StatusTooManyRequests, "too_many_attempts",
		"로그인 시도가 너무 많습니다. "+strconv.Itoa(minutes)+"분 후에 다시 시도하세요")
}

// clientIP returns the peer address without its port. Proxy headers are not
// trusted because they are attacker-controlled on a direct connection.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return strings.Trim(strings.TrimSpace(host), "[]")
}

// clientIPValue yields a value suitable for a PostgreSQL inet column, or nil
// when the peer address is not a usable IP (unix socket, test server, proxy).
func clientIPValue(r *http.Request) any {
	addr, err := netip.ParseAddr(clientIP(r))
	if err != nil {
		return nil
	}
	return addr.Unmap()
}

// crossOriginWrite reports a state-changing request that a browser sent from
// another origin while carrying this site's session cookie.
//
// SameSite=Lax stops a genuinely cross-site POST, but "site" is the registrable
// domain, not the origin. On an intranet where Vendra sits beside other tools
// on one domain, a page on a sibling host is same-site: the cookie rides along,
// and so does anything that host is tricked into sending. A browser always
// attaches Origin to a state-changing request and cannot be made to omit it, so
// comparing it closes that gap. Callers that authenticate with an API key carry
// no ambient credential and are left alone, as are requests with neither header
// — those are not browsers.
func crossOriginWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if _, err := r.Cookie(sessionCookie); err != nil {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return false
	case "same-site", "cross-site":
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return true
	}
	// Behind a proxy that rewrites Host, the browser's origin names the public
	// address while r.Host names the internal one. Accepting the forwarded name
	// costs nothing here: a page that adds a header to a cross-origin request
	// turns it into a preflighted one, and this service answers no preflight,
	// so the browser never sends it. A caller that can set headers freely is
	// not a browser and carries no cookie of anyone else's.
	for _, host := range []string{r.Host, r.Header.Get("X-Forwarded-Host")} {
		if host != "" && strings.EqualFold(parsed.Host, host) {
			return false
		}
	}
	return true
}
