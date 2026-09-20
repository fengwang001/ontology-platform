package ctxtmpl

import "strings"

var blockedURLPrefixes = []string{
	"javascript:",
	"vbscript:",
	"data:",
}

// isURLAttr reports whether attr (already lower-cased) is a URL-bearing
// attribute.
func isURLAttr(attr string) bool {
	switch attr {
	case "href", "src", "action", "formaction":
		return true
	}
	return false
}

// validateURLStart validates a raw interpolated value placed at the start
// of a URL attribute value. Leading whitespace is ignored and embedded
// tab/newline characters are stripped before matching, mirroring how
// browsers parse scheme names.
func validateURLStart(raw string) error {
	stripped := strings.Builder{}
	stripped.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case ' ', '\t', '\n', '\r', '\f':
			continue
		}
		stripped.WriteByte(raw[i])
	}
	flat := strings.ToLower(stripped.String())
	for _, prefix := range blockedURLPrefixes {
		if strings.HasPrefix(flat, prefix) {
			return ErrUnsafeURL
		}
	}
	return nil
}
