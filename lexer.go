package ontology

type tokKind int

const (
	tNum tokKind = iota
	tPlus
	tMinus
	tStar
	tSlash
	tLParen
	tRParen
	tEOF
)

type token struct {
	kind tokKind
	text string // 仅数字字面量使用
	pos  int    // 字节偏移
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// validNumber 校验字面量：至多一个小数点，至少一位数字。
func validNumber(s string) bool {
	dots, digits := 0, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '.':
			dots++
		case isDigit(s[i]):
			digits++
		default:
			return false
		}
	}
	return dots <= 1 && digits >= 1
}

// lex 把输入扫描为 token 序列，末尾固定追加 tEOF。
// 输入字符串只被读取，绝不被修改。
func lex(s string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case isSpace(c):
			i++
		case isDigit(c) || c == '.':
			start := i
			for i < len(s) && (isDigit(s[i]) || s[i] == '.') {
				i++
			}
			text := s[start:i]
			if !validNumber(text) {
				return nil, errAt(ErrBadLiteral, start, "非法字面量 %q", text)
			}
			toks = append(toks, token{kind: tNum, text: text, pos: start})
		case c == '+':
			toks = append(toks, token{kind: tPlus, pos: i})
			i++
		case c == '-':
			toks = append(toks, token{kind: tMinus, pos: i})
			i++
		case c == '*':
			toks = append(toks, token{kind: tStar, pos: i})
			i++
		case c == '/':
			toks = append(toks, token{kind: tSlash, pos: i})
			i++
		case c == '(':
			toks = append(toks, token{kind: tLParen, pos: i})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen, pos: i})
			i++
		default:
			return nil, errAt(ErrIllegalChar, i, "非法字符 %q", rune(c))
		}
	}
	toks = append(toks, token{kind: tEOF, pos: len(s)})
	return toks, nil
}
