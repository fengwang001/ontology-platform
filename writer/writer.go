// Package writer 最小引号回写：只在必要时加引号，保证往返。
package writer

import "ontology/cell"

// NeedsQuote 推导「必要」：原本带引号（保留引号标记）、含 , " \r \n，
// 或单列记录的唯一空字段（不加引号会被当成空行跳过而丢记录）。
func NeedsQuote(f cell.Field, singleCol bool) bool {
	if f.Quoted || (singleCol && f.Value == "") {
		return true
	}
	for i := 0; i < len(f.Value); i++ {
		switch f.Value[i] {
		case ',', '"', '\r', '\n':
			return true
		}
	}
	return false
}

func AppendField(dst []byte, f cell.Field, singleCol bool) []byte {
	if !NeedsQuote(f, singleCol) {
		return append(dst, f.Value...)
	}
	dst = append(dst, '"')
	for i := 0; i < len(f.Value); i++ {
		if f.Value[i] == '"' {
			dst = append(dst, '"')
		}
		dst = append(dst, f.Value[i])
	}
	return append(dst, '"')
}

// AppendRecord 写一条记录，以 \n 结尾。
func AppendRecord(dst []byte, fs []cell.Field) []byte {
	for i, f := range fs {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = AppendField(dst, f, len(fs) == 1)
	}
	return append(dst, '\n')
}
