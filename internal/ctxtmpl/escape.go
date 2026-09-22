package ctxtmpl

import "strings"

// escapeValue applies context-appropriate escaping to a raw value. Escaping is
// performed in a single pass so '&' is encoded exactly once.
func escapeValue(v string, c context) string {
	esc := escaperFor(c.kind)
	if esc == nil {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		ch := v[i]
		if repl, ok := esc[ch]; ok {
			b.WriteString(repl)
		} else {
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// escaperFor returns the byte -> entity replacement table for a context kind.
// Every table encodes '&' first and only once because each input byte is
// visited a single time.
func escaperFor(k contextKind) map[byte]string {
	switch k {
	case ctxText:
		return map[byte]string{
			'&': "&amp;",
			'<': "&lt;",
			'>': "&gt;",
		}
	case ctxAttrDouble:
		return map[byte]string{
			'&': "&amp;",
			'<': "&lt;",
			'>': "&gt;",
			'"': "&quot;",
		}
	case ctxAttrSingle:
		return map[byte]string{
			'&':  "&amp;",
			'<':  "&lt;",
			'>':  "&gt;",
			'\'': "&#39;",
		}
	case ctxAttrUnquoted:
		return unquotedEscaper()
	case ctxComment:
		return map[byte]string{
			'-': "&#45;",
		}
	default:
		return nil
	}
}

// unquotedPunctuationEscaper is the whitespace-independent part of the
// unquoted-attribute escape table.
var unquotedPunctuationEscaper = map[byte]string{
	'&':  "&amp;",
	'<':  "&lt;",
	'>':  "&gt;",
	'"':  "&quot;",
	'\'': "&#39;",
	'`':  "&#96;",
	'=':  "&#61;",
}

// unquotedEscaper builds the unquoted-attribute table as punctuation plus
// every byte isASCIISpace recognizes. The whitespace entries must be derived
// from isASCIISpace, not listed by hand: the scanner (scanner.go) ends an
// unquoted value at exactly those bytes, so any byte the scanner treats as
// whitespace but this table leaves raw — previously '\f' and '\v', which a
// hand-maintained list omitted — passes through unescaped and can terminate
// the value in the browser, letting a value inject a new attribute name.
func unquotedEscaper() map[byte]string {
	esc := make(map[byte]string, len(unquotedPunctuationEscaper)+6)
	for ch, repl := range unquotedPunctuationEscaper {
		esc[ch] = repl
	}
	for b := 0; b < 256; b++ {
		ch := byte(b)
		if isASCIISpace(ch) {
			esc[ch] = "&#" + itoa(b) + ";"
		}
	}
	return esc
}
