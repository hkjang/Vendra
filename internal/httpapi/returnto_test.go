package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The post-sign-in redirect runs immediately after the session cookie is set,
// so a target outside this site lands the user on someone else's page
// believing Vendra sent them. A leading slash alone does not keep them here.
func TestSafeReturnToStaysOnThisSite(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"", "/"},
		{"/", "/"},
		{"/dashboard", "/dashboard"},
		{"/suppliers?tab=risk", "/suppliers?tab=risk"},
		{"/공급업체", "/%EA%B3%B5%EA%B8%89%EC%97%85%EC%B2%B4"},

		// A protocol-relative URL has the leading slash and leaves the site.
		{"//evil.com", "/"},
		{"//evil.com/path", "/"},
		{"///evil.com", "/"},
		// Browsers normalise a backslash here into a slash.
		{`/\evil.com`, "/"},
		{`/\/evil.com`, "/"},
		// Absolute and scheme-bearing targets.
		{"https://evil.com", "/"},
		{"http://evil.com", "/"},
		{"javascript:alert(1)", "/"},
		{"mailto:someone@evil.com", "/"},
		// Control characters a parser might strip before resolving.
		{"/\tevil", "/"},
		{"/\nevil", "/"},
		{"/\revil", "/"},
	} {
		t.Run(strings.ReplaceAll(tc.raw, "\n", `\n`), func(t *testing.T) {
			if got := safeReturnTo(tc.raw); got != tc.want {
				t.Errorf("safeReturnTo(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// What a browser is actually told, for the values that matter most.
func TestReturnToNeverProducesACrossOriginLocation(t *testing.T) {
	for _, raw := range []string{"//evil.com", "//evil.com/path", `/\evil.com`, "https://evil.com"} {
		r := httptest.NewRequest(http.MethodGet, "https://vendra.example/api/auth/oidc/callback", nil)
		w := httptest.NewRecorder()
		http.Redirect(w, r, safeReturnTo(raw), http.StatusFound)

		location := w.Header().Get("Location")
		if strings.HasPrefix(location, "//") || strings.Contains(location, "://") {
			t.Errorf("returnTo %q sent the browser to %q", raw, location)
		}
	}
}
