package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// filler produces an endless run of bytes so an oversized body can be built
// without holding it in memory.
type filler struct{}

func (filler) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'A'
	}
	return len(p), nil
}

func multipartUpload(filename string, size int64, truncated bool) (io.Reader, string) {
	const boundary = "vendratestboundary"
	head := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"" +
		filename + "\"\r\nContent-Type: application/octet-stream\r\n\r\n"
	parts := []io.Reader{strings.NewReader(head), io.LimitReader(filler{}, size)}
	if !truncated {
		parts = append(parts, strings.NewReader("\r\n--"+boundary+"--\r\n"))
	}
	return io.MultiReader(parts...), boundary
}

func uploadOutcome(t *testing.T, filename string, size int64, truncated bool) (int, string) {
	t.Helper()
	body, boundary := multipartUpload(filename, size, truncated)
	req := httptest.NewRequest("POST", "/api/v1/documents/upload", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	rec := httptest.NewRecorder()
	if parseUpload(rec, req) {
		return 0, ""
	}
	var out struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response was not the usual error envelope: %s", rec.Body.String())
	}
	return rec.Code, out.Error.Code
}

func TestParseUploadNamesTheActualFailure(t *testing.T) {
	// Every parse error used to answer "file_too_large", so a one-kilobyte
	// upload that ended mid-stream told the uploader it exceeded 25 MB.
	for _, tc := range []struct {
		name      string
		filename  string
		size      int64
		truncated bool
		wantCode  string
	}{
		{"a file within the limit", "ok.bin", 1024, false, ""},
		{"a body that ended mid-stream", "cut.bin", 1024, true, "invalid_upload"},
		{"a filename the parser refuses", "a\x00b.txt", 16, false, "invalid_upload"},
		{"a file over the limit", "big.bin", uploadLimitBytes + (2 << 20), false, "file_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, code := uploadOutcome(t, tc.filename, tc.size, tc.truncated)
			if tc.wantCode == "" {
				if code != "" {
					t.Fatalf("accepted upload was rejected as %q", code)
				}
				return
			}
			if code != tc.wantCode {
				t.Errorf("error code = %q, want %q", code, tc.wantCode)
			}
			if status != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", status, http.StatusBadRequest)
			}
		})
	}
}
