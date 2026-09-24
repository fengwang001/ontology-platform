package writer

import (
	"strings"

	"ontology/cell"
)

// Table serializes a table using LF record endings and no trailing newline.
func Table(t [][]cell.Cell) string {
	rows := make([]string, len(t))
	for i := range t {
		rows[i] = Record(t[i])
	}
	return strings.Join(rows, "\n")
}

// Field writes one field with minimal quoting.
func Field(c cell.Cell) string {
	v := c.Value
	necessary := c.Quoted
	if !necessary {
		necessary = strings.ContainsAny(v, ",\"\r\n")
	}
	if !necessary {
		return v
	}
	return "\"" + strings.ReplaceAll(v, "\"", "\"\"") + "\""
}

// Record writes one complete record without a line ending.
func Record(cells []cell.Cell) string {
	parts := make([]string, len(cells))
	for i := range cells {
		c := cells[i]
		if len(cells) == 1 && c.Value == "" {
			c.Quoted = true
		}
		parts[i] = Field(c)
	}
	return strings.Join(parts, ",")
}
