package eval

// parser turns a token stream into a unique parse tree.
//
// Grammar (precedence low to high, binary ops left-associative):
//
//	expr   := term (('+'|'-') term)*
//	term   := factor (('*'|'/') factor)*
//	factor := '-' factor | atom
//	atom   := NUMBER | '(' expr ')'
type parser struct {
	tokens []token
	pos    int

	openPos     []int
	openOrdinal []int
	ordinal     int
}

func parse(tokens []token) (Node, error) {
	if len(tokens) == 1 && tokens[0].kind == tokEOF {
		return nil, ErrEmptyExpression
	}
	p := &parser{tokens: tokens}
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.cur().kind != tokEOF {
		if p.cur().kind == tokRParen {
			return nil, posError(ErrUnmatchedParen, p.cur().pos)
		}
		return nil, posError(ErrIllegalChar, p.cur().pos)
	}
	if len(p.openPos) > 0 {
		idx := len(p.openPos) - 1
		return nil, &PosError{
			Err:     ErrUnmatchedParen,
			Pos:     p.openPos[idx],
			Ordinal: p.openOrdinal[idx],
		}
	}
	return node, nil
}

func (p *parser) cur() token { return p.tokens[p.pos] }

func (p *parser) advance() token {
	t := p.tokens[p.pos]
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}

func (p *parser) parseExpr() (Node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tokPlus || p.cur().kind == tokMinus {
		op := p.advance()
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &BinaryNode{Left: left, Right: right, Op: op.text[0], pos: op.pos}
	}
	return left, nil
}

func (p *parser) parseTerm() (Node, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tokStar || p.cur().kind == tokSlash {
		op := p.advance()
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = &BinaryNode{Left: left, Right: right, Op: op.text[0], pos: op.pos}
	}
	return left, nil
}

func (p *parser) parseFactor() (Node, error) {
	if p.cur().kind == tokMinus {
		op := p.advance()
		operand, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return &UnaryNode{Operand: operand, pos: op.pos}, nil
	}
	return p.parseAtom()
}

func (p *parser) parseAtom() (Node, error) {
	t := p.cur()
	switch t.kind {
	case tokNumber:
		p.advance()
		if err := p.rejectImplicitMult(); err != nil {
			return nil, err
		}
		return &NumberNode{Literal: t.text, pos: t.pos}, nil
	case tokLParen:
		p.advance()
		p.ordinal++
		p.openPos = append(p.openPos, t.pos)
		p.openOrdinal = append(p.openOrdinal, p.ordinal)
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.cur().kind != tokRParen {
			idx := len(p.openPos) - 1
			return nil, &PosError{
				Err:     ErrUnmatchedParen,
				Pos:     p.openPos[idx],
				Ordinal: p.openOrdinal[idx],
			}
		}
		p.advance()
		p.openPos = p.openPos[:len(p.openPos)-1]
		p.openOrdinal = p.openOrdinal[:len(p.openOrdinal)-1]
		if err := p.rejectImplicitMult(); err != nil {
			return nil, err
		}
		return inner, nil
	case tokRParen:
		if len(p.openPos) == 0 {
			return nil, posError(ErrUnmatchedParen, t.pos)
		}
		return nil, posError(ErrIllegalChar, t.pos)
	default:
		return nil, posError(ErrIllegalChar, t.pos)
	}
}

// rejectImplicitMult rejects adjacency like "2(3)", "1 2" or ")(".
func (p *parser) rejectImplicitMult() error {
	switch p.cur().kind {
	case tokNumber, tokLParen:
		return posError(ErrIllegalChar, p.cur().pos)
	}
	return nil
}
