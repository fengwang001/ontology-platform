package ontology

import (
	"unicode"
	"unicode/utf8"
)

type tokKind int

const (
	tokEOF tokKind = iota
	tokNum
	tokPlus
	tokMinus
	tokStar
	tokSlash
	tokLParen
	tokRParen
)

// token 是词法单元；pos 为输入中的字节偏移，ord 为开括号序号。
type token struct {
	kind tokKind
	text string
	pos  int
	ord  int
}

// describe 返回记号的可读描述，用于错误信息。
func (t token) describe() string {
	switch t.kind {
	case tokNum:
		return "字面量 " + t.text
	case tokPlus:
		return "+"
	case tokMinus:
		return "-"
	case tokStar:
		return "*"
	case tokSlash:
		return "/"
	case tokLParen:
		return "("
	case tokRParen:
		return ")"
	}
	return "输入结尾"
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// lex 把输入切成记号流；只读 input，不做任何改写。
func lex(input string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(input) {
		r, size := utf8.DecodeRuneInString(input[i:])
		if unicode.IsSpace(r) {
			i += size
			continue
		}
		c := input[i]
		switch {
		case isDigit(c):
			tok, next, err := lexNumber(input, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, tok)
			i = next
		case c == '.':
			return nil, errAt(ErrBadLiteral, i, "小数点前后缺少数字")
		case c == '+':
			toks = append(toks, token{kind: tokPlus, pos: i})
			i++
		case c == '-':
			toks = append(toks, token{kind: tokMinus, pos: i})
			i++
		case c == '*':
			toks = append(toks, token{kind: tokStar, pos: i})
			i++
		case c == '/':
			toks = append(toks, token{kind: tokSlash, pos: i})
			i++
		case c == '(':
			toks = append(toks, token{kind: tokLParen, pos: i})
			i++
		case c == ')':
			toks = append(toks, token{kind: tokRParen, pos: i})
			i++
		default:
			return nil, errAt(ErrIllegalChar, i, "字符 %q", r)
		}
	}
	return toks, nil
}

// lexNumber 从 start 处扫描 [0-9]+(\.[0-9]+)? 形式的字面量。
func lexNumber(input string, start int) (token, int, error) {
	i := start
	for i < len(input) && isDigit(input[i]) {
		i++
	}
	if i < len(input) && input[i] == '.' {
		i++
		frac := i
		for i < len(input) && isDigit(input[i]) {
			i++
		}
		if i == frac {
			return token{}, 0, errAt(ErrBadLiteral, start, "小数点后缺少数字")
		}
		if i < len(input) && input[i] == '.' {
			return token{}, 0, errAt(ErrBadLiteral, i, "字面量含有多个小数点")
		}
	}
	return token{kind: tokNum, text: input[start:i], pos: start}, i, nil
}
