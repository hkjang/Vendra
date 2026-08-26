package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// recordingMux collects the route table registerAPI writes instead of serving it.
type recordingMux struct{ patterns []string }

func (m *recordingMux) HandleFunc(pattern string, _ http.HandlerFunc) {
	m.patterns = append(m.patterns, pattern)
}

// TestOpenAPIDocumentMatchesTheRoutes holds the published spec to the routes
// that exist. The document is a hand-written map of 169 operations and nothing
// tied it to registerAPI, so a route added without a matching entry — or an
// entry left behind after a route was removed — would only be noticed by an
// integrator building against a spec that no longer describes the server.
func TestOpenAPIDocumentMatchesTheRoutes(t *testing.T) {
	app := &App{}
	var mux recordingMux
	app.registerAPI(&mux)

	registered := map[string]bool{}
	for _, pattern := range mux.patterns {
		method, path, found := strings.Cut(pattern, " ")
		if !found {
			t.Fatalf("route %q has no method", pattern)
		}
		registered[strings.ToLower(method)+" "+path] = true
	}

	w := httptest.NewRecorder()
	app.openapi(w, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("the document returned %d", w.Code)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode the document: %v", err)
	}
	documented := map[string]bool{}
	for path, operations := range doc.Paths {
		for method := range operations {
			switch method {
			case "get", "post", "put", "patch", "delete":
				documented[method+" "+path] = true
			}
		}
	}

	report := func(label string, have, want map[string]bool) {
		t.Helper()
		var missing []string
		for key := range have {
			if !want[key] {
				missing = append(missing, key)
			}
		}
		sort.Strings(missing)
		for _, key := range missing {
			t.Errorf("%s: %s", label, key)
		}
	}
	report("registered but not in the OpenAPI document", registered, documented)
	report("in the OpenAPI document but not registered", documented, registered)
	if len(registered) == 0 {
		t.Fatal("no routes were recorded, so the comparison proves nothing")
	}
	if !t.Failed() {
		t.Logf("%d operations, matched both ways", len(registered))
	}
}
