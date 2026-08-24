package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"
)

type servedDocument struct {
	id   string
	name string
}

func uploadForServing(t *testing.T, w *scopeWorld, token, filename, contentType, content string) servedDocument {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := mw.CreatePart(header)
	if err != nil {
		t.Fatalf("part: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("write: %v", err)
	}
	for k, v := range map[string]string{"documentType": "other", "supplierId": w.mySupplier} {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("field: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/documents/upload", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload %q: %d %s", filename, rec.Code, rec.Body.String())
	}
	var out struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return servedDocument{id: out.ID, name: out.Name}
}

func serveDoc(t *testing.T, w *scopeWorld, token, id, route string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/documents/"+id+"/"+route, nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	w.handler.ServeHTTP(rec, r)
	return rec
}

func scopeToken(t *testing.T, w *scopeWorld) string {
	t.Helper()
	return w.deptToken
}

// Uploaded content is served from the application's own origin, so anything a
// browser would execute has to be declawed on the way out.
func TestUploadedContentCannotRunInTheBrowser(t *testing.T) {
	w := newScopeWorld(t)
	token := scopeToken(t, w)

	for _, tc := range []struct{ name, filename, contentType, body string }{
		{"html", "evil.html", "text/html", `<script>alert(document.domain)</script>`},
		{"svg", "evil.svg", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := uploadForServing(t, w, token, tc.filename, tc.contentType, tc.body)

			download := serveDoc(t, w, token, doc.id, "download")
			if disposition := download.Header().Get("Content-Disposition"); !strings.HasPrefix(disposition, "attachment") {
				t.Errorf("download disposition = %q, want an attachment", disposition)
			}
			if download.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("download does not forbid content sniffing")
			}

			preview := serveDoc(t, w, token, doc.id, "preview")
			policy := preview.Header().Get("Content-Security-Policy")
			if !strings.Contains(policy, "sandbox") {
				t.Errorf("preview policy = %q, want it sandboxed", policy)
			}
			if !strings.Contains(policy, "default-src 'none'") {
				t.Errorf("preview policy = %q, want everything denied by default", policy)
			}
			if strings.Contains(policy, "allow-scripts") {
				t.Errorf("preview policy = %q, want scripts left out of the sandbox", policy)
			}
		})
	}
}

// A filename is caller-supplied and reaches both the filesystem and a response
// header.
func TestUploadedFilenameCannotEscapeOrInject(t *testing.T) {
	w := newScopeWorld(t)
	token := scopeToken(t, w)

	t.Run("path segments are dropped", func(t *testing.T) {
		doc := uploadForServing(t, w, token, "../../../etc/passwd", "text/plain", "x")
		if strings.ContainsAny(doc.name, `/\`) || strings.Contains(doc.name, "..") {
			t.Errorf("stored name = %q, want no path in it", doc.name)
		}
		var path string
		_, pool := newTestApp(t)
		if err := pool.QueryRow(context.Background(), `SELECT storage_path FROM documents WHERE id=$1`, doc.id).Scan(&path); err != nil {
			t.Fatalf("read path: %v", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the stored file is not where the row says: %v", err)
		}
	})

	t.Run("header injection is refused", func(t *testing.T) {
		doc := uploadForServing(t, w, token, "a\"b\r\nX-Injected: yes.txt", "text/plain\r\nX-Injected: yes", "x")
		rec := serveDoc(t, w, token, doc.id, "download")
		if rec.Header().Get("X-Injected") != "" {
			t.Error("a header was injected through the upload")
		}
		for _, header := range []string{"Content-Type", "Content-Disposition"} {
			if value := rec.Header().Get(header); strings.ContainsAny(value, "\r\n") {
				t.Errorf("%s carries a line break: %q", header, value)
			}
		}
	})
}

// The response advertises a checksum. A file that no longer matches it must not
// go out unremarked.
func TestServedContentIsCheckedAgainstItsChecksum(t *testing.T) {
	w := newScopeWorld(t)
	token := scopeToken(t, w)
	_, pool := newTestApp(t)

	doc := uploadForServing(t, w, token, "contract.txt", "text/plain", "원본 내용")
	var path string
	if err := pool.QueryRow(context.Background(), `SELECT storage_path FROM documents WHERE id=$1`, doc.id).Scan(&path); err != nil {
		t.Fatalf("read path: %v", err)
	}
	// Same length, different bytes — exactly what the size check cannot see.
	if err := os.WriteFile(path, []byte("바뀐 내용"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	rec := serveDoc(t, w, token, doc.id, "download")
	if rec.Code != http.StatusOK {
		t.Fatalf("download: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logged.String(), "does not match its recorded checksum") {
		t.Errorf("a document that no longer matches its checksum was served without a word\n  log: %s", logged.String())
	}
}
