package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestCallbackURIPrefersConfiguration(t *testing.T) {
	// The request the two legs see is not the same request, and need not carry
	// the same headers.
	start := httptest.NewRequest("GET", "http://vendra.internal:8080/api/auth/oidc/start", nil)
	start.Host = "vendra.internal:8080"
	callback := httptest.NewRequest("GET", "http://10.0.0.7/api/auth/oidc/callback", nil)
	callback.Host = "10.0.0.7"
	callback.Header.Set("X-Forwarded-Proto", "https")

	configured := oidcSettings{PublicURL: "https://vendra.corp"}
	if got, want := configured.callbackURI(start), "https://vendra.corp/api/auth/oidc/callback"; got != want {
		t.Errorf("start leg = %q, want %q", got, want)
	}
	if configured.callbackURI(start) != configured.callbackURI(callback) {
		t.Errorf("the two legs disagree: %q vs %q", configured.callbackURI(start), configured.callbackURI(callback))
	}

	// Without it, each leg answers from its own request — which is the
	// redirect_uri_mismatch this setting exists to prevent.
	var unset oidcSettings
	if unset.callbackURI(start) == unset.callbackURI(callback) {
		t.Fatal("the fallback happened to agree; this test no longer shows what it means to")
	}
	if got, want := unset.callbackURI(start), "http://vendra.internal:8080/api/auth/oidc/callback"; got != want {
		t.Errorf("fallback = %q, want %q", got, want)
	}
}

func TestCallbackURIAcceptsWhatAnOperatorWillType(t *testing.T) {
	r := httptest.NewRequest("GET", "http://fallback.local/x", nil)
	r.Host = "fallback.local"
	const fallback = "http://fallback.local/api/auth/oidc/callback"

	for _, tc := range []struct{ configured, want string }{
		{"https://vendra.corp", "https://vendra.corp/api/auth/oidc/callback"},
		{"https://vendra.corp/", "https://vendra.corp/api/auth/oidc/callback"},
		{"  https://vendra.corp//  ", "https://vendra.corp/api/auth/oidc/callback"},
		{"https://vendra.corp:8443", "https://vendra.corp:8443/api/auth/oidc/callback"},
		{"http://vendra.internal", "http://vendra.internal/api/auth/oidc/callback"},
		// A service mounted under a path keeps it.
		{"https://intranet.corp/vendra", "https://intranet.corp/vendra/api/auth/oidc/callback"},
		// Nothing usable falls back rather than sending the provider a URI it
		// will refuse.
		{"", fallback},
		{"   ", fallback},
		{"vendra.corp", fallback},
		{"ftp://vendra.corp", fallback},
		{"javascript:alert(1)", fallback},
		{"https://", fallback},
	} {
		s := oidcSettings{PublicURL: tc.configured}
		if got := s.callbackURI(r); got != tc.want {
			t.Errorf("publicUrl %q gave %q, want %q", tc.configured, got, tc.want)
		}
	}
}
