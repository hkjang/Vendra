package httpapi

import (
	"mime"
	"strings"
	"testing"
)

// decodedFilename is what a browser ends up showing. Go's mime parser applies
// RFC 8187 decoding and lets filename* win over the plain filename, exactly as
// browsers do.
func decodedFilename(t *testing.T, header string) string {
	t.Helper()
	typ, params, err := mime.ParseMediaType(header)
	if err != nil {
		t.Fatalf("could not parse %q: %v", header, err)
	}
	if typ != "attachment" && typ != "inline" {
		t.Fatalf("unexpected disposition type %q", typ)
	}
	return params["filename"]
}

func TestContentDispositionPreservesKoreanAndPunctuation(t *testing.T) {
	// Escaping only spaces, as the previous implementation did, broke every one
	// of these: Korean emitted raw UTF-8 in an ext-value, '%' produced an
	// invalid escape, and ';' or '"' ended the parameter early.
	names := []string{
		"계약서 최종.pdf",
		"100%보고서.pdf",
		"report;v2.pdf",
		`quote"final".pdf`,
		"2026 견적 비교표 (최종본).xlsx",
		"plain-report.pdf",
	}
	for _, name := range names {
		if got := decodedFilename(t, contentDisposition("attachment", name)); got != name {
			t.Errorf("%q was delivered as %q", name, got)
		}
	}
}

func TestContentDispositionAlwaysCarriesAnASCIIFallback(t *testing.T) {
	// A client that ignores filename* must still get a usable name rather than
	// falling back to the URL, which would save every file as "download".
	for _, test := range []struct{ name, want string }{
		{"계약서 최종.pdf", `filename="document.pdf"`},
		{"100%보고서.pdf", `filename="100%.pdf"`},
		{"plain-report.pdf", `filename="plain-report.pdf"`},
	} {
		if header := contentDisposition("attachment", test.name); !strings.Contains(header, test.want) {
			t.Errorf("header for %q was %q, want it to contain %s", test.name, header, test.want)
		}
	}
}

func TestContentDispositionKeepsTheDispositionType(t *testing.T) {
	for _, disposition := range []string{"attachment", "inline"} {
		typ, _, err := mime.ParseMediaType(contentDisposition(disposition, "계약.pdf"))
		if err != nil {
			t.Fatalf("%s header did not parse: %v", disposition, err)
		}
		if typ != disposition {
			t.Errorf("disposition = %q, want %q", typ, disposition)
		}
	}
}

func TestContentDispositionCannotInjectAnotherParameter(t *testing.T) {
	// Filenames come from whoever uploaded the file, including supplier portal
	// users, so they must not be able to add or replace header parameters.
	hostile := `x"; filename="owned.exe`
	header := contentDisposition("attachment", hostile)
	if got := decodedFilename(t, header); got != hostile {
		t.Errorf("hostile name was delivered as %q", got)
	}
	_, params, err := mime.ParseMediaType(header)
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 1 {
		t.Errorf("header produced %d parameters (%v), want only filename", len(params), params)
	}
}

func TestASCIIFallbackIsNeverEmpty(t *testing.T) {
	for _, name := range []string{"한글.pdf", "...", "", "   ", "。。。"} {
		if got := asciiFallbackName(name); got == "" {
			t.Errorf("fallback for %q was empty; the browser would name the file from the URL", name)
		}
	}
}

func TestEncodeExtValueEscapesEverythingOutsideAttrChar(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{"a b", "a%20b"},
		{"100%", "100%25"},
		{"가", "%EA%B0%80"},
		{";", "%3B"},
		{`"`, "%22"},
		{"safe-name_1.pdf", "safe-name_1.pdf"},
	} {
		if got := encodeExtValue(test.in); got != test.want {
			t.Errorf("encodeExtValue(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}
