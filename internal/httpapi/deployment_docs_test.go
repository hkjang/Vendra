package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hkjang/Vendra/internal/mail"
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

// TestAdminGuideDescribesSilentSignInAsTheCodeDoesIt holds the auto-login
// section to the two names an administrator will meet: the setting key they
// look for in the JSON, and the address a refused attempt lands on, which is
// what they will see in the browser bar when asked "why did I get the login
// screen".
func TestAdminGuideDescribesSilentSignInAsTheCodeDoesIt(t *testing.T) {
	guide := repoFile(t, "docs/ADMIN_GUIDE.md")
	key, _ := reflect.TypeOf(oidcSettings{}).FieldByName("AutoLogin")
	setting := strings.Split(key.Tag.Get("json"), ",")[0]
	for _, name := range []string{"`" + setting + "`", "`" + silentRefusalPath + "`", "`prompt=none`"} {
		if !strings.Contains(guide, name) {
			t.Errorf("the admin guide does not mention %s", name)
		}
	}
	if (oidcSettings{}).AutoLogin {
		t.Error("auto-login is on when the key is absent; the guide promises the opposite")
	}
}

// TestAdminGuideNamesEveryMailSettingAndTheDefaultIsOff holds the guide's
// 3.6 table to the rows the migration installs: a mail key added to the code
// without a line in the table is one an operator cannot find, and the whole
// point of standard names is that they are written down. The "off by default"
// promise is checked against the parsed defaults, not the prose.
func TestAdminGuideNamesEveryMailSettingAndTheDefaultIsOff(t *testing.T) {
	guide := repoFile(t, "docs/ADMIN_GUIDE.md")
	for _, setting := range mail.Settings {
		if !strings.Contains(guide, "`"+setting.Key+"`") {
			t.Errorf("the admin guide does not list %s", setting.Key)
		}
	}
	migration := repoFile(t, "internal/db/migrations/018_mail.sql")
	for _, setting := range mail.Settings {
		if !strings.Contains(migration, "('"+setting.Key+"',") {
			t.Errorf("the migration does not install %s", setting.Key)
		}
	}
	if config := mail.Parse(nil); config.Enabled || config.Port != mail.DefaultPort || config.Username != "" || config.Security != mail.SecurityAuto {
		t.Errorf("the defaults are %+v; the guide promises off, port 25, no credentials, auto", config)
	}
	for _, route := range []string{"/api/v1/admin/mail/test", "/api/v1/admin/mail/deliveries"} {
		if !strings.Contains(guide, route+"`") {
			t.Errorf("the admin guide does not name %s", route)
		}
	}
}

// TestCIRunsEveryDocumentedTestDatabase keeps the integration harnesses from
// going quiet.
//
// A harness that needs a DSN skips without one, and a skip is indistinguishable
// from a pass in a green build. The README names three VENDRA_TEST_* databases
// and describes what each verifies; CI set one, so the migration-concurrency
// and populated-upgrade harnesses — the two that cover the riskiest things this
// product does — never ran once.
func TestCIRunsEveryDocumentedTestDatabase(t *testing.T) {
	documented := regexp.MustCompile(`VENDRA_TEST_[A-Z_]+`)
	readme := repoFile(t, "README.md")
	workflow := repoFile(t, ".github/workflows/ci.yml")

	named := map[string]bool{}
	for _, name := range documented.FindAllString(readme, -1) {
		named[name] = true
	}
	if len(named) < 2 {
		t.Fatalf("the README names %d test databases, so this comparison proves nothing", len(named))
	}
	for name := range named {
		if !strings.Contains(workflow, name+":") {
			t.Errorf("the README says %s verifies part of the product, but CI never sets it, so that harness silently skips", name)
		}
	}
}
