package ontology

import "math/big"

// parser 是递归下降解析器。文法（唯一解析，无歧义）：
//
//	expr    := term (('+' | '-') term)*
//	term    := factor (('*' | '/') factor)*
//	factor  := '-' factor | primary
//	primary := number | '(' expr ')'
//
// 优先级：括号 > 一元负号 > * / > + -；同级左结合。
type parser struct {
	toks      []token
	i         int
	end       int     // 输入总长度，用于 EOF 定位
	openStack []token // 尚未闭合的 '('
	openCount int     // 已出现的 '(' 总数，用于编号
}

// Parse 把表达式字符串解析为唯一的语法树；不修改 input。
func Parse(input string) (Node, error) {
	toks, err := lex(input)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, errAt(ErrEmpty, -1, "")
	}
	p := &parser{toks: toks, end: len(input)}
	n, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.i < len(p.toks) {
		t := p.toks[p.i]
		if t.kind == tokRParen {
			return nil, errAt(ErrParen, t.pos, "右括号没有匹配的开括号")
		}
		return nil, errAt(ErrIllegalChar, t.pos, "此处不应出现 %s", t.describe())
	}
	return n, nil
}

func (p *parser) peek() token {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return token{kind: tokEOF, pos: p.end}
}

func (p *parser) parseExpr() (Node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tokPlus && t.kind != tokMinus {
			return left, nil
		}
		p.i++
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		op := byte('+')
		if t.kind == tokMinus {
			op = '-'
		}
		left = &Binary{Op: op, L: left, R: right, Pos: t.pos}
	}
}

func (p *parser) parseTerm() (Node, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tokStar && t.kind != tokSlash {
			return left, nil
		}
		p.i++
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		op := byte('*')
		if t.kind == tokSlash {
			op = '/'
		}
		left = &Binary{Op: op, L: left, R: right, Pos: t.pos}
	}
}

func (p *parser) parseFactor() (Node, error) {
	t := p.peek()
	if t.kind == tokMinus {
		p.i++
		x, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return &Unary{X: x, Pos: t.pos}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Node, error) {
	t := p.peek()
	switch t.kind {
	case tokNum:
		p.i++
		return litNode(t)
	case tokLParen:
		p.i++
		p.openCount++
		t.ord = p.openCount
		p.openStack = append(p.openStack, t)
		n, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokRParen {
			open := p.openStack[len(p.openStack)-1]
			return nil, errAt(ErrParen, open.pos, "第 %d 个开括号未闭合", open.ord)
		}
		p.openStack = p.openStack[:len(p.openStack)-1]
		p.i++
		return n, nil
	case tokRParen:
		if len(p.openStack) == 0 {
			return nil, errAt(ErrParen, t.pos, "右括号没有匹配的开括号")
		}
		return nil, errAt(ErrIllegalChar, t.pos, "此处缺少操作数")
	case tokEOF:
		return nil, errAt(ErrIllegalChar, t.pos, "此处缺少操作数")
	}
	return nil, errAt(ErrIllegalChar, t.pos, "此处不应出现 %s", t.describe())
}

// litNode 把字面量记号精确解析为 big.Rat，并做可表示性检查：
// 整数值必须落在 int64 内，小数值必须能被 float64 精确表示。
func litNode(t token) (Node, error) {
	r, ok := new(big.Rat).SetString(t.text)
	if !ok {
		return nil, errAt(ErrBadLiteral, t.pos, "无法解析 %q", t.text)
	}
	if r.IsInt() {
		if !r.Num().IsInt64() {
			return nil, errAt(ErrInexact, t.pos, "整数字面量 %s 超出 int64 范围", t.text)
		}
		return &Lit{Text: t.text, Pos: t.pos, rat: r}, nil
	}
	if _, exact := r.Float64(); !exact {
		return nil, errAt(ErrInexact, t.pos, "小数字面量 %s 无法被 float64 精确表示", t.text)
	}
	return &Lit{Text: t.text, Pos: t.pos, rat: r}, nil
}
