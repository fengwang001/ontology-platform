// Package writer 实现最小引号回写：只在必要时加引号，并保证往返。
package writer

import (
	"strings"

	"ontology/cell"
	"ontology/table"
)

// needsQuote 判定「必要」的加引号情形：
// 值含逗号/引号/CR/LF；单列记录的唯一空字段（否则序列化结果是空行，
// 再解析会被跳过而丢数据）；原单元格本身带引号（否则往返后引号标记改变）。
func needsChoice(c cell.Cell, singleColumn bool) (string, bool) {
	v := c.Value
	needed := c.Quoted || strings.ContainsAny(v, ",\"\r\n") || (singleColumn && v == "")
	if !needed {
		return v, false
	}
	var b strings.Builder
	b.Grow(len(v) + 2)
	b.WriteByte('"')
	for i := 0; i < len(v); i++ {
		if v[i] == '"' {
			b.WriteByte('"')
		}
		b.WriteByte(v[i])
	}
	b.WriteByte('"')
	return b.String(), true
}

// WriteCell 回写单个单元格（singleColumn 表明它是单列表的唯一字段）。
func WriteCell(c cell.Cell, singleColumn bool) string {
	s, _ := needsChoice(c, singleColumn)
	return s
}

// WriteRecord 回写一条记录，末尾带 \n。引号字段内的 \r\n 原样保留。
func WriteRecord(rec []cell.Cell) string {
	var b strings.Builder
	one := len(rec) == 1
	for i, c := range rec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(WriteCell(c, one))
	}
	b.WriteByte('\n')
	return b.String()
}

// Write 回写整张表；规范输入下 Parse(Write(t)) 与 t 逐字段、逐引号标记相等。
func Write(t *table.Table) string {
	var b strings.Builder
	for _, rec := range t.Records {
		b.WriteString(WriteRecord(rec))
	}
	return b.String()
}
