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
		// Root cause of the unquoted-attribute escape: this table's
		// whitespace entries were maintained by hand, separately from the
		// scanner's isASCIISpace, and drifted — '\f' and '\v' were missing,
		// so they were emitted raw even though the scanner treats them as
		// attribute-value terminators. The whitespace entries now live in
		// one named set (unquotedWhitespaceEscapes) whose equality with
		// isASCIISpace is enforced by TestWhitespaceDefinitionsAgree.
		m := map[byte]string{
			'&':  "&amp;",
			'<':  "&lt;",
			'>':  "&gt;",
			'"':  "&quot;",
			'\'': "&#39;",
			'`':  "&#96;",
			'=':  "&#61;",
		}
		for ch, entity := range unquotedWhitespaceEscapes {
			m[ch] = entity
		}
		return m
	case ctxComment:
		return map[byte]string{
			'-': "&#45;",
		}
	default:
		return nil
	}
}

// unquotedWhitespaceEscapes holds the whitespace half of the unquoted
// attribute escape table. It must cover exactly the bytes isASCIISpace
// recognizes: any byte the scanner treats as ending an unquoted value must
// never appear literally in the escaped output.
var unquotedWhitespaceEscapes = map[byte]string{
	' ':  "&#32;",
	'\t': "&#9;",
	'\n': "&#10;",
	'\v': "&#11;",
	'\f': "&#12;",
	'\r': "&#13;",
}
