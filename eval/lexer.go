package eval

import (
	"unicode"
	"unicode/utf8"
)

// lex tokenizes src. It never mutates src; all positions are byte offsets.
func lex(src string) ([]token, error) {
	tokens := make([]token, 0, 16)
	i := 0
	for i < len(src) {
		r, size := utf8.DecodeRuneInString(src[i:])
		if unicode.IsSpace(r) {
			i += size
			continue
		}
		if r >= '0' && r <= '9' || r == '.' {
			start := i
			for i < len(src) {
				c := src[i]
				if (c < '0' || c > '9') && c != '.' {
					break
				}
				i++
			}
			lit := src[start:i]
			if !validLiteral(lit) {
				return nil, posError(ErrBadLiteral, start)
			}
			tokens = append(tokens, token{kind: tokNumber, pos: start, text: lit})
			continue
		}
		kind := singleToken(r)
		if kind == tokEOF {
			return nil, posError(ErrIllegalChar, i)
		}
		tokens = append(tokens, token{kind: kind, pos: i, text: string(r)})
		i += size
	}
	tokens = append(tokens, token{kind: tokEOF, pos: i})
	return tokens, nil
}

// validLiteral accepts only [0-9]+ or [0-9]+\.[0-9]+.
func validLiteral(lit string) bool {
	dot := -1
	for i := 0; i < len(lit); i++ {
		c := lit[i]
		if c == '.' {
			if dot >= 0 {
				return false
			}
			dot = i
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	if dot == 0 || dot == len(lit)-1 {
		return false
	}
	return true
}

func singleToken(r rune) tokenKind {
	switch r {
	case '+':
		return tokPlus
	case '-':
		return tokMinus
	case '*':
		return tokStar
	case '/':
		return tokSlash
	case '(':
		return tokLParen
	case ')':
		return tokRParen
	default:
		return tokEOF
	}
}
