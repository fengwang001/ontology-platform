// Package writer 用最小引号把表回写为 CSV，并保证往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 判断除「保持引号标记」之外，语法上必须加引号的情形：
// 含逗号、引号、\r、\n。
func needsQuote(s string) bool {
	return strings.ContainsAny(s, ",\"\r\n")
}

// encode 编码一个字段。
func encode(c cell.Cell, singleCol bool, dst []byte) []byte {
	emptySingle := singleCol && len(c.Value) == 0
	if c.Quoted || needsQuote(c.Value) || emptySingle {
		dst = append(dst, '"')
		dst = appendEscaped(dst, c.Value)
		dst = append(dst, '"')
		return dst
	}
	return append(dst, c.Value...)
}

func appendEscaped(dst []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			dst = append(dst, '"', '"')
			continue
		}
		dst = append(dst, s[i])
	}
	return dst
}

// Write 回写整个表（含表头行）。规范输出：字段内 \r\n 原样保留，
// 记录间以 \n 分隔，最后一条记录也以 \n 结束（空表输出空字节）。
func Write(t *table.Table) []byte {
	var dst []byte
	single := t.Width() == 1
	writeRow := func(row []cell.Cell) {
		for i := range row {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = encode(row[i], single, dst)
		}
		dst = append(dst, '\n')
	}
	if t.Header != nil {
		writeRow(t.Header)
	}
	for _, r := range t.Records {
		writeRow(r)
	}
	return dst
}
