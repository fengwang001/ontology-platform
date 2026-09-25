package writer

import (
	"bytes"

	"ontology/cell"
)

// needsQuote 报告裸写是否会破坏 CSV 语法或语义：
// 值含逗号 / 引号 / CR / LF 时必须加引号。
func needsQuote(v []byte) bool {
	return bytes.ContainsAny(v, ",\"\r\n")
}

// Write 以最小引号策略回写记录，所有记录以 \n 终止（规范形式）。
// 加引号当且仅当：字段原本带引号、值含必须转义的字符、
// 或单列表中最后一条裸空字段（否则会被尾随换行规则吞掉）。
func Write(rows [][]cell.Cell) []byte {
	var b bytes.Buffer
	last := len(rows) - 1
	for ri, row := range rows {
		for ci, c := range row {
			forceEmpty := len(row) == 1 && ri == last && ci == 0 &&
				len(c.Value) == 0 && !c.Quoted
			if c.Quoted || needsQuote(c.Value) || forceEmpty {
				b.WriteByte('"')
				for _, x := range c.Value {
					if x == '"' {
						b.WriteByte('"')
					}
					b.WriteByte(x)
				}
				b.WriteByte('"')
			} else {
				b.Write(c.Value)
			}
			if ci < len(row)-1 {
				b.WriteByte(',')
			}
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}
