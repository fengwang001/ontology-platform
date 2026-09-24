// Package field parses one event-stream line into a name/value field.
package field

import "bytes"

// Field is a single parsed line: a name and its value.
type Field struct {
	Name  string
	Value string
}

// Parse splits one line into a Field. ok is false for comment lines
// (starting with ':'), which carry no field. A line without ':' is a
// field name with an empty value; exactly one space after the ':' is
// removed from the value.
func Parse(line []byte) (f Field, ok bool) {
	if len(line) > 0 && line[0] == ':' {
		return f, false
	}
	if i := bytes.IndexByte(line, ':'); i >= 0 {
		v := line[i+1:]
		if len(v) > 0 && v[0] == ' ' {
			v = v[1:]
		}
		return Field{Name: string(line[:i]), Value: string(v)}, true
	}
	return Field{Name: string(line)}, true
}
