package safeexpr

// tokenKind 标识词法单元种类。
type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNumber
	tokPlus
	tokMinus
	tokStar
	tokSlash
	tokLParen
	tokRParen
)

// token 为一个词法单元。Text 仅数字字面量使用。
type token struct {
	kind tokenKind
	// Pos 为 token 首字节在原始表达式中的下标（从 0 开始）。
	pos  int
	text string
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func kindString(k tokenKind) string {
	switch k {
	case tokEOF:
		return "结尾"
	case tokNumber:
		return "数字"
	case tokPlus:
		return "'+'"
	case tokMinus:
		return "'-'"
	case tokStar:
		return "'*'"
	case tokSlash:
		return "'/'"
	case tokLParen:
		return "'('"
	case tokRParen:
		return "')'"
	default:
		return "未知"
	}
}
