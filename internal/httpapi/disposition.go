package httpapi

import (
	"fmt"
	"path/filepath"
	"strings"
)

// attrChar is the set RFC 8187 allows to appear literally in an ext-value.
// Everything else, including every byte of a Korean filename, must be
// percent-encoded.
func attrChar(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", b) >= 0
}

// encodeExtValue renders name as an RFC 8187 ext-value.
func encodeExtValue(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if c := name[i]; attrChar(c) {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", name[i])
	}
	return b.String()
}

// asciiFallbackName produces the quoted-string `filename` parameter for clients
// that ignore `filename*`. Non-ASCII is dropped rather than mangled, and the
// quoting characters are escaped so the parameter cannot be broken out of.
func asciiFallbackName(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 0x20 || c >= 0x7f {
			continue
		}
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	fallback := strings.Join(strings.Fields(b.String()), " ")
	// Stripping Korean from "계약서 최종.pdf" leaves ".pdf", which is a worse name
	// than none. Keep the extension but give it a usable stem.
	if stem := strings.TrimSuffix(fallback, filepath.Ext(fallback)); strings.Trim(stem, ".") == "" {
		fallback = "document" + filepath.Ext(fallback)
	}
	if fallback == "" {
		return "document"
	}
	return fallback
}

// contentDisposition builds a header both old and new clients read correctly.
// The previous implementation escaped only spaces, so a Korean name emitted raw
// UTF-8 in an ext-value, a name containing '%' produced an invalid escape, and
// a name containing ';' or '"' terminated the parameter early. With no
// `filename` fallback present, a client that rejected the ext-value fell back to
// the URL's last segment and saved the file as "download".
func contentDisposition(disposition, name string) string {
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`,
		disposition, asciiFallbackName(name), encodeExtValue(name))
}
