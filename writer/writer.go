package writer

import (
	"ontology/cell"
	"ontology/table"
)

func Write(t table.Table) []byte {
	rows := make([][]cell.Cell, 0, len(t.Records)+1)
	if t.Header != nil {
		rows = append(rows, t.Header)
	}
	rows = append(rows, t.Records...)
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	out := make([]byte, 0, len(rows)*4)
	for ri, row := range rows {
		for i := 0; i < width; i++ {
			if i > 0 {
				out = append(out, ',')
			}
			switch {
			case i < len(row):
				out = appendCell(out, row[i])
			case width == 1:
				out = appendCell(out, cell.Cell{Quoted: true})
			}
		}
		if ri < len(rows)-1 {
			out = append(out, '\n')
		}
	}
	return out
}

func appendCell(out []byte, c cell.Cell) []byte {
	if !c.Quoted {
		return append(out, c.Value...)
	}
	out = append(out, '"')
	for i := 0; i < len(c.Value); i++ {
		if c.Value[i] == '"' {
			out = append(out, '"')
		}
		out = append(out, c.Value[i])
	}
	return append(out, '"')
}
