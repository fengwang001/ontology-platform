package writer

import "ontology/cell"

// needsQuote 判断字段回写时是否必须加引号。
func needsQuote(c cell.Cell, cols int) bool {
	if c.Quoted {
		return true
	}
	if len(c.Value) == 0 && cols == 1 {
		return true
	}
	for i := 0; i < len(c.Value); i++ {
		switch c.Value[i] {
		case ',', '"', '\r', '\n':
			return true
		}
	}
	return false
}

// Write 以最小引号回写整张表，无尾换行。
func Write(rows [][]cell.Cell) []byte {
	total := 0
	for ri := range rows {
		for ci := range rows[ri] {
			c := rows[ri][ci]
			total += len(c.Value)
			if needsQuote(c, len(rows[ri])) {
				total += 2
				for i := 0; i < len(c.Value); i++ {
					if c.Value[i] == '"' {
						total++
					}
				}
			}
		}
		if len(rows[ri]) > 0 {
			total += len(rows[ri]) - 1
		}
		if ri+1 < len(rows) {
			total++
		}
	}
	out := make([]byte, 0, total)
	for ri := range rows {
		for ci := range rows[ri] {
			c := rows[ri][ci]
			if ci > 0 {
				out = append(out, ',')
			}
			if needsQuote(c, len(rows[ri])) {
				out = append(out, '"')
				for i := 0; i < len(c.Value); i++ {
					if c.Value[i] == '"' {
						out = append(out, '"', '"')
					} else {
						out = append(out, c.Value[i])
					}
				}
				out = append(out, '"')
			} else {
				out = append(out, c.Value...)
			}
		}
		if ri+1 < len(rows) {
			out = append(out, '\n')
		}
	}
	return out
}
