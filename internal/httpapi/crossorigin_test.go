package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// SameSite=Lax stops a genuinely cross-site POST, but "site" is the registrable
// domain. On an intranet where Vendra sits beside other tools on one domain, a
// page on a sibling host is same-site and the session cookie rides along.
func TestCrossOriginWrite(t *testing.T) {
	for _, tc := range []struct {
		name          string
		method        string
		host          string
		origin        string
		fetchSite     string
		forwardedHost string
		cookie        bool
		want          bool
	}{
		{name: "the app's own page", method: "POST", host: "vendra.corp.example", origin: "https://vendra.corp.example", fetchSite: "same-origin", cookie: true},
		{name: "a sibling host on the same domain", method: "POST", host: "vendra.corp.example", origin: "https://wiki.corp.example", fetchSite: "same-site", cookie: true, want: true},
		{name: "another site entirely", method: "POST", host: "vendra.corp.example", origin: "https://evil.example", fetchSite: "cross-site", cookie: true, want: true},
		{name: "a sibling host without Sec-Fetch-Site", method: "POST", host: "vendra.corp.example", origin: "https://wiki.corp.example", cookie: true, want: true},
		{name: "a different port is a different origin", method: "POST", host: "vendra.corp.example", origin: "https://vendra.corp.example:8443", cookie: true, want: true},
		{name: "typed into the address bar", method: "POST", host: "vendra.corp.example", fetchSite: "none", cookie: true},
		{name: "behind a proxy that rewrote Host", method: "POST", host: "vendra-internal:8080", origin: "https://vendra.corp.example", forwardedHost: "vendra.corp.example", cookie: true},
		{name: "a forwarded name that still does not match", method: "POST", host: "vendra-internal:8080", origin: "https://evil.example", forwardedHost: "vendra.corp.example", cookie: true, want: true},

		// Reads are not what CSRF turns into damage, and every state-changing
		// GET in this service authenticates by token rather than by cookie.
		{name: "a read from anywhere", method: "GET", host: "vendra.corp.example", origin: "https://evil.example", fetchSite: "cross-site", cookie: true},

		// An API key is not an ambient credential, so nothing rides along.
		{name: "an API key from a script", method: "POST", host: "vendra.corp.example", origin: "https://evil.example", fetchSite: "cross-site"},
		{name: "a script with no browser headers", method: "POST", host: "vendra.corp.example", cookie: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "https://"+tc.host+"/api/v1/suppliers", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				r.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			if tc.forwardedHost != "" {
				r.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			}
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "a-session"})
			}
			if got := crossOriginWrite(r); got != tc.want {
				t.Errorf("crossOriginWrite = %v, want %v", got, tc.want)
			}
		})
	}
}

// The middleware refuses before it looks at who the caller is.
func TestMiddlewareRefusesACrossOriginWrite(t *testing.T) {
	a := authService{db: unreachablePool(t)}
	guarded := a.middleware(true, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the handler ran for a request from another origin")
	}))

	r := httptest.NewRequest(http.MethodPost, "https://vendra.corp.example/api/v1/suppliers", nil)
	r.Host = "vendra.corp.example"
	r.Header.Set("Origin", "https://wiki.corp.example")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "a-session"})
	w := httptest.NewRecorder()
	guarded.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if got := errorCode(t, w.Body.String()); got != "cross_origin" {
		t.Errorf("code = %q, want cross_origin", got)
	}
}
