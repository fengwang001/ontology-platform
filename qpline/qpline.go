// Package qpline 实现 Quoted-Printable 单行级别的编码判定：
// 每个输入字节对应一个不可拆分的输出 token（字面字符或 =XX），
// 以及软换行 =\\r\\n 的插入位置。它不依赖其他包。
package qpline

// MaxLine 是一行（不含行尾 \r\n）允许的最大字符数。
const MaxLine = 76

const hexdigits = "0123456789ABCDEF"

// Literal 判断字节 b 是否可以原样输出：33..126 且不等于 '='。
// 空格(32)与 TAB(9)是否字面取决于是否处于行末，由 EncodedWidth 的
// atLineEnd 参数控制，故这里返回 false。
func Literal(b byte) bool {
	return b != '=' && b >= 33 && b <= 126
}

// EncodedWidth 返回字节 b 作为 token 占的输出字符数：字面为 1，否则 =XX 为 3。
// atLineEnd 表示 b 之后紧跟换行或输入结束：此时空格/TAB 必须转义。
func EncodedWidth(b byte, atLineEnd bool) int {
	if Literal(b) || ((b == ' ' || b == '\t') && !atLineEnd) {
		return 1
	}
	return 3
}

// AppendToken 把字节 b 的编码 token 追加到 dst。atLineEnd 含义同 EncodedWidth。
func AppendToken(dst []byte, b byte, atLineEnd bool) []byte {
	if Literal(b) || ((b == ' ' || b == '\t') && !atLineEnd) {
		return append(dst, b)
	}
	return append(dst, '=', hexdigits[b>>4], hexdigits[b&0xf])
}

// EncodeLine 编码不含换行符的一行 line，末尾追加 endLine：
// endLine 为 true 表示其后是硬换行或输入结束（行末空白必须转义）；
// false 表示行太长、其后将由调用方插入软换行（空格允许落在软换行前）。
// 返回追加后的结果。col 是该行已经占用的字符数（通常为 0）。
// EncodeLine 本身不插入软换行；行长控制由配合 Fits 的调用方完成。
func EncodeLine(dst []byte, line []byte, col int, endLine bool) []byte {
	for i := 0; i < len(line); i++ {
		b := line[i]
		atEnd := endLine && i == len(line)-1
		dst = AppendToken(dst, b, atEnd)
	}
	return dst
}

// Fits 判断在当前列 col 上，宽度为 width 的 token 能否放入本行。
// last 为 true 时该 token 是本行最后一个 token（下一个是硬换行或 EOF），
// 允许占满 76；否则其后还需容纳软换行的 '='，上限为 75。
func Fits(col, width int, last bool) bool {
	limit := MaxLine
	if !last {
		limit = MaxLine - 1
	}
	return col+width <= limit
}

// SoftBreak 返回软换行序列。
func SoftBreak() []byte { return []byte("=\r\n") }
