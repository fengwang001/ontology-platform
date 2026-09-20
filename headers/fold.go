package headers

import "strings"

// appendFold appends an obs-fold continuation line to a pending field value.
// The leading whitespace of the continuation is collapsed to a single space,
// and trailing whitespace of the accumulated value is removed first.
func appendFold(value, continuation string) string {
	trimmed := strings.TrimLeft(continuation, " \t")
	prefix := strings.TrimRight(value, " \t")
	if prefix == "" {
		return trimmed
	}
	return prefix + " " + trimmed
}

// trimValue removes leading and trailing spaces and tabs; whitespace inside
// the value is preserved.
func trimValue(v string) string {
	return strings.Trim(v, " \t")
}

func isFoldingLine(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}
