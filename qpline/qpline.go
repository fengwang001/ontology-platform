// Package qpline 处理 Quoted-Printable 单个逻辑行的编码判定：
// 哪些字节必须转义、行尾空白如何处理、软换行插入在哪里。
// 本包不知道换行与多行的存在。
package qpline

import "encoding/hex"

// MaxLineLen 是一条物理行（不含行尾 \r\n）允许的最大字符数。
const MaxLineLen = 76

// NeedsEscape 报告字节是否必须以 =XX 形式输出。
// 可打印 ASCII 33..126 原样输出，但 '=' 除外；空格与制表符
// 是行内空白，默认不转义（行末情形由 EncodeLine 单独处理）。
func NeedsEscape(b byte) bool {
	if b == '=' {
		return true
	}
	return b < 33 || b > 126
}

// TokenWidth 返回字节在编码行中占据的列数：
// 原字符宽 1，转义序列 =XX 宽 3（且不可拆分）。
func TokenWidth(b byte) int {
	if NeedsEscape(b) {
		return 3
	}
	return 1
}

// EncodeLine 把一个不含换行符的逻辑行追加编码到 dst 并返回。
//
// 行末（逻辑行最后一个字节）若为空格或制表符，必须转义，
// 因为硬换行之后解码器会将行末空白视为非法；而软换行 =\r\n
// 并非逻辑行结束，其前的空格属于行内空白，保持原样。
//
// 每个 token 原子装填：非末 token 之后至少还要容纳 1 列，
// 故上限分别为 75 与 76；放不下就在该 token 前插入软换行。
func EncodeLine(dst, line []byte) []byte {
	if len(line) == 0 {
		return dst
	}
	last := len(line) - 1
	trailingWS := line[last] == ' ' || line[last] == '\t'
	col := 0
	for i := 0; i < len(line); i++ {
		b := line[i]
		escape := NeedsEscape(b)
		width := TokenWidth(b)
		if i == last && trailingWS {
			escape = true
			width = 3
		}
		limit := MaxLineLen - 1 // 非末 token：给后续至少留 1 列
		if i == last {
			limit = MaxLineLen
		}
		if col+width > limit {
			dst = append(dst, '=', '\r', '\n')
			col = 0
		}
		if escape {
			dst = append(dst, '=')
			dst = hex.AppendEncoded(dst, []byte{b})
		} else {
			dst = append(dst, b)
		}
		col += width
	}
	return dst
}
