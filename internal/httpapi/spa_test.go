package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newSPAApp lays out a directory shaped like a Vite build: a no-store entry
// document and content-hashed, immutably cached chunks.
func newSPAApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "<!doctype html><div id=root></div>")
	write(filepath.Join("assets", "index-abc123.js"), "export const entry = 1")
	write(filepath.Join("assets", "Admin-def456.js"), "export default function Admin() {}")
	return &App{staticDir: dir}
}

func serveSPAPath(t *testing.T, app *App, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	app.serveSPA(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestServeSPACachesChunksImmutablyAndEntryNever(t *testing.T) {
	app := newSPAApp(t)
	// Lazily loaded page chunks are content-hashed, so they must be cacheable
	// forever; otherwise code splitting costs a request on every navigation.
	chunk := serveSPAPath(t, app, "/assets/Admin-def456.js")
	if chunk.Code != http.StatusOK {
		t.Fatalf("page chunk returned %d", chunk.Code)
	}
	if got := chunk.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("chunk Cache-Control = %q, want an immutable policy", got)
	}
	if got := chunk.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("chunk Content-Type = %q, want a JavaScript type", got)
	}
	// The entry document names those hashes, so it must never be cached or an
	// upgraded server keeps handing out references to deleted chunks.
	index := serveSPAPath(t, app, "/")
	if got := index.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("index Cache-Control = %q, want no-store", got)
	}
}

func TestServeSPAFallsBackToTheEntryDocument(t *testing.T) {
	app := newSPAApp(t)
	// Client-side routes have no file behind them and must still boot the app.
	deep := serveSPAPath(t, app, "/suppliers/9f1c/evaluations")
	if deep.Code != http.StatusOK || !strings.Contains(deep.Body.String(), "id=root") {
		t.Fatalf("client route returned %d: %s", deep.Code, deep.Body.String())
	}
	// A missing chunk must not be answered with the HTML document: the browser
	// would try to execute it and fail confusingly.
	missing := serveSPAPath(t, app, "/assets/Admin-deleted.js")
	if body := missing.Body.String(); strings.Contains(body, "id=root") {
		t.Error("a missing chunk was answered with index.html instead of a 404")
	}
}

func TestServeSPARejectsPathTraversal(t *testing.T) {
	app := newSPAApp(t)
	secret := filepath.Join(filepath.Dir(app.staticDir), "outside.txt")
	if err := os.WriteFile(secret, []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/../outside.txt", "/assets/../../outside.txt", "/%2e%2e/outside.txt"} {
		w := serveSPAPath(t, app, path)
		if strings.Contains(w.Body.String(), "private") {
			t.Errorf("%s escaped the static root", path)
		}
	}
}
