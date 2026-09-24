// Package qpline implements single-line Quoted-Printable encoding decisions.
package qpline

// MaxLen is the maximum encoded line length excluding the trailing CRLF.
const MaxLen = 76

// NeedEscape reports whether b must be emitted as an =XX escape atom.
func NeedEscape(b byte) bool {
	if b >= 33 && b <= 126 && b != '=' {
		return false
	}
	return b != '\t' && b != ' '
}

// AppendContent appends the encoding of a single content byte to dst.
// Trailing line whitespace must be escaped by the caller beforehand.
func AppendContent(dst []byte, b byte) []byte {
	if b == ' ' || b == '\t' || !NeedEscape(b) {
		return append(dst, b)
	}
	return append(dst, '=', hex[b>>4], hex[b&0x0f])
}

// AppendSoftBreak appends a Quoted-Printable soft line break.
func AppendSoftBreak(dst []byte) []byte {
	return append(dst, '=', '\r', '\n')
}

// AppendEscaped appends b as an =XX atom.
func AppendEscaped(dst []byte, b byte) []byte {
	return append(dst, '=', hex[b>>4], hex[b&0x0f])
}

// Width returns the encoded width of content byte b: 3 if escaped, else 1.
func Width(b byte) int {
	if b == ' ' || b == '\t' || !NeedEscape(b) {
		return 1
	}
	return 3
}

const hex = "0123456789ABCDEF"
