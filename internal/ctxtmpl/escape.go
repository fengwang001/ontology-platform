package ctxtmpl

import "strings"

// Per-context escapers. Replacers scan left to right in a single pass, so
// "&" must be the first pattern in each of them.
var (
	textEscaper = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	doubleQuotedAttrEscaper = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
	)
	singleQuotedAttrEscaper = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"'", "&#39;",
	)
	// Unquoted values are terminated by whitespace and may not contain
	// quotes, "=" or backticks, so all of those are escaped as well.
	unquotedAttrEscaper = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39;",
		"=", "&#61;",
		"`", "&#96;",
		" ", "&#32;",
		"\t", "&#9;",
		"\n", "&#10;",
		"\r", "&#13;",
		"\f", "&#12;",
	)
)

// urlAttrNames are the attributes whose values browsers dereference as URLs.
var urlAttrNames = map[string]bool{
	"href":       true,
	"src":        true,
	"action":     true,
	"formaction": true,
	"cite":       true,
	"xlink:href": true,
}

func isURLAttr(name string) bool {
	return urlAttrNames[strings.ToLower(name)]
}

var dangerousSchemes = []string{"javascript:", "vbscript:", "data:"}

// hasDangerousProtocol reports whether the fully assembled attribute-value
// prefix s begins with a dangerous scheme. It mirrors how browsers sniff a
// scheme: leading whitespace is ignored, and tab/newline/carriage-return
// characters embedded in the scheme are removed before matching.
//
// It must be called with the complete value prefix rendered so far, not with
// a single interpolated value: the original bug checked only the current
// interpolation, so "java" + "script:1" split across two interpolations (or
// between template literal and interpolation) slipped through.
func hasDangerousProtocol(s string) bool {
	s = strings.TrimLeft(s, " \t\n\r\f")
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
	s = strings.ToLower(s)
	for _, scheme := range dangerousSchemes {
		if strings.HasPrefix(s, scheme) {
			return true
		}
	}
	return false
}
