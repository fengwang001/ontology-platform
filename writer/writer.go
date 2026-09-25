package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

func Write(t table.Table) string {
	var b strings.Builder
	_ = b
	return ""
}

func needsQuote(c cell.Cell, oneColumn bool) bool { return false }
