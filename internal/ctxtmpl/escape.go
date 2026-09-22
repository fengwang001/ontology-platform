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
		t := map[byte]string{
			'&':  "&amp;",
			'<':  "&lt;",
			'>':  "&gt;",
			'"':  "&quot;",
			'\'': "&#39;",
			'`':  "&#96;",
			'=':  "&#61;",
		}
		// Encode exactly the bytes the scanner (isASCIISpace) treats as
		// ending an unquoted value. The table used to list space/tab/LF/CR
		// only, so '\f' and '\v' passed through and injected a new attribute.
		for sp, entity := range asciiSpaceEntities {
			t[sp] = entity
		}
		return t
	case ctxComment:
		return map[byte]string{
			'-': "&#45;",
		}
	default:
		return nil
	}
}
