// Package pratt implements precedence-climbing (Pratt) parsing over the
// token stream from package lex, plus evaluation of the resulting AST.
package pratt

import (
	"errors"

	"ontology/lex"
)

// Sentinel errors. Every rejected input fails with exactly one of these
// (or a lex sentinel), never with a plausible-looking value.
var (
	ErrSyntax   = errors.New("pratt: syntax error")
	ErrParens   = errors.New("pratt: unbalanced parentheses")
	ErrExponent = errors.New("pratt: exponent negative or not an int")
	ErrDivZero  = errors.New("pratt: division by zero")
	ErrOverflow = errors.New("pratt: power overflow")
	errSelfChk  = errors.New("pratt: self-check failed")
)

// Kind classifies an AST node.
type Kind uint8

const (
	Num Kind = iota // literal, Val
	Neg             // unary minus, operand in Left
	Bin             // Op applied to Left and Right
)

// Node is an expression AST node.
type Node struct {
	Kind        Kind
	Op          lex.Kind
	Val         int64
	Left, Right *Node
}

// infixBP is the binding-power table (lbp, rbp) for infix operators.
func infixBP(k lex.Kind) (lbp, rbp int, ok bool) {
	switch k {
	case lex.PLUS, lex.MINUS:
		return 10, 11, true
	case lex.STAR, lex.SLASH:
		return 20, 21, true
	case lex.CARET: // right-associative: rbp == lbp
		return 30, 30, true
	}
	return 0, 0, false
}

type parser struct {
	toks []lex.Token
	pos  int
	cmp  int // lbp comparisons made by the innermost parseExpr call
}

// Parse parses a whole token stream into an AST; leftover tokens are an error.
func Parse(toks []lex.Token) (*Node, error) {
	p := &parser{toks: toks}
	root, err := p.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		if p.toks[p.pos].Kind == lex.RPAREN {
			return nil, ErrParens
		}
		return nil, ErrSyntax
	}
	return root, nil
}

// parseExpr parses a prefix form, then folds infix operators while the
// next token is an infix operator with lbp >= minBP.
func (p *parser) parseExpr(minBP int) (*Node, error) {
	p.cmp = 0
	left, err := p.parsePrefix()
	if err != nil {
		return nil, err
	}
	for p.pos < len(p.toks) {
		lbp, rbp, ok := infixBP(p.toks[p.pos].Kind)
		if !ok {
			break
		}
		p.cmp++
		if lbp < minBP {
			break
		}
		op := p.toks[p.pos].Kind
		p.pos++
		right, err := p.parseExpr(rbp)
		if err != nil {
			return nil, err
		}
		left = &Node{Kind: Bin, Op: op, Left: left, Right: right}
	}
	return left, nil
}

// parsePrefix parses a NUMBER, a parenthesized subexpression, or a prefix
// unary minus (whose operand is parsed at min_bp 40).
func (p *parser) parsePrefix() (*Node, error) {
	if p.pos >= len(p.toks) {
		return nil, ErrSyntax
	}
	tok := p.toks[p.pos]
	p.pos++
	switch tok.Kind {
	case lex.NUMBER:
		return &Node{Kind: Num, Val: tok.Val}, nil
	case lex.MINUS:
		n, err := p.parseExpr(40)
		if err != nil {
			return nil, err
		}
		return &Node{Kind: Neg, Left: n}, nil
	case lex.LPAREN:
		n, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if p.pos >= len(p.toks) || p.toks[p.pos].Kind != lex.RPAREN {
			return nil, ErrParens
		}
		p.pos++
		return n, nil
	}
	return nil, ErrSyntax
}
