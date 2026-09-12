package tracking

import (
	"strings"
	"testing"
	"time"
)

func TestAFreshInstallationTracksNothing(t *testing.T) {
	for name, value := range map[string][]byte{"missing row": nil, "empty object": []byte(`{}`), "garbage": []byte(`not json`)} {
		config := Parse(value)
		if config.Enabled {
			t.Errorf("%s: enabled", name)
		}
		if config.Active("/") || config.Active("/suppliers") {
			t.Errorf("%s: a page would carry a snippet", name)
		}
		if config.ProxyActive() {
			t.Errorf("%s: the momento proxy would forward", name)
		}
		if config.Provider != ProviderMomento || !config.MomentoProxy || config.Placement != "head" {
			t.Errorf("%s: defaults = %+v", name, config)
		}
		if err := config.Validate(); err != nil {
			t.Errorf("%s: the default does not validate: %v", name, err)
		}
	}
}

func TestMomentoIsFirstAndThroughTheProxyNeedsNoExternalOrigin(t *testing.T) {
	if Providers[0] != ProviderNone || Providers[1] != ProviderMomento {
		t.Fatalf("providers = %v, want none then momento", Providers)
	}
	config := Parse([]byte(`{"enabled":true,"provider":"momento","momentoUrl":"https://momento.corp.example/","momentoSiteId":"vendra-prd","momentoProxy":true}`))
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if !config.ProxyActive() {
		t.Fatal("the proxy is not active")
	}
	snippet := config.Snippet("n0nce")
	for _, want := range []string{`src="/momento/tracker.js"`, `data-endpoint="/momento"`, `data-site-id="vendra-prd"`, `data-environment="prd"`, `data-contract-version="1"`, `nonce="n0nce"`} {
		if !strings.Contains(snippet, want) {
			t.Errorf("snippet lacks %s:\n%s", want, snippet)
		}
	}
	if strings.Contains(snippet, "momento.corp.example") {
		t.Errorf("through the proxy the collector's address must not reach the page:\n%s", snippet)
	}
	scripts, connects, images := config.PolicySources()
	if len(scripts)+len(connects)+len(images) != 0 {
		t.Errorf("through the proxy the policy needs no extra origin, got %v %v %v", scripts, connects, images)
	}

	direct := config
	direct.MomentoProxy = false
	snippet = direct.Snippet("n0nce")
	if !strings.Contains(snippet, `src="https://momento.corp.example/tracker.js"`) || strings.Contains(snippet, "data-endpoint") {
		t.Errorf("direct snippet = %s", snippet)
	}
	scripts, connects, images = direct.PolicySources()
	for _, group := range [][]string{scripts, connects, images} {
		if len(group) != 1 || group[0] != "https://momento.corp.example" {
			t.Errorf("direct policy sources = %v %v %v", scripts, connects, images)
		}
	}
	if direct.ProxyActive() {
		t.Error("the proxy forwards although it is off")
	}
}

func TestEveryScriptTagCarriesTheRequestNonce(t *testing.T) {
	pasted := `<script async src="https://t.example/tracker.js"></script>
<SCRIPT>window.t=window.t||[];t.push(['page']);</SCRIPT>
<script nonce="theirs">keep()</script>`
	config := Config{Enabled: true, Provider: ProviderCustom, CustomSnippet: pasted}
	snippet := config.Snippet("abc")
	if got := strings.Count(snippet, `nonce="abc"`); got != 2 {
		t.Errorf("%d tags received the nonce, want 2:\n%s", got, snippet)
	}
	if !strings.Contains(snippet, `nonce="theirs"`) {
		t.Error("a tag that already carried a nonce was rewritten")
	}
	for _, provider := range []Config{
		{Enabled: true, Provider: ProviderGA4, MeasurementID: "G-1"},
		{Enabled: true, Provider: ProviderGTM, MeasurementID: "GTM-1"},
		{Enabled: true, Provider: ProviderMatomo, MatomoURL: "https://m.example", MatomoSiteID: "3"},
		{Enabled: true, Provider: ProviderMomento, MomentoURL: "https://m.example", MomentoSiteID: "s"},
	} {
		snippet := provider.Snippet("abc")
		if tags, nonces := strings.Count(strings.ToLower(snippet), "<script"), strings.Count(snippet, `nonce="abc"`); tags == 0 || tags != nonces {
			t.Errorf("%s: %d script tags, %d nonces:\n%s", provider.Provider, tags, nonces, snippet)
		}
	}
}

func TestPolicySourcesAreReadOutOfThePastedSnippet(t *testing.T) {
	config := Config{Enabled: true, Provider: ProviderCustom, AllowedHosts: "https://extra.example, https://extra.example/",
		CustomSnippet: `<script src="https://cdn.tracker.example/t.js" data-endpoint='HTTPS://collect.tracker.example/v1/events'></script><script>fetch("https://cdn.tracker.example/x")</script>`}
	scripts, connects, images := config.PolicySources()
	want := []string{"https://cdn.tracker.example", "https://collect.tracker.example", "https://extra.example"}
	for _, group := range [][]string{scripts, connects, images} {
		if strings.Join(group, " ") != strings.Join(want, " ") {
			t.Errorf("policy sources = %v, want %v", group, want)
		}
	}
}

func TestAdministrativePagesAreLeftAloneUnlessAskedFor(t *testing.T) {
	config := Config{Enabled: true, Provider: ProviderMomento, MomentoSiteID: "s", MomentoProxy: true}
	for _, path := range []string{"/", "/suppliers", "/admin-ish", "/login"} {
		if !config.Active(path) {
			t.Errorf("%s should carry the snippet", path)
		}
	}
	for _, path := range []string{"/admin", "/admin/general", "/admin/tracking"} {
		if config.Active(path) {
			t.Errorf("%s carries the snippet although include_admin is off", path)
		}
	}
	config.IncludeAdmin = true
	if !config.Active("/admin/general") {
		t.Error("include_admin does not include the admin screens")
	}
	config.Enabled = false
	if config.Active("/") {
		t.Error("a disabled configuration is active")
	}
}

func TestWhatCannotBeStored(t *testing.T) {
	long := strings.Repeat("x", MaxSnippetBytes+1)
	for name, raw := range map[string]string{
		"oversized snippet":         `{"provider":"custom","customSnippet":"` + long + `"}`,
		"oversized snippet, off":    `{"enabled":false,"provider":"custom","customSnippet":"` + long + `"}`,
		"unknown provider":          `{"provider":"pixel"}`,
		"host without scheme":       `{"allowedHosts":"cdn.example.com"}`,
		"momento without a site id": `{"enabled":true,"provider":"momento","momentoUrl":"https://m.example"}`,
		"momento with a bad url":    `{"enabled":true,"provider":"momento","momentoUrl":"m.example","momentoSiteId":"s"}`,
		"ga4 without an id":         `{"enabled":true,"provider":"ga4"}`,
		"matomo without a url":      `{"enabled":true,"provider":"matomo","matomoSiteId":"1"}`,
		"custom without a snippet":  `{"enabled":true,"provider":"custom"}`,
	} {
		if err := Parse([]byte(raw)).Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for name, raw := range map[string]string{
		"off with nothing filled in": `{"enabled":false,"provider":"custom"}`,
		"wildcard allow list":        `{"allowedHosts":"https://*.google-analytics.com"}`,
		"snippet at the limit":       `{"enabled":true,"provider":"custom","customSnippet":"` + strings.Repeat("y", MaxSnippetBytes) + `"}`,
	} {
		if err := Parse([]byte(raw)).Validate(); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
}

func TestRecorderKeepsDistinctOriginsNotCounts(t *testing.T) {
	recorder := NewRecorder()
	moment := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	recorder.now = func() time.Time { return moment }
	for i := 0; i < 5; i++ {
		recorder.Record("https://collect.example/v1/events?sid=42", "connect-src https://x", "https://vendra.internal/suppliers?q=acme")
	}
	recorder.Record("https://cdn.example/t.js", "script-src-elem", "https://vendra.internal/")
	recorder.Record("inline", "script-src", "https://vendra.internal/")
	recorder.Record("chrome-extension://abc/inject.js", "script-src", "https://vendra.internal/")
	items := recorder.List(Config{})
	if len(items) != 2 {
		t.Fatalf("%d violations recorded, want 2: %+v", len(items), items)
	}
	var collect Violation
	for _, item := range items {
		if item.Origin == "https://collect.example" {
			collect = item
		}
	}
	if collect.Count != 5 || collect.Directive != "connect-src" || collect.Page != "/suppliers" {
		t.Errorf("collect = %+v", collect)
	}
	// The one the configuration already allows is marked so it stops nagging.
	items = recorder.List(Config{Provider: ProviderCustom, AllowedHosts: "https://collect.example"})
	for _, item := range items {
		if item.Allowed != (item.Origin == "https://collect.example") {
			t.Errorf("allowed flag on %s = %v", item.Origin, item.Allowed)
		}
	}
	// The ring is bounded: the oldest goes first.
	for i := 0; i < MaxViolations+10; i++ {
		moment = moment.Add(time.Second)
		recorder.Record("https://host"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+".example/"+strings.Repeat("y", i), "img-src", "/")
	}
	if got := len(recorder.List(Config{})); got > MaxViolations {
		t.Errorf("%d violations kept, the ring holds %d", got, MaxViolations)
	}
	recorder.Forget()
	if len(recorder.List(Config{})) != 0 {
		t.Error("forget left violations behind")
	}
}

func TestAddAllowedHostKeepsTheListTidy(t *testing.T) {
	list := AddAllowedHost("", "https://a.example/")
	list = AddAllowedHost(list, "https://b.example")
	list = AddAllowedHost(list, "HTTPS://A.EXAMPLE")
	if list != "https://a.example, https://b.example" {
		t.Errorf("list = %q", list)
	}
}
