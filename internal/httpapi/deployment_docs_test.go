package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// repoFile reads a file from the repository root, which is two directories up
// from this package.
func repoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// TestComposeDoesNotPinAnImageVersion keeps the offline install working.
//
// compose.yaml named vendra:v0.6.21 and stayed there for twenty-one releases,
// because nothing required updating it. An operator who loaded the release
// archive they were given and ran the documented `docker compose up -d` got
// Docker reaching for a registry to find a version that was never in the
// archive — which on an air-gapped machine is the end of the install.
func TestComposeDoesNotPinAnImageVersion(t *testing.T) {
	compose := repoFile(t, "compose.yaml")
	pinned := regexp.MustCompile(`(?m)^\s*image:\s*vendra:v[0-9][^\s]*`)
	if match := pinned.FindString(compose); match != "" {
		t.Errorf("compose.yaml names a specific version (%s); an archive of any other version will not satisfy it offline",
			strings.TrimSpace(match))
	}
	if !strings.Contains(compose, "vendra:latest") {
		t.Error("compose.yaml no longer falls back to vendra:latest, which is the tag every release archive carries")
	}
	// The release script has to put that tag in the archive, or the fallback
	// names an image the operator never received.
	script := repoFile(t, "scripts/offline-release.sh")
	if !strings.Contains(script, `docker save "$image" "$rolling"`) {
		t.Error("offline-release.sh no longer saves the rolling tag alongside the versioned one")
	}
}

// TestAdminGuidePublishesTheRealCallbackPath keeps the OIDC instructions honest.
//
// The guide told a Keycloak administrator to register
// /api/v1/auth/oidc/callback. That path is not the callback; it is inside the
// authenticated API, so a user coming back from a successful SSO login was
// answered "로그인이 필요합니다" — the least helpful response possible for
// somebody who had just logged in.
func TestAdminGuidePublishesTheRealCallbackPath(t *testing.T) {
	settings := oidcSettings{PublicURL: "https://vendra.internal"}
	actual := settings.callbackURI(httptest.NewRequest(http.MethodGet, "/api/auth/oidc/start", nil))
	path := strings.TrimPrefix(actual, "https://vendra.internal")

	guide := repoFile(t, "docs/ADMIN_GUIDE.md")
	if !strings.Contains(guide, actual) {
		t.Errorf("the admin guide does not publish %s, which is the redirect_uri the server actually sends", actual)
	}
	for _, wrong := range []string{"/api/v1/auth/oidc/callback", "/api/v1/auth/oidc/start"} {
		if strings.Contains(guide, wrong) {
			t.Errorf("the admin guide still tells an administrator to register %s; the callback is %s", wrong, path)
		}
	}

	// And the path it publishes is one the server serves outside the
	// authenticated API, so the answer is an OIDC message rather than a 401.
	handler := (&App{}).Handler()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code == http.StatusUnauthorized {
		t.Errorf("%s answers 401, so it is behind the session middleware and cannot be a callback", path)
	}
}
