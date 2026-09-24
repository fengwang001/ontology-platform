// Package field parses one SSE text line into a field name and value.
package field

// Field is a parsed line. Comment is true for lines starting with ':'.
type Field struct {
	Name    string
	Value   string
	Comment bool
}

// Parse splits "name: value". Rules:
//   - a line starting with ':' is a comment and is ignored;
//   - without ':' the whole line is the name and the value is empty;
//   - at most one leading space after ':' is stripped.
func Parse(line string) Field {
	if len(line) > 0 && line[0] == ':' {
		return Field{Comment: true}
	}
	for i := 0; i < len(line); i++ {
		if line[i] != ':' {
			continue
		}
		name := line[:i]
		value := line[i+1:]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		return Field{Name: name, Value: value}
	}
	return Field{Name: line}
}
