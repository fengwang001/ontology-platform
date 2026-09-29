// Package writer writes tables back to CSV with minimal quoting.
package writer

import (
	"strings"

	"ontology/cell"
)

// needsQuote reports whether a cell must be quoted. columns is the number
// of columns in its record; an empty sole cell of a 1-column record must be
// quoted or its row would parse as a skipped blank line.
func needsQuote(c cell.Cell, columns int) bool {
	if c.Quoted || len(c.Value) == 0 && columns == 1 {
		return true
	}
	return strings.ContainsAny(c.Value, ",\"\r\n")
}

// WriteField renders one field.
func WriteField(c cell.Cell, columns int) string { return "" }

// Write renders a full table; each record ends with LF.
func Write(rows [][]cell.Cell) []byte { return nil }
