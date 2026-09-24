// Package filename derives a safe-to-store file name from disposition params.
package filename

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"ontology/paramjoin"
	"ontology/paramlex"
)

// Fallback is used when sanitizing strips everything. It contains a "%2e"
// sequence, which Sanitize always strips from real input, so no genuine
// parameter value can ever sanitize to it (see NOTES.md, invariant 3).
const Fallback = "unnamed%2e"

// FromHeader parses a Content-Disposition header and derives a safe name.
func FromHeader(header string) (string, error) {
	typ, items, err := paramlex.Parse(header)
	if err != nil {
		return "", err
	}
	params, err := paramjoin.Join(items)
	if err != nil {
		return "", err
	}
	return Choose(typ, params), nil
}

// Choose prefers the extended filename* form over plain filename; the
// choice is explicit and never depends on parameter or map order.
func Choose(_ string, params []paramjoin.Param) string {
	var plain, ext string
	hasPlain, hasExt := false, false
	for _, p := range params {
		if p.Name != "filename" {
			continue
		}
		if p.Ext {
			ext, hasExt = p.Value, true
		} else {
			plain, hasPlain = p.Value, true
		}
	}
	if hasExt {
		return Sanitize(ext)
	}
	if hasPlain {
		return Sanitize(plain)
	}
	return Fallback
}

// Sanitize only drops or folds characters, never rewrites them, so two
// inputs can collide only when they differ in stripped parts alone.
func Sanitize(s string) string {
	var b strings.Builder
	lastDot := false
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			i += 3 // drop %XX triples: downstream must not re-decode them
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == '/' || r == '\\' || r == 0 || unicode.IsControl(r):
		case r == utf8.RuneError && size == 1: // invalid utf-8 byte: drop
		case r == '.':
			if !lastDot {
				b.WriteByte('.')
			}
			lastDot = true
		default:
			b.WriteRune(r)
			lastDot = false
		}
	}
	name := strings.TrimSpace(b.String())
	if name == "" || name == "." {
		return Fallback
	}
	return name
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
