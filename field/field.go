// Package field parses a single event-stream line into a name/value
// field, following the "name: value" conventions of text/event-stream.
package field

import "strings"

// Field is one parsed line: a name and its value.
type Field struct {
	Name  string
	Value string
}

// Parse splits one line into a Field. It reports ok=false for comment
// lines (starting with ':'), which callers must ignore. A line without
// ':' is a field name with an empty value; exactly one space after the
// colon is stripped from the value.
func Parse(line string) (f Field, ok bool) {
	if strings.HasPrefix(line, ":") {
		return Field{}, false
	}
	name, value, found := strings.Cut(line, ":")
	if !found {
		return Field{Name: line}, true
	}
	value = strings.TrimPrefix(value, " ")
	return Field{Name: name, Value: value}, true
}
