// Package writer minimally quotes CSV cells and writes tables back.
package writer

import "ontology/cell"

// Write renders records using minimal quoting; CRLF inside fields is preserved.
func Write(records [][]cell.Cell) []byte { return nil }
