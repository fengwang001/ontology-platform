package writer

import (
	"ontology/cell"
	"ontology/table"
)

func Write(t *table.Table) []byte {
	if t == nil || len(t.Header) == 0 {
		return nil
	}
	var out []byte
	single := len(t.Header) == 1
	out = append(out, WriteRow(t.Header, single)...)
	out = append(out, '\n')
	for _, row := range t.Records {
		out = append(out, WriteRow(row, single)...)
		out = append(out, '\n')
	}
	return out
}

func WriteRow(row []cell.Cell, forceEmptyQuote bool) []byte {
	var out []byte
	for i, c := range row {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, writeCell(c, forceEmptyQuote)...)
	}
	return out
}

func writeCell(c cell.Cell, forceEmptyQuote bool) []byte {
	v := c.Value
	need := c.Quoted || forceEmptyQuote && v == ""
	for _, b := range []byte(v) {
		if b == ',' || b == '"' || b == '\r' || b == '\n' {
			need = true
			break
		}
	}
	if !need {
		return []byte(v)
	}
	out := make([]byte, 0, len(v)+2)
	out = append(out, '"')
	for _, b := range []byte(v) {
		if b == '"' {
			out = append(out, '"', '"')
		} else {
			out = append(out, b)
		}
	}
	out = append(out, '"')
	return out
}
