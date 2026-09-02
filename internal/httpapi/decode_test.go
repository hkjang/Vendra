package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRejectsNullByte(t *testing.T) {
	// A NUL reached PostgreSQL and came back as "invalid byte sequence for
	// encoding UTF8", which the handler reported as a 500 with a database
	// error in the log — for input the client got wrong.
	for _, body := range []string{
		`{"name":"a\u0000b"}`,
		`{"a\u0000b":"x"}`,
		"{\"name\":\"a\x00b\"}",
	} {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		var dst map[string]any
		if err := decodeJSON(req, &dst); err == nil {
			t.Errorf("decodeJSON(%q) accepted a NUL byte", body)
		}
	}

	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"평범한 이름"}`))
	var dst map[string]any
	if err := decodeJSON(req, &dst); err != nil {
		t.Fatalf("decodeJSON rejected an ordinary payload: %v", err)
	}
	if dst["name"] != "평범한 이름" {
		t.Errorf("decoded %v, want the name intact", dst)
	}
}

func TestValidTextCountsRunesNotBytes(t *testing.T) {
	// Every Hangul syllable is three bytes, so a byte-length check would cut
	// Korean names at a third of the limit it advertises.
	atLimit := map[string]any{"name": strings.Repeat("가", maxIdentifierLen)}
	if !validTextFields(httptest.NewRecorder(), atLimit, textField{"name", "업체명"}) {
		t.Errorf("a %d-rune name was rejected", maxIdentifierLen)
	}
	if !validTextFields(httptest.NewRecorder(), map[string]any{"name": "ok"},
		textField{"name", "업체명"}, textField{"missing", "없는 값"}) {
		t.Error("validTextFields rejected a body that left a field out")
	}

	rec := httptest.NewRecorder()
	several := map[string]any{"name": "ok", "country": strings.Repeat("x", maxIdentifierLen+1)}
	if validTextFields(rec, several, textField{"name", "업체명"}, textField{"country", "국가"}) {
		t.Fatal("validTextFields accepted a 201-character country")
	}
	code, msg := errorCodeAndMessage(t, rec)
	if code != "validation_error" {
		t.Errorf("answered %q, want validation_error", code)
	}
	// The caller cannot fix the request without being told which box is wrong.
	if !strings.HasPrefix(msg, "국가는 ") || !strings.Contains(msg, "200자") {
		t.Errorf("said %q; it has to name the field, with the right particle, and the limit", msg)
	}
}
