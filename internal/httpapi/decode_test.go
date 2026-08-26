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

func TestOverlongFieldCountsRunesNotBytes(t *testing.T) {
	// Every Hangul syllable is three bytes, so a byte-length check would cut
	// Korean names at a third of the limit it advertises.
	atLimit := map[string]any{"name": strings.Repeat("가", maxIdentifierLen)}
	if f := overlongField(atLimit, "name"); f != "" {
		t.Errorf("a %d-rune name was rejected as %q", maxIdentifierLen, f)
	}
	over := map[string]any{"name": strings.Repeat("가", maxIdentifierLen+1)}
	if f := overlongField(over, "name"); f != "name" {
		t.Errorf("overlongField = %q, want \"name\"", f)
	}
	several := map[string]any{"name": "ok", "country": strings.Repeat("x", maxIdentifierLen+1)}
	if f := overlongField(several, "name", "country"); f != "country" {
		t.Errorf("overlongField = %q, want \"country\"", f)
	}
	if f := overlongField(map[string]any{"name": "ok"}, "name", "missing"); f != "" {
		t.Errorf("overlongField = %q, want \"\" for absent keys", f)
	}
}
