package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hkjang/Vendra/internal/tracking"
)

// The page shell a Vite build produces, reduced to the two tags a snippet is
// placed before.
const testIndexHTML = "<!doctype html><html><head><title>Vendra</title></head><body><div id=root></div><script type=module src=/assets/index-abc123.js></script></body></html>"

var noncePattern = regexp.MustCompile(`'nonce-([A-Za-z0-9_-]+)'`)

// TestAFreshInstallationServesThePolicyItAlwaysDid is the "nothing changes
// until an administrator turns it on" guarantee: with no database at all the
// entry document is untouched and the policy header is byte for byte the one
// shipped before tracking existed.
func TestAFreshInstallationServesThePolicyItAlwaysDid(t *testing.T) {
	app := newSPAApp(t)
	page := serveSPAPath(t, app, "/suppliers")
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "<script") {
		t.Fatalf("status=%d body=%s", page.Code, page.Body.String())
	}
	if got := page.Header().Get("Content-Security-Policy"); got != pagePolicy {
		t.Errorf("policy = %q, want the shipped one", got)
	}
	handler := app.Handler()
	for _, path := range []string{"/", "/suppliers", "/admin/general", "/login"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		policy := w.Header().Get("Content-Security-Policy")
		if policy != pagePolicy || strings.Contains(policy, "nonce") || strings.Contains(policy, "report-uri") {
			t.Errorf("%s: policy = %q", path, policy)
		}
	}
	// Non-page paths carry a policy under which nothing loads.
	for _, path := range []string{"/api/version", "/api/v1/me", "/health/live", "/mcp", tracking.ProxyPath + "/tracker.js"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if got := w.Header().Get("Content-Security-Policy"); got != nonPagePolicy {
			t.Errorf("%s: policy = %q, want %q", path, got, nonPagePolicy)
		}
	}
	// And the collector proxy is closed.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tracking.ProxyPath+"/tracker.js", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("the momento proxy answered %d while tracking is off", w.Code)
	}
}

// TestTheSnippetAndThePolicyShareOneNonce holds the two halves together: every
// script tag on the page carries the nonce the header names, the origins the
// snippet needs are in the header, and 'unsafe-inline' never is.
func TestTheSnippetAndThePolicyShareOneNonce(t *testing.T) {
	config := tracking.Config{Enabled: true, Provider: tracking.ProviderCustom, Placement: "body", AllowedHosts: "https://extra.example",
		CustomSnippet: `<script async src="https://cdn.tracker.example/t.js" data-endpoint="https://collect.tracker.example/v1"></script><script>window.t=1</script>`}
	page, policy, err := decoratePage(config, "/suppliers", []byte(testIndexHTML))
	if err != nil {
		t.Fatal(err)
	}
	match := noncePattern.FindStringSubmatch(policy)
	if match == nil {
		t.Fatalf("policy names no nonce: %s", policy)
	}
	nonce := match[1]
	body := string(page)
	if got := strings.Count(body, `nonce="`+nonce+`"`); got != 2 {
		t.Errorf("%d script tags carry the nonce %s, want 2:\n%s", got, nonce, body)
	}
	if !strings.Contains(body, `</script>\n</body>`) && !strings.HasSuffix(strings.TrimSpace(body), "</body></html>") {
		t.Errorf("placement=body did not put the snippet before </body>:\n%s", body)
	}
	if index := strings.Index(body, "cdn.tracker.example"); index < strings.Index(body, "</head>") {
		t.Errorf("placement=body put the snippet in the head:\n%s", body)
	}
	for _, directive := range []string{
		"script-src 'self' 'nonce-" + nonce + "' https://cdn.tracker.example https://collect.tracker.example https://extra.example",
		"connect-src 'self' https://cdn.tracker.example https://collect.tracker.example https://extra.example",
		"img-src 'self' data: https://cdn.tracker.example https://collect.tracker.example https://extra.example",
		"report-uri " + cspReportPath,
		"style-src 'self' 'unsafe-inline'",
	} {
		if !strings.Contains(policy, directive) {
			t.Errorf("policy lacks %q:\n%s", directive, policy)
		}
	}
	scripts := policy[strings.Index(policy, "script-src"):]
	scripts = scripts[:strings.Index(scripts, ";")]
	if strings.Contains(scripts, "unsafe-inline") {
		t.Errorf("script-src was loosened with 'unsafe-inline': %s", scripts)
	}
	// A second request gets a second nonce; a fixed one would be a password
	// written on the wall.
	_, again, _ := decoratePage(config, "/suppliers", []byte(testIndexHTML))
	if noncePattern.FindStringSubmatch(again)[1] == nonce {
		t.Error("two requests received the same nonce")
	}
	// Head placement lands before </head>.
	config.Placement = "head"
	page, _, _ = decoratePage(config, "/suppliers", []byte(testIndexHTML))
	if body := string(page); strings.Index(body, "cdn.tracker.example") > strings.Index(body, "</head>") {
		t.Errorf("placement=head did not put the snippet in the head:\n%s", body)
	}
	// Administrative pages are left alone, with the strict policy, unless
	// asked for.
	page, policy, _ = decoratePage(config, "/admin/general", []byte(testIndexHTML))
	if string(page) != testIndexHTML || policy != pagePolicy {
		t.Errorf("admin page: policy=%s body=%s", policy, page)
	}
	config.IncludeAdmin = true
	if page, _, _ = decoratePage(config, "/admin/general", []byte(testIndexHTML)); string(page) == testIndexHTML {
		t.Error("includeAdmin did not include the admin page")
	}
	// Momento through the proxy: the snippet is on the page, the policy names
	// no external origin at all.
	momento := tracking.Config{Enabled: true, Provider: tracking.ProviderMomento, MomentoURL: "https://momento.corp.example", MomentoSiteID: "vendra", MomentoProxy: true}
	page, policy, _ = decoratePage(momento, "/", []byte(testIndexHTML))
	if !strings.Contains(string(page), `src="/momento/tracker.js"`) {
		t.Errorf("momento snippet missing:\n%s", page)
	}
	if strings.Contains(policy, "momento.corp.example") || !strings.Contains(policy, "connect-src 'self';") {
		t.Errorf("through the proxy the collector must not appear in the policy: %s", policy)
	}
}

func TestBrowserReportsAreRecordedAndNeverAnsweredWithAnError(t *testing.T) {
	app := &App{violations: tracking.NewRecorder()}
	handler := app.Handler()
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, cspReportPath, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/csp-report")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	report := `{"csp-report":{"blocked-uri":"https://collect.tracker.example/v1/events","effective-directive":"connect-src","document-uri":"https://vendra.internal/suppliers?q=acme"}}`
	if w := post(report); w.Code != http.StatusNoContent {
		t.Fatalf("report answered %d", w.Code)
	}
	post(report)
	for _, broken := range []string{"", "not json", `{"csp-report":{"blocked-uri":"inline"}}`, strings.Repeat("x", maxReportBytes*2)} {
		if w := post(broken); w.Code != http.StatusNoContent {
			t.Errorf("a broken report answered %d", w.Code)
		}
	}
	items := app.violations.List(tracking.Config{})
	if len(items) != 1 || items[0].Origin != "https://collect.tracker.example" || items[0].Directive != "connect-src" || items[0].Count != 2 || items[0].Page != "/suppliers" {
		t.Errorf("recorded = %+v", items)
	}
	// The endpoint is reachable without a session: the login page reports too.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, cspReportPath, strings.NewReader(report)))
	if w.Code == http.StatusUnauthorized {
		t.Error("the report endpoint is behind the session middleware")
	}
}

// TestTheCollectorProxyCarriesNoSessionAcross checks the one thing a same-origin
// proxy must not do: hand the browser's Vendra session to the collector, or the
// collector's cookies to the browser.
func TestTheCollectorProxyCarriesNoSessionAcross(t *testing.T) {
	var seen struct {
		path, cookie, authorization, host, body string
	}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen.path, seen.cookie, seen.authorization, seen.host, seen.body = r.URL.Path, r.Header.Get("Cookie"), r.Header.Get("Authorization"), r.Host, string(body)
		w.Header().Set("Set-Cookie", "collector=1")
		w.Header().Set("Content-Security-Policy", "default-src *")
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write([]byte("tracker()"))
	}))
	defer collector.Close()
	config := tracking.Config{Enabled: true, Provider: tracking.ProviderMomento, MomentoURL: collector.URL + "/base/", MomentoSiteID: "vendra", MomentoProxy: true}

	r := httptest.NewRequest(http.MethodPost, tracking.ProxyPath+"/collect/v1/events", strings.NewReader(`{"event":"page_view"}`))
	r.Host = "vendra.internal"
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "secret-session"})
	r.Header.Set("Authorization", "Bearer vnd_secret")
	w := httptest.NewRecorder()
	proxyToCollector(config, w, r)
	if w.Code != http.StatusOK || w.Body.String() != "tracker()" {
		t.Fatalf("proxy answered %d %s", w.Code, w.Body.String())
	}
	if seen.path != "/base/collect/v1/events" {
		t.Errorf("collector saw path %q", seen.path)
	}
	if seen.cookie != "" || seen.authorization != "" {
		t.Errorf("the session crossed to the collector: cookie=%q authorization=%q", seen.cookie, seen.authorization)
	}
	if seen.body != `{"event":"page_view"}` {
		t.Errorf("collector saw body %q", seen.body)
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Content-Security-Policy") != "" {
		t.Errorf("the collector's headers reached the browser: %v", w.Header())
	}
	// Off, or pointed at nothing, the path does not exist.
	for _, off := range []tracking.Config{{}, {Enabled: true, Provider: tracking.ProviderMomento, MomentoURL: collector.URL, MomentoSiteID: "v", MomentoProxy: false}, {Enabled: true, Provider: tracking.ProviderGA4, MeasurementID: "G-1"}} {
		w := httptest.NewRecorder()
		proxyToCollector(off, w, httptest.NewRequest(http.MethodGet, tracking.ProxyPath+"/tracker.js", nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%+v: proxy answered %d, want 404", off, w.Code)
		}
	}
	// A collector that is down is a 502, not a hang and not a 500.
	collector.Close()
	w = httptest.NewRecorder()
	proxyToCollector(config, w, httptest.NewRequest(http.MethodGet, tracking.ProxyPath+"/tracker.js", nil))
	if w.Code != http.StatusBadGateway {
		t.Errorf("a dead collector answered %d", w.Code)
	}
}

// TestTrackingIsOffUntilAnAdministratorTurnsItOn walks the whole thing through
// the API against PostgreSQL: the stored default, what the settings endpoint
// refuses, what turning it on does to the page and the policy, the report a
// browser sends, the one-click allow, and the policy going back to exactly
// what it was when tracking is turned off again.
func TestTrackingIsOffUntilAnAdministratorTurnsItOn(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(app.staticDir, "index.html"), []byte(testIndexHTML), 0o644); err != nil {
		t.Fatal(err)
	}
	var original []byte
	if err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, tracking.SettingKey).Scan(&original); err != nil {
		t.Fatalf("the migration did not seed the tracking row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE settings SET value=$2 WHERE key=$1`, tracking.SettingKey, original)
		app.violations.Forget()
	})
	if seeded := tracking.Parse(original); seeded.Enabled || seeded.Provider != tracking.ProviderMomento || !seeded.MomentoProxy {
		t.Errorf("the seeded row is %+v, want off, momento, through the proxy", seeded)
	}
	handler := app.Handler()
	token := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "10.9.0.1:4000"))
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	send := func(method, path string, payload any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(payload)
		r := httptest.NewRequest(method, path, strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	putTracking := func(value map[string]any) *httptest.ResponseRecorder {
		return send(http.MethodPut, "/api/v1/admin/settings/"+tracking.SettingKey, map[string]any{"value": value, "category": tracking.SettingCategory})
	}

	// Off: the page is bare and the policy is the shipped one.
	page := get("/suppliers")
	if strings.Contains(page.Body.String(), "nonce=") || page.Header().Get("Content-Security-Policy") != pagePolicy {
		t.Fatalf("with tracking off: policy=%s body=%s", page.Header().Get("Content-Security-Policy"), page.Body.String())
	}

	// What cannot be stored is refused with the box named.
	for name, value := range map[string]map[string]any{
		"an oversized snippet":  {"enabled": true, "provider": "custom", "customSnippet": "<script>" + strings.Repeat("x", tracking.MaxSnippetBytes) + "</script>"},
		"an unknown provider":   {"enabled": true, "provider": "pixel"},
		"momento without an id": {"enabled": true, "provider": "momento", "momentoUrl": "https://momento.corp.example"},
	} {
		if w := putTracking(value); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "validation_error") {
			t.Errorf("%s was answered %d %s", name, w.Code, w.Body.String())
		}
	}
	if stored := app.trackingConfig(ctx); stored.Enabled {
		t.Fatal("a refused configuration was stored")
	}

	// On, Momento through the proxy: the page carries the snippet under the
	// nonce the policy names, and no external origin appears anywhere.
	if w := putTracking(map[string]any{"enabled": true, "provider": "momento", "momentoUrl": "https://momento.corp.example/", "momentoSiteId": "vendra-prd", "momentoProxy": true}); w.Code != http.StatusOK {
		t.Fatalf("turning tracking on answered %d %s", w.Code, w.Body.String())
	}
	page = get("/suppliers")
	policy := page.Header().Get("Content-Security-Policy")
	match := noncePattern.FindStringSubmatch(policy)
	if match == nil {
		t.Fatalf("policy names no nonce: %s", policy)
	}
	if body := page.Body.String(); !strings.Contains(body, `src="/momento/tracker.js"`) || !strings.Contains(body, `nonce="`+match[1]+`"`) || !strings.Contains(body, `data-site-id="vendra-prd"`) {
		t.Errorf("page = %s", body)
	}
	if strings.Contains(policy, "momento.corp.example") || strings.Contains(policy, "unsafe-inline'; script-src") && strings.Contains(policy[strings.Index(policy, "script-src"):], "'unsafe-inline'") {
		t.Errorf("policy = %s", policy)
	}
	if !strings.Contains(policy, "report-uri "+cspReportPath) {
		t.Errorf("policy carries no report-uri: %s", policy)
	}
	// The administration screens are left alone.
	if admin := get("/admin/tracking"); strings.Contains(admin.Body.String(), "tracker.js") || admin.Header().Get("Content-Security-Policy") != pagePolicy {
		t.Errorf("admin page carries the snippet: %s", admin.Body.String())
	}
	// The API is not a page.
	if me := get("/api/v1/me"); me.Header().Get("Content-Security-Policy") != nonPagePolicy {
		t.Errorf("api policy = %s", me.Header().Get("Content-Security-Policy"))
	}

	// A browser reports what the policy blocked; the screen shows it; one click
	// allows it; the next page carries it.
	report := httptest.NewRequest(http.MethodPost, cspReportPath, strings.NewReader(`{"csp-report":{"blocked-uri":"https://pixel.tracker.example/p.gif","effective-directive":"img-src","document-uri":"https://vendra.internal/suppliers"}}`))
	report.Header.Set("Content-Type", "application/csp-report")
	reported := httptest.NewRecorder()
	handler.ServeHTTP(reported, report)
	if reported.Code != http.StatusNoContent {
		t.Fatalf("report answered %d", reported.Code)
	}
	var listed struct {
		Items []tracking.Violation `json:"items"`
	}
	if w := get("/api/v1/admin/tracking/violations"); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed.Items) != 1 || listed.Items[0].Origin != "https://pixel.tracker.example" || listed.Items[0].Allowed {
		t.Fatalf("violations = %d %s", w.Code, w.Body.String())
	}
	if w := send(http.MethodPost, "/api/v1/admin/tracking/allowed-hosts", map[string]string{"origin": "pixel.tracker.example"}); w.Code != http.StatusBadRequest {
		t.Errorf("a bare host was accepted: %d %s", w.Code, w.Body.String())
	}
	if w := send(http.MethodPost, "/api/v1/admin/tracking/allowed-hosts", map[string]string{"origin": "https://pixel.tracker.example/p.gif"}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"allowedHosts":"https://pixel.tracker.example"`) {
		t.Fatalf("allow answered %d %s", w.Code, w.Body.String())
	}
	if w := get("/api/v1/admin/tracking/violations"); json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed.Items) != 1 || !listed.Items[0].Allowed {
		t.Errorf("after allowing, violations = %s", w.Body.String())
	}
	if policy := get("/suppliers").Header().Get("Content-Security-Policy"); !strings.Contains(policy, "img-src 'self' data: https://pixel.tracker.example") {
		t.Errorf("the allowed origin did not reach the policy: %s", policy)
	}
	if stored := app.trackingConfig(ctx); !stored.Enabled || stored.Provider != tracking.ProviderMomento || stored.MomentoSiteID != "vendra-prd" {
		t.Errorf("allowing a host disturbed the rest of the configuration: %+v", stored)
	}
	if w := send(http.MethodDelete, "/api/v1/admin/tracking/violations", nil); w.Code != http.StatusNoContent {
		t.Errorf("clearing answered %d", w.Code)
	}
	if w := get("/api/v1/admin/tracking/violations"); json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed.Items) != 0 {
		t.Errorf("after clearing, violations = %s", w.Body.String())
	}
	// None of this is open to a supplier account or to nobody.
	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/v1/admin/tracking/violations", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Errorf("anonymous read of violations answered %d", anonymous.Code)
	}

	// Off again: the policy is exactly what it was.
	if w := putTracking(map[string]any{"enabled": false, "provider": "momento", "momentoUrl": "https://momento.corp.example", "momentoSiteId": "vendra-prd", "momentoProxy": true, "allowedHosts": "https://pixel.tracker.example"}); w.Code != http.StatusOK {
		t.Fatalf("turning tracking off answered %d %s", w.Code, w.Body.String())
	}
	page = get("/suppliers")
	if page.Header().Get("Content-Security-Policy") != pagePolicy || strings.Contains(page.Body.String(), "tracker.js") {
		t.Errorf("after turning off: policy=%s body=%s", page.Header().Get("Content-Security-Policy"), page.Body.String())
	}
	if w := get(tracking.ProxyPath + "/tracker.js"); w.Code != http.StatusNotFound {
		t.Errorf("the proxy is still open after turning off: %d", w.Code)
	}
}
