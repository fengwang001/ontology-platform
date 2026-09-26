// Package writer 用最小引号把表回写为 CSV。
package writer

import "ontology/cell"

// needsQuote 判断字段是否必须加引号：
// 原字段加引号，或内容含逗号、引号、CR、LF。
func needsQuote(c cell.Cell) bool {
	if c.Quoted {
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

// Write 把字段行（含表头）回写为规范 CSV：记录以 \n 结束。
func Write(rows [][]cell.Cell) []byte {
	var b []byte
	for _, rec := range rows {
		for i, c := range rec {
			if i > 0 {
				b = append(b, ',')
			}
			if needsQuote(c) {
				b = append(b, '"')
				for i := 0; i < len(c.Value); i++ {
					if c.Value[i] == '"' {
						b = append(b, '"', '"')
					} else {
						b = append(b, c.Value[i])
					}
				}
				b = append(b, '"')
			} else {
				b = append(b, c.Value...)
			}
		}
		b = append(b, '\n')
	}
	return b
}
