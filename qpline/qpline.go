// Package qpline 提供 Quoted-Printable 单行级别的编码判定原语：
// 哪些字节必须转义、行尾空白的判定，以及软换行的插入位置。
// 它不依赖其它包，行级编排（硬换行、列位置推进）由 qp 包负责。
package qpline

// MaxColumn 是编码行（不含结尾 \r\n）允许的最大字符数（RFC 2045 的 76）。
const MaxColumn = 76

// IsPrintable 报告字节是否为可打印 ASCII（33–126，含 '='）。
func IsPrintable(b byte) bool { return b >= 33 && b <= 126 }

// IsHorizontalSpace 报告字节是否为空格或制表符。
func IsHorizontalSpace(b byte) bool { return b == ' ' || b == '\t' }

// IsNewline 报告位置 i 处是否为一个换行：裸 '\n' 或 "\r\n"。
// 单独的 '\r'（后不跟 '\n'）不算换行。
func IsNewline(p []byte, i int) bool {
	if i < len(p) && p[i] == '\n' {
		return true
	}
	return i+1 < len(p) && p[i] == '\r' && p[i+1] == '\n'
}

// MustEscape 报告一个非换行字节是否必须写成 =XX。
// 空格与制表符按字面输出（是否位于行末由 TrailingSpace 判定），
// '=' 与其余 33–126 之外的字节必须转义。
func MustEscape(b byte) bool {
	if IsHorizontalSpace(b) {
		return false
	}
	if IsPrintable(b) {
		return b == '='
	}
	return true
}

// TrailingSpace 报告位置 i 处的空格/制表符是否位于（硬）行末：
// 即紧跟一个换行或输入结束。注意软换行不是硬行末，其前的空白不由此判定。
func TrailingSpace(p []byte, i int) bool {
	return i+1 >= len(p) || IsNewline(p, i+1)
}

// BreakBefore 报告在当前列位置 col（0..MaxColumn）提交长度为 tokenLen 的
// 输出令牌前，是否必须先插入软换行 "=\r\n"。tokenLen 为 1（字面字节）或 3（=XX）。
// 规则：放不下则先换行；单字符号在第 76 列（col=75）且其后既非硬换行也非输入
// 结束时也先换行，保证该字符不会成为超长行的开头（该情况只前瞻一次）。
func BreakBefore(col, tokenLen int, atEnd bool) bool {
	if col+tokenLen > MaxColumn {
		return true
	}
	return tokenLen == 1 && col == MaxColumn-1 && !atEnd
}

// HexCode 返回字节 b 的大写 =XX 表示，例如 '=' -> "=3D"、空格 -> "=20"。
func HexCode(b byte) string {
	const digits = "0123456789ABCDEF"
	return "=" + string(digits[b>>4]) + string(digits[b&0x0f])
}

// IsHexDigit 报告 b 是否为十六进制数字（大小写皆可）。
func IsHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'A' && b <= 'F' || b >= 'a' && b <= 'f'
}
