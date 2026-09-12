package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/hkjang/Vendra/internal/tracking"
)

// Content security policies. Pages get the strict one and, only while a
// tracking snippet is on, the additions that snippet needs; everything that is
// not a page — JSON, metrics, the MCP endpoint, the collector proxy — gets a
// policy under which nothing can load at all, because nothing should.
const (
	pagePolicy    = "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'"
	nonPagePolicy = "default-src 'none'; frame-ancestors 'none'"

	// cspReportPath is where browsers post the requests the policy refused. It
	// sits outside the authenticated API because the browser reports from the
	// login screen too, and it stores nothing but a bounded, in-memory list of
	// origins.
	cspReportPath  = "/api/tracking/csp-report"
	maxReportBytes = 8 * 1024
	// maxProxyBodyBytes bounds what a page may send through the collector
	// proxy. A page-view event is a few hundred bytes.
	maxProxyBodyBytes = 64 * 1024
)

// isPagePath reports whether a request path is served by the single page
// application rather than by an API, health, metrics or proxy handler.
func isPagePath(path string) bool {
	for _, prefix := range []string{"/api/", "/mcp", "/health/", "/metrics", tracking.ProxyPath + "/"} {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	return path != tracking.ProxyPath
}

// trackingPolicy assembles the page policy for one request: the strict policy
// plus, while a snippet is on for this page, its nonce, its origins and the
// report-uri that turns a silent block into a line on the administration
// screen. Without a snippet the policy is exactly the one shipped before
// tracking existed.
func trackingPolicy(config tracking.Config, path, nonce string) string {
	if !config.Active(path) {
		return pagePolicy
	}
	scripts := []string{"'self'", "'nonce-" + nonce + "'"}
	connects := []string{"'self'"}
	images := []string{"'self'", "data:"}
	extraScripts, extraConnects, extraImages := config.PolicySources()
	scripts = append(scripts, extraScripts...)
	connects = append(connects, extraConnects...)
	images = append(images, extraImages...)
	return "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; img-src " + strings.Join(images, " ") +
		"; style-src 'self' 'unsafe-inline'; script-src " + strings.Join(scripts, " ") +
		"; connect-src " + strings.Join(connects, " ") +
		"; report-uri " + cspReportPath
}

// trackingConfig reads the stored setting. Any failure — no database in a
// unit test, an outage, an unreadable row — is "no tracking", so a settings
// problem can never keep a page from being served.
func (a *App) trackingConfig(ctx context.Context) tracking.Config {
	if a == nil || a.db == nil {
		return tracking.Default()
	}
	var value []byte
	if err := a.db.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, tracking.SettingKey).Scan(&value); err != nil {
		return tracking.Default()
	}
	return tracking.Parse(value)
}

// injectSnippet places the markup just before the closing tag the placement
// names, or at the end of the document when that tag is missing.
func injectSnippet(page []byte, snippet, placement string) []byte {
	marker := "</head>"
	if placement == "body" {
		marker = "</body>"
	}
	text := string(page)
	index := strings.LastIndex(strings.ToLower(text), marker)
	if index < 0 {
		return []byte(text + "\n" + snippet + "\n")
	}
	return []byte(text[:index] + snippet + "\n" + text[index:])
}

// decoratePage returns the entry document with the tracking snippet for this
// page, and the policy header that lets exactly that snippet run. The two are
// produced together because they share the nonce: a policy that names one
// nonce and a page that carries another is a blocked script with no message.
func decoratePage(config tracking.Config, path string, page []byte) ([]byte, string, error) {
	if !config.Active(path) {
		return page, pagePolicy, nil
	}
	nonce, err := randomToken(16)
	if err != nil {
		return nil, "", err
	}
	return injectSnippet(page, config.Snippet(nonce), config.Placement), trackingPolicy(config, path, nonce), nil
}

// A CSP report as browsers post it (application/csp-report).
type cspReport struct {
	Report struct {
		BlockedURI         string `json:"blocked-uri"`
		ViolatedDirective  string `json:"violated-directive"`
		EffectiveDirective string `json:"effective-directive"`
		DocumentURI        string `json:"document-uri"`
	} `json:"csp-report"`
}

// receiveCSPReport records what a browser refused to load. Reports are always
// answered 204: the page that sent one is already in trouble, and an error
// from here would be one more thing in its console.
func (a *App) receiveCSPReport(w http.ResponseWriter, r *http.Request) {
	defer w.WriteHeader(http.StatusNoContent)
	if a.violations == nil {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxReportBytes))
	if err != nil || len(body) == 0 {
		return
	}
	var report cspReport
	if json.Unmarshal(body, &report) != nil {
		return
	}
	directive := report.Report.EffectiveDirective
	if directive == "" {
		directive = report.Report.ViolatedDirective
	}
	a.violations.Record(report.Report.BlockedURI, directive, report.Report.DocumentURI)
}

// listTrackingViolations shows the administrator which addresses the policy
// is blocking, so a snippet can be fixed without reading a browser console.
func (a *App) listTrackingViolations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"items": a.violations.List(a.trackingConfig(r.Context()))})
}

// clearTrackingViolations forgets the recorded reports, which is how an
// administrator checks whether a change actually fixed the snippet.
func (a *App) clearTrackingViolations(w http.ResponseWriter, _ *http.Request) {
	a.violations.Forget()
	w.WriteHeader(http.StatusNoContent)
}

// allowTrackingHost adds one blocked origin to the allow list — the one-click
// fix for the reports listed above.
func (a *App) allowTrackingHost(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Origin string `json:"origin"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	origin := strings.TrimSpace(in.Origin)
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeError(w, 400, "validation_error", "허용할 출처는 https://host 모양이어야 합니다")
		return
	}
	config := a.trackingConfig(r.Context())
	config.AllowedHosts = tracking.AddAllowedHost(config.AllowedHosts, parsed.Scheme+"://"+parsed.Host)
	if err := a.storeSetting(r.Context(), tracking.SettingKey, config, tracking.SettingCategory, p.ID); err != nil {
		writeError(w, 400, "save_failed", "설정을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "update", "setting", tracking.SettingKey, nil, map[string]any{"allowedHosts": config.AllowedHosts})
	writeJSON(w, 200, map[string]any{"ok": true, "allowedHosts": config.AllowedHosts})
}

// storeSetting writes one plain (non-secret) settings row the way putSetting
// does, keeping the secret column whatever it was.
func (a *App) storeSetting(ctx context.Context, key string, value any, category, actorID string) error {
	_, err := a.db.Exec(ctx, `INSERT INTO settings(key,value,secret,category,updated_by,updated_at) VALUES($1,$2,false,$3,$4,now()) ON CONFLICT(key) DO UPDATE SET value=excluded.value,category=excluded.category,updated_by=excluded.updated_by,updated_at=now()`, key, raw(value), category, actorID)
	return err
}

// momentoProxy forwards ProxyPath/* to the Momento collector so the tracker
// loads from, and reports to, this origin. That is what keeps the collector's
// address out of the policy: to the browser the whole thing is 'self'.
//
// The request is the browser's, so it carries the Vendra session cookie; that
// is stripped before anything leaves. Whatever the collector answers with is
// passed back without its cookies or policy, which have no business on this
// origin.
func (a *App) momentoProxy(w http.ResponseWriter, r *http.Request) {
	proxyToCollector(a.trackingConfig(r.Context()), w, r)
}

func proxyToCollector(config tracking.Config, w http.ResponseWriter, r *http.Request) {
	if !config.ProxyActive() {
		writeError(w, 404, "not_found", "찾을 수 없습니다")
		return
	}
	target, err := url.Parse(config.MomentoURL)
	if err != nil {
		writeError(w, 404, "not_found", "찾을 수 없습니다")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost && r.Method != http.MethodOptions {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "허용되지 않는 메서드입니다")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxProxyBodyBytes)
	proxy := &httputil.ReverseProxy{
		Transport: outboundClient.Transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = strings.TrimSuffix(target.Path, "/") + strings.TrimPrefix(pr.In.URL.Path, tracking.ProxyPath)
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			resp.Header.Del("Content-Security-Policy")
			resp.Header.Del("Content-Security-Policy-Report-Only")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeError(w, http.StatusBadGateway, "collector_unavailable", "수집기에 연결하지 못했습니다")
		},
	}
	proxy.ServeHTTP(w, r)
}
