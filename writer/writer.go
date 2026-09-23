// Package writer 以「最小引号」策略把表回写为 CSV，保证逐字段往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 判定一个字段是否必须加引号才能被唯一解析回来。
func needsQuote(c cell.Cell, singleColumn bool) bool {
	if c.Quoted {
		return true // 保留原引号标记
	}
	if strings.ContainsAny(c.Value, ",\"\r\n") {
		return true
	}
	// 单列表里未加引号的空字段会与被跳过的空行无法区分，必须写成 ""。
	return singleColumn && c.Value == ""
}

// writeCell 追加一个字段；单字段内引号转义为 ""，其余字节原样（保留 \r\n）。
func writeCell(b *strings.Builder, c cell.Cell, singleColumn bool) {
	if !needsQuote(c, singleColumn) {
		b.WriteString(c.Value)
		return
	}
	b.WriteByte('"')
	for i := 0; i < len(c.Value); i++ {
		if c.Value[i] == '"' {
			b.WriteByte('"')
		}
		b.WriteByte(c.Value[i])
	}
	b.WriteByte('"')
}

// Write 把表序列化为规范字节：每条记录以单个 \n 结束，记录间无空行。
func Write(t *table.Table) []byte {
	recs := t.Records()
	var b strings.Builder
	for _, rec := range recs {
		single := len(rec) == 1
		for i := range rec {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCell(&b, rec[i], single)
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
