package runs

import (
	"math/big"
	"unicode/utf8"
)

// Run 表示一个最长游程：码点 Rune 连续出现 Count 次。
type Run struct {
	Count *big.Int
	Rune  rune
}

// Split 把 UTF-8 字符串切成最长游程序列（码点相等即合并，不做规范化）。
func Split(s string) []Run {
	return nil
}

// AppendCount 把无符号无前导零的十进制次数追加到 b；次数 1 不追加。
func AppendCount(b []byte, n *big.Int) []byte {
	return b
}

// AppendSymbol 按格式追加一个符号：ASCII 数字与反斜杠加 '\' 转义。
func AppendSymbol(b []byte, r rune) []byte {
	return b
}

// SymbolLen 返回符号在编码文本中的字节长度（可能含转义符）。
func SymbolLen(r rune) int {
	return utf8.RuneLen(r)
}
