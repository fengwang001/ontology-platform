package safeexpr

// lexer 在不修改输入字符串的前提下扫描词法单元。
type lexer struct {
	src string
	pos int
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// next 返回下一个 token；非法字符或非法字面量时返回带定位的错误。
func (l *lexer) next() (token, *Error) {
	for l.pos < len(l.src) && isSpace(l.src[l.pos]) {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return token{kind: tokEOF, pos: l.pos}, nil
	}

	b := l.src[l.pos]
	switch b {
	case '+':
		t := token{kind: tokPlus, pos: l.pos}
		l.pos++
		return t, nil
	case '-':
		t := token{kind: tokMinus, pos: l.pos}
		l.pos++
		return t, nil
	case '*':
		t := token{kind: tokStar, pos: l.pos}
		l.pos++
		return t, nil
	case '/':
		t := token{kind: tokSlash, pos: l.pos}
		l.pos++
		return t, nil
	case '(':
		t := token{kind: tokLParen, pos: l.pos}
		l.pos++
		return t, nil
	case ')':
		t := token{kind: tokRParen, pos: l.pos}
		l.pos++
		return t, nil
	}

	if isDigit(b) || b == '.' {
		return l.scanNumber()
	}
	return token{}, newError(ErrIllegalChar, "字符 "+quoteByte(b)+" 不被允许", l.pos)
}

// scanNumber 消费最长的数字/小数点序列并严格校验字面量形态。
// 合法形态为 [0-9]+(\.[0-9]+)?；调用时当前字符为数字或 '.'。
func (l *lexer) scanNumber() (token, *Error) {
	start := l.pos

	if l.src[start] == '.' {
		// 如 .5、..5：小数点前缺少整数部分。
		for l.pos < len(l.src) && (isDigit(l.src[l.pos]) || l.src[l.pos] == '.') {
			l.pos++
		}
		return token{}, newError(ErrMalformedNumber, "数字字面量缺少整数部分", start)
	}

	for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
		l.pos++
	}

	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		dotPos := l.pos
		l.pos++
		for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
			l.pos++
		}
		if l.pos < len(l.src) && l.src[l.pos] == '.' {
			// 如 1.2.3：第二个小数点多余。
			return token{}, newError(ErrMalformedNumber, "数字字面量含多个小数点", l.pos)
		}
		if l.pos == dotPos+1 {
			// 如 5.：小数点后没有数字。
			return token{}, newError(ErrMalformedNumber, "数字字面量缺少小数部分", dotPos)
		}
	}

	return token{kind: tokNumber, pos: start, text: l.src[start:l.pos]}, nil
}

func quoteByte(b byte) string {
	return "'" + string(rune(b)) + "'"
}
