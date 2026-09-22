package safeexpr

import (
	"math/big"
	"strings"
)

// Parse 仅解析表达式，返回唯一的抽象语法树，不做任何求值。
// 空表达式、非法字符、非法字面量、括号不匹配等均返回分类错误。
func Parse(src string) (Node, error) {
	if strings.TrimSpace(src) == "" {
		return nil, newError(ErrEmpty, "表达式为空", -1)
	}

	p := &parser{lx: lexer{src: src}}
	if err := p.advance(); err != nil {
		return nil, err
	}
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.cur.kind != tokEOF {
		if p.cur.kind == tokRParen {
			return nil, p.unmatchedClose()
		}
		if p.cur.kind == tokLParen || p.cur.kind == tokNumber {
			return nil, newError(ErrIllegalChar, "意外的 "+kindString(p.cur.kind)+"，不允许隐式乘法", p.cur.pos)
		}
		return nil, newError(ErrSyntax, "意外的 "+kindString(p.cur.kind), p.cur.pos)
	}
	if len(p.opens) > 0 {
		return nil, p.unclosedOpen()
	}
	return node, nil
}

type openParen struct {
	pos   int
	index int
}

type parser struct {
	lx  lexer
	cur token

	// opens 记录尚未闭合的开括号；openCount 为开括号全局序号。
	opens     []openParen
	openCount int
}

func (p *parser) advance() *Error {
	t, err := p.lx.next()
	if err != nil {
		return err
	}
	p.cur = t
	return nil
}

// parseExpr 处理 + -，同级严格左结合。
func (p *parser) parseExpr() (Node, *Error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.cur.kind == tokPlus || p.cur.kind == tokMinus {
		op := p.cur
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: op.kind, OpPos: op.pos, Left: left, Right: right}
	}
	return left, nil
}

// parseTerm 处理 * /，同级严格左结合。
func (p *parser) parseTerm() (Node, *Error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.cur.kind == tokStar || p.cur.kind == tokSlash {
		op := p.cur
		if err := p.advance(); err != nil {
			return nil, err
		}
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = &Binary{Op: op.kind, OpPos: op.pos, Left: left, Right: right}
	}
	return left, nil
}

// parseFactor 处理一元负号，其优先级高于乘除，可连续出现。
func (p *parser) parseFactor() (Node, *Error) {
	if p.cur.kind == tokMinus {
		op := p.cur
		if err := p.advance(); err != nil {
			return nil, err
		}
		child, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return &Unary{OpPos: op.pos, Child: child}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Node, *Error) {
	switch p.cur.kind {
	case tokNumber:
		val, ok := new(big.Rat).SetString(p.cur.text)
		if !ok {
			return nil, newError(ErrMalformedNumber, "无法解析数字字面量", p.cur.pos)
		}
		node := &Number{PosVal: p.cur.pos, Value: val, Raw: p.cur.text}
		if err := p.advance(); err != nil {
			return nil, err
		}
		return node, nil
	case tokLParen:
		p.openCount++
		p.opens = append(p.opens, openParen{pos: p.cur.pos, index: p.openCount})
		if err := p.advance(); err != nil {
			return nil, err
		}
		node, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.cur.kind != tokRParen {
			return nil, p.unclosedOpen()
		}
		p.opens = p.opens[:len(p.opens)-1]
		if err := p.advance(); err != nil {
			return nil, err
		}
		return node, nil
	case tokEOF:
		return nil, newError(ErrSyntax, "表达式在此结束，缺少操作数", p.cur.pos)
	case tokRParen:
		return nil, newError(ErrSyntax, "意外的 ')'，缺少操作数", p.cur.pos)
	default:
		return nil, newError(ErrSyntax, "意外的 "+kindString(p.cur.kind)+"，缺少操作数", p.cur.pos)
	}
}

// unclosedOpen 报告最近一个未闭合的开括号序号与位置。
func (p *parser) unclosedOpen() *Error {
	last := p.opens[len(p.opens)-1]
	e := newError(ErrUnmatchedParen, "开括号未闭合", last.pos)
	e.OpenIndex = last.index
	return e
}

// unmatchedClose 报告多余的闭括号。
func (p *parser) unmatchedClose() *Error {
	return newError(ErrUnmatchedParen, "闭括号没有对应的开括号", p.cur.pos)
}
