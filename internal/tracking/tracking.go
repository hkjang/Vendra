// Package tracking injects a visitor tracking snippet into the served pages.
//
// The content security policy Vendra ships with allows scripts from the
// application origin only, so a tracking snippet cannot simply be pasted in:
// the browser refuses it and the administrator sees an empty dashboard with no
// explanation. This package produces both halves of the answer — the markup to
// inject and the policy sources it needs — with a per-request nonce so inline
// code runs without weakening the policy for everything else.
package tracking

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strings"
)

const (
	// SettingKey is the settings row the configuration lives in.
	SettingKey = "tracking"
	// SettingCategory is the row's category on the settings screen.
	SettingCategory = "tracking"

	ProviderNone    = "none"
	ProviderMomento = "momento"
	ProviderGA4     = "ga4"
	ProviderGTM     = "gtm"
	ProviderMatomo  = "matomo"
	ProviderCustom  = "custom"

	// MaxSnippetBytes bounds a pasted snippet. A tracker's loader is a few
	// hundred bytes; anything larger is a page, not a snippet.
	MaxSnippetBytes = 8 * 1024

	// ProxyPath is the same-origin prefix the application forwards to the
	// Momento collector, so no external origin has to appear in the policy.
	ProxyPath = "/momento"
)

// Providers lists the accepted providers in the order the screen offers them.
// Momento is first: it is the self-hosted collector, the only choice under
// which nothing leaves the network.
var Providers = []string{ProviderNone, ProviderMomento, ProviderGA4, ProviderGTM, ProviderMatomo, ProviderCustom}

// Config is the stored setting. The JSON shape is the one the administration
// screen writes, in the camelCase the other settings rows use.
type Config struct {
	Enabled       bool   `json:"enabled"`
	Provider      string `json:"provider"`
	MomentoURL    string `json:"momentoUrl"`
	MomentoSiteID string `json:"momentoSiteId"`
	// MomentoProxy sends the tracker and its events through ProxyPath on this
	// origin instead of straight to the collector.
	MomentoProxy  bool   `json:"momentoProxy"`
	MeasurementID string `json:"measurementId"`
	MatomoURL     string `json:"matomoUrl"`
	MatomoSiteID  string `json:"matomoSiteId"`
	CustomSnippet string `json:"customSnippet"`
	AllowedHosts  string `json:"allowedHosts"`
	IncludeAdmin  bool   `json:"includeAdmin"`
	Placement     string `json:"placement"`
}

// Default is what a fresh installation gets: nothing on.
func Default() Config {
	return Config{Provider: ProviderMomento, MomentoProxy: true, Placement: "head"}
}

// Parse reads a stored value. A missing or unreadable row is "no tracking";
// a settings problem must never break the page it would have decorated.
func Parse(value []byte) Config {
	config := Default()
	if len(value) == 0 {
		return config
	}
	var stored struct {
		Enabled       *bool   `json:"enabled"`
		Provider      *string `json:"provider"`
		MomentoURL    *string `json:"momentoUrl"`
		MomentoSiteID *string `json:"momentoSiteId"`
		MomentoProxy  *bool   `json:"momentoProxy"`
		MeasurementID *string `json:"measurementId"`
		MatomoURL     *string `json:"matomoUrl"`
		MatomoSiteID  *string `json:"matomoSiteId"`
		CustomSnippet *string `json:"customSnippet"`
		AllowedHosts  *string `json:"allowedHosts"`
		IncludeAdmin  *bool   `json:"includeAdmin"`
		Placement     *string `json:"placement"`
	}
	if json.Unmarshal(value, &stored) != nil {
		return Default()
	}
	text := func(field *string) string {
		if field == nil {
			return ""
		}
		return strings.TrimSpace(*field)
	}
	if stored.Enabled != nil {
		config.Enabled = *stored.Enabled
	}
	if stored.Provider != nil {
		config.Provider = strings.ToLower(text(stored.Provider))
	}
	if config.Provider == "" {
		config.Provider = ProviderNone
	}
	config.MomentoURL = strings.TrimRight(text(stored.MomentoURL), "/")
	config.MomentoSiteID = text(stored.MomentoSiteID)
	if stored.MomentoProxy != nil {
		config.MomentoProxy = *stored.MomentoProxy
	}
	config.MeasurementID = text(stored.MeasurementID)
	config.MatomoURL = strings.TrimRight(text(stored.MatomoURL), "/")
	config.MatomoSiteID = text(stored.MatomoSiteID)
	if stored.CustomSnippet != nil {
		config.CustomSnippet = strings.TrimSpace(*stored.CustomSnippet)
	}
	config.AllowedHosts = text(stored.AllowedHosts)
	if stored.IncludeAdmin != nil {
		config.IncludeAdmin = *stored.IncludeAdmin
	}
	config.Placement = strings.ToLower(text(stored.Placement))
	if config.Placement != "body" {
		config.Placement = "head"
	}
	return config
}

// Validate says what is wrong with a configuration before it is stored. The
// limits hold whether or not tracking is on, so a value that would fail the
// moment somebody flips the switch is refused while they are still looking at
// the box it came from.
func (c Config) Validate() error {
	known := false
	for _, provider := range Providers {
		if c.Provider == provider {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("provider는 %s 중 하나여야 합니다", strings.Join(Providers, ", "))
	}
	if len(c.CustomSnippet) > MaxSnippetBytes {
		return fmt.Errorf("추적 코드는 %d바이트를 넘을 수 없습니다", MaxSnippetBytes)
	}
	for _, host := range splitHosts(c.AllowedHosts) {
		if originOf(host) == "" || !strings.HasPrefix(strings.ToLower(host), "http") {
			return fmt.Errorf("허용 출처 %q는 https://host 모양이어야 합니다", host)
		}
	}
	if c.MomentoURL != "" && !absoluteHTTP(c.MomentoURL) {
		return fmt.Errorf("momentoUrl이 올바른 http(s) 주소가 아닙니다")
	}
	if c.MatomoURL != "" && !absoluteHTTP(c.MatomoURL) {
		return fmt.Errorf("matomoUrl이 올바른 http(s) 주소가 아닙니다")
	}
	if !c.Enabled {
		return nil
	}
	switch c.Provider {
	case ProviderNone:
	case ProviderMomento:
		if c.MomentoURL == "" || c.MomentoSiteID == "" {
			return fmt.Errorf("momentoUrl과 momentoSiteId가 필요합니다")
		}
	case ProviderGA4, ProviderGTM:
		if c.MeasurementID == "" {
			return fmt.Errorf("measurementId가 필요합니다")
		}
	case ProviderMatomo:
		if c.MatomoURL == "" || c.MatomoSiteID == "" {
			return fmt.Errorf("matomoUrl과 matomoSiteId가 필요합니다")
		}
	case ProviderCustom:
		if c.CustomSnippet == "" {
			return fmt.Errorf("customSnippet이 비어 있습니다")
		}
	}
	return nil
}

// Active reports whether a page at path should carry the snippet.
// Administrative pages are excluded unless asked for, because console traffic
// is rarely the visitor data anybody wants.
func (c Config) Active(path string) bool {
	if !c.Enabled || c.Provider == ProviderNone || c.Provider == "" {
		return false
	}
	if !c.IncludeAdmin && (path == "/admin" || strings.HasPrefix(path, "/admin/")) {
		return false
	}
	return strings.TrimSpace(c.Snippet("")) != ""
}

// ProxyActive reports whether ProxyPath should forward to a Momento collector.
func (c Config) ProxyActive() bool {
	return c.Enabled && c.Provider == ProviderMomento && c.MomentoProxy && absoluteHTTP(c.MomentoURL)
}

// Snippet renders the markup to inject. The nonce goes on every script tag so
// the policy can stay strict.
func (c Config) Snippet(nonce string) string {
	switch c.Provider {
	case ProviderMomento:
		site := html.EscapeString(c.MomentoSiteID)
		if site == "" {
			return ""
		}
		if c.MomentoProxy {
			// Through the proxy the tracker loads from, and reports to, this
			// origin — which the policy already allows.
			return withNonce(fmt.Sprintf(`<script async src="%s/tracker.js" data-site-id="%s" data-environment="prd" data-contract-version="1" data-endpoint="%s"></script>`, ProxyPath, site, ProxyPath), nonce)
		}
		if !absoluteHTTP(c.MomentoURL) {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script async src="%s/tracker.js" data-site-id="%s" data-environment="prd" data-contract-version="1"></script>`, html.EscapeString(c.MomentoURL), site), nonce)
	case ProviderGA4:
		id := html.EscapeString(c.MeasurementID)
		if id == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script async src="https://www.googletagmanager.com/gtag/js?id=%s"></script>
<script>window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}gtag('js',new Date());gtag('config','%s');</script>`, id, id), nonce)
	case ProviderGTM:
		id := html.EscapeString(c.MeasurementID)
		if id == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script>(function(w,d,s,l,i){w[l]=w[l]||[];w[l].push({'gtm.start':new Date().getTime(),event:'gtm.js'});var f=d.getElementsByTagName(s)[0],j=d.createElement(s),dl=l!='dataLayer'?'&l='+l:'';j.async=true;j.src='https://www.googletagmanager.com/gtm.js?id='+i+dl;f.parentNode.insertBefore(j,f);})(window,document,'script','dataLayer','%s');</script>`, id), nonce)
	case ProviderMatomo:
		site := html.EscapeString(c.MatomoSiteID)
		if !absoluteHTTP(c.MatomoURL) || site == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script>var _paq=window._paq=window._paq||[];_paq.push(['trackPageView']);_paq.push(['enableLinkTracking']);(function(){var u="%s/";_paq.push(['setTrackerUrl',u+'matomo.php']);_paq.push(['setSiteId','%s']);var d=document,g=d.createElement('script'),s=d.getElementsByTagName('script')[0];g.async=true;g.src=u+'matomo.js';s.parentNode.insertBefore(g,s);})();</script>`, html.EscapeString(c.MatomoURL), site), nonce)
	case ProviderCustom:
		return withNonce(c.CustomSnippet, nonce)
	}
	return ""
}

// withNonce adds the nonce to every script tag that does not already carry
// one, which is what lets a pasted snippet run under a strict policy unchanged.
func withNonce(snippet, nonce string) string {
	if nonce == "" || snippet == "" {
		return snippet
	}
	var builder strings.Builder
	remaining := snippet
	for {
		index := strings.Index(strings.ToLower(remaining), "<script")
		if index < 0 {
			builder.WriteString(remaining)
			return builder.String()
		}
		end := index + len("<script")
		builder.WriteString(remaining[:end])
		tag := remaining[end:]
		if closing := strings.IndexByte(tag, '>'); closing >= 0 {
			tag = tag[:closing]
		}
		if !strings.Contains(strings.ToLower(tag), "nonce=") {
			builder.WriteString(` nonce="` + html.EscapeString(nonce) + `"`)
		}
		remaining = remaining[end:]
	}
}

// PolicySources lists the extra origins the snippet needs, derived from the
// provider so a common setup needs no policy knowledge at all. Momento through
// the proxy needs none: everything it touches is this origin.
func (c Config) PolicySources() (scripts, connects, images []string) {
	seen := map[string]struct{}{}
	add := func(origin string) {
		if _, duplicate := seen[strings.ToLower(origin)]; duplicate {
			return
		}
		seen[strings.ToLower(origin)] = struct{}{}
		scripts = append(scripts, origin)
		connects = append(connects, origin)
		images = append(images, origin)
	}
	switch c.Provider {
	case ProviderMomento:
		if !c.MomentoProxy {
			if origin := originOf(c.MomentoURL); origin != "" {
				add(origin)
			}
		}
	case ProviderGA4, ProviderGTM:
		scripts = append(scripts, "https://www.googletagmanager.com")
		connects = append(connects, "https://www.google-analytics.com", "https://analytics.google.com", "https://*.google-analytics.com")
		images = append(images, "https://www.google-analytics.com", "https://www.googletagmanager.com")
	case ProviderMatomo:
		if origin := originOf(c.MatomoURL); origin != "" {
			add(origin)
		}
	case ProviderCustom:
		// A pasted snippet names the addresses it loads and reports to, so
		// those origins are allowed without anybody reading a policy error.
		for _, origin := range SnippetOrigins(c.CustomSnippet) {
			add(origin)
		}
	}
	for _, host := range splitHosts(c.AllowedHosts) {
		add(host)
	}
	return scripts, connects, images
}

// SnippetOrigins lists every http(s) origin written into a snippet: the script
// it loads, the endpoint it posts to, the pixel it requests. A tracker almost
// always writes its own address somewhere in its loader.
func SnippetOrigins(snippet string) []string {
	origins := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	lower := strings.ToLower(snippet)
	for index := 0; index < len(snippet); {
		start := strings.Index(lower[index:], "http")
		if start < 0 {
			break
		}
		start += index
		end := start
		for end < len(snippet) && !isURLBoundary(snippet[end]) {
			end++
		}
		index = end
		origin := originOf(snippet[start:end])
		if origin == "" || !strings.HasPrefix(origin, "http") {
			continue
		}
		if _, duplicate := seen[origin]; duplicate {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins
}

// AddAllowedHost appends an origin to the allow list, leaving the existing
// entries and their order alone.
func AddAllowedHost(existing, origin string) string {
	origin = strings.TrimSpace(strings.TrimSuffix(origin, "/"))
	if origin == "" {
		return existing
	}
	for _, host := range splitHosts(existing) {
		if strings.EqualFold(host, origin) {
			return existing
		}
	}
	if strings.TrimSpace(existing) == "" {
		return origin
	}
	return strings.TrimSpace(existing) + ", " + origin
}

// isURLBoundary reports the characters that cannot appear in a URL written
// inside HTML or JavaScript, which is where each address ends.
func isURLBoundary(letter byte) bool {
	switch letter {
	case '"', '\'', '`', '<', '>', ' ', '\t', '\n', '\r', ')', ',', ';', '\\', '+':
		return true
	}
	return false
}

func splitHosts(list string) []string {
	var hosts []string
	for _, host := range strings.FieldsFunc(list, func(letter rune) bool {
		return letter == ',' || letter == ' ' || letter == '\n' || letter == '\r' || letter == '\t'
	}) {
		if trimmed := strings.TrimSuffix(strings.TrimSpace(host), "/"); trimmed != "" {
			hosts = append(hosts, trimmed)
		}
	}
	return hosts
}

func absoluteHTTP(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// originOf reduces an address to its scheme and host, which is the grain a
// policy source is written in.
func originOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(parsed.Host)
}
