package ctxtmpl

import "strings"

// Each replacer performs a single left-to-right pass, so "&" is
// escaped first and already-escaped output is never re-scanned:
// "<" becomes "&lt;", never "&amp;lt;".
var (
	// HTML text context: & < >
	textReplacer = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	// Double-quoted attribute value: & < > "
	doubleQuoteReplacer = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
	)
	// Single-quoted attribute value: & < > '
	singleQuoteReplacer = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"'", "&#39;",
	)
	// Unquoted attribute value: additionally escape space, tab,
	// newline, '=', backquote and both quotes, so a value cannot
	// break out and inject new attributes.
	unquotedReplacer = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		" ", "&#32;",
		"\t", "&#9;",
		"\n", "&#10;",
		"\r", "&#13;",
		"=", "&#61;",
		"`", "&#96;",
		"'", "&#39;",
		`"`, "&#34;",
	)
	// HTML comment context: escape '-' so the value cannot close
	// the comment early with "-->".
	commentReplacer = strings.NewReplacer("-", "&#45;")
)

func escapeText(s string) string        { return textReplacer.Replace(s) }
func escapeDoubleQuoted(s string) string { return doubleQuoteReplacer.Replace(s) }
func escapeSingleQuoted(s string) string { return singleQuoteReplacer.Replace(s) }
func escapeUnquoted(s string) string    { return unquotedReplacer.Replace(s) }
func escapeComment(s string) string     { return commentReplacer.Replace(s) }
