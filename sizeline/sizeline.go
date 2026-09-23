// Package sizeline 实现块大小行的编码与解析：十六进制长度、可选 ;k=v
// 扩展以及带引号值的转义。本包不依赖工程内其他包。
package sizeline

import "strconv"

// Ext 是一个块扩展键值对。
type Ext struct {
	Key   string
	Value string
}

// Encode 编码一行块大小行（含结尾 CRLF）。size=0 且 exts=nil 是流结束行。
func Encode(size int, exts []Ext) []byte {
	b := strconv.AppendInt(nil, int64(size), 16)
	for _, e := range exts {
		b = append(b, ';')
		b = append(b, e.Key...)
		b = append(b, '=')
		b = appendValue(b, e.Value)
	}
	return append(b, '\r', '\n')
}

// EncodedExtLen 返回扩展部分编码后的字节数（不含十六进制长度与 CRLF）。
func EncodedExtLen(exts []Ext) int {
	n := 0
	for _, e := range exts {
		n += 1 + len(e.Key) + 1 + valueLen(e.Value)
	}
	return n
}

// needsQuote 判断值是否必须加引号：出现分号、等号、引号、反斜杠或 CRLF 时。
func needsQuote(v string) bool {
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case ';', '=', '"', '\\', '\r', '\n':
			return true
		}
	}
	return false
}

// appendValue 追加（必要时加引号并转义的）值；转义不引入任何裸 ;=" 字符，
// 因此转义后的字节不可能改变块大小行的解析结构。
func appendValue(b []byte, v string) []byte {
	if !needsQuote(v) {
		return append(b, v...)
	}
	b = append(b, '"')
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '\\':
			b = append(b, '\\', '\\')
		case '"':
			b = append(b, '\\', '"')
		case '\r':
			b = append(b, '\\', 'r')
		case '\n':
			b = append(b, '\\', 'n')
		default:
			b = append(b, v[i])
		}
	}
	return append(b, '"')
}

func valueLen(v string) int {
	if !needsQuote(v) {
		return len(v)
	}
	n := 2 // 两端引号
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '\\', '"', '\r', '\n':
			n += 2
		default:
			n++
		}
	}
	return n
}
