package ontology

import "math/big"

// 文法（保证解析唯一）：
//
//	expr    := term (('+' | '-') term)*
//	term    := factor (('*' | '/') factor)*
//	factor  := '-' factor | primary
//	primary := NUMBER | '(' expr ')'
//
// 优先级：括号 > 一元负号 > * / > + -；同级左结合。
type parser struct {
	toks  []token
	i     int
	opens int // 已遇到的 '(' 总数，用于给未闭合括号编号
}

// Parse 把表达式解析为 AST，只读取输入，不做任何预处理或改写。
func Parse(s string) (Node, error) {
	toks, err := lex(s)
	if err != nil {
		return nil, err
	}
	if toks[0].kind == tEOF {
		return nil, errPlain(ErrEmptyExpr, "表达式为空或只含空白")
	}
	p := &parser{toks: toks}
	n, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	switch t := p.cur(); t.kind {
	case tEOF:
		return n, nil
	case tRParen:
		return nil, errAt(ErrUnmatchedParen, t.pos, "多余的右括号，没有对应的开括号")
	default:
		return nil, errAt(ErrIllegalChar, t.pos, "此处出现意外的记号 %q", p.describe(t))
	}
}

func (p *parser) cur() token  { return p.toks[p.i] }
func (p *parser) next() token { t := p.toks[p.i]; p.i++; return t }

func (p *parser) describe(t token) string {
	if t.kind == tNum {
		return t.text
	}
	if t.kind == tEOF {
		return "表达式结尾"
	}
	return tokName(t.kind)
}

func tokName(k tokKind) string {
	switch k {
	case tPlus:
		return "+"
	case tMinus:
		return "-"
	case tStar:
		return "*"
	case tSlash:
		return "/"
	case tLParen:
		return "("
	case tRParen:
		return ")"
	}
	return "?"
}

func (p *parser) parseExpr() (Node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tPlus || p.cur().kind == tMinus {
		op := p.next()
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &binNode{op: opByte(op.kind), l: left, r: right, pos: op.pos}
	}
	return left, nil
}

func (p *parser) parseTerm() (Node, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tStar || p.cur().kind == tSlash {
		op := p.next()
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = &binNode{op: opByte(op.kind), l: left, r: right, pos: op.pos}
	}
	return left, nil
}

func (p *parser) parseFactor() (Node, error) {
	if p.cur().kind == tMinus {
		p.next()
		x, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return &negNode{x: x}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Node, error) {
	t := p.cur()
	switch t.kind {
	case tNum:
		p.next()
		rat, ok := new(big.Rat).SetString(normalizeNumber(t.text))
		if !ok {
			return nil, errAt(ErrBadLiteral, t.pos, "非法字面量 %q", t.text)
		}
		return &numNode{text: t.text, rat: rat}, nil
	case tLParen:
		p.next()
		p.opens++
		ordinal := p.opens
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.cur().kind != tRParen {
			return nil, errAt(ErrUnmatchedParen, t.pos,
				"第 %d 个开括号未闭合", ordinal)
		}
		p.next()
		return inner, nil
	case tEOF:
		return nil, errAt(ErrIllegalChar, t.pos, "表达式在此处意外结束")
	default:
		return nil, errAt(ErrIllegalChar, t.pos, "此处出现意外的记号 %q", p.describe(t))
	}
}

func opByte(k tokKind) byte {
	switch k {
	case tPlus:
		return '+'
	case tMinus:
		return '-'
	case tStar:
		return '*'
	case tSlash:
		return '/'
	}
	return 0
}

// normalizeNumber 把 ".5"、"5." 规范为 big.Rat 可解析的形式。
func normalizeNumber(s string) string {
	if s[0] == '.' {
		s = "0" + s
	}
	if s[len(s)-1] == '.' {
		s = s + "0"
	}
	return s
}
