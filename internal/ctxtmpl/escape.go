package ctxtmpl

import (
	"strconv"
	"strings"
)

// contextKind identifies the HTML context in which an interpolation sits.
type contextKind int

const (
	ctxText contextKind = iota
	ctxAttrDouble
	ctxAttrSingle
	ctxAttrUnquoted
	ctxComment
)

// escapeHTMLText escapes text-node content: & < >.
func escapeHTMLText(s string) string {
	return escapeByKind(s, ctxText)
}

// escapeAttrValue escapes an attribute value for the given quote style.
func escapeAttrValue(s string, kind contextKind) string {
	return escapeByKind(s, kind)
}

// escapeComment escapes hyphens so injected content cannot close a comment.
func escapeComment(s string) string {
	return escapeByKind(s, ctxComment)
}

// escapeHTMLText escapes text-node content: & < >.
//
// Every character is handled in a single pass and '&' is rewritten to
// '&amp;' directly, so output can never be double-escaped.
func escapeByKind(s string, kind contextKind) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '&':
			b.WriteString("&amp;")
		case c == '<':
			b.WriteString("&lt;")
		case c == '>':
			b.WriteString("&gt;")
		case kind == ctxAttrDouble && c == '"':
			b.WriteString("&#34;")
		case kind == ctxAttrSingle && c == '\'':
			b.WriteString("&#39;")
		case kind == ctxAttrUnquoted && c == '"':
			b.WriteString("&#34;")
		case kind == ctxAttrUnquoted && c == '\'':
			b.WriteString("&#39;")
		case kind == ctxAttrUnquoted && c == '`':
			b.WriteString("&#96;")
		case kind == ctxAttrUnquoted && c == '=':
			b.WriteString("&#61;")
		case kind == ctxAttrUnquoted && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			b.WriteString("&#" + strconv.Itoa(int(c)) + ";")
		case kind == ctxComment && c == '-':
			b.WriteString("&#45;")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
