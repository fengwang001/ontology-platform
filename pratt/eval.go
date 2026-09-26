package pratt

import (
	"math/bits"

	"ontology/lex"
)

// Eval evaluates n with int64 arithmetic; / truncates toward zero.
func Eval(n *Node) (int64, error) {
	switch n.Kind {
	case Num:
		return n.Val, nil
	case Neg:
		v, err := Eval(n.Left)
		if err != nil {
			return 0, err
		}
		return -v, nil
	case Bin:
		l, err := Eval(n.Left)
		if err != nil {
			return 0, err
		}
		r, err := Eval(n.Right)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case lex.PLUS:
			return l + r, nil
		case lex.MINUS:
			return l - r, nil
		case lex.STAR:
			return l * r, nil
		case lex.SLASH:
			if r == 0 {
				return 0, ErrDivZero
			}
			return l / r, nil
		case lex.CARET:
			return pow(l, r)
		}
	}
	return 0, ErrSyntax
}

// pow computes base^exp; 0^0 = a^0 = 1. exp must be >= 0 and fit in an int.
func pow(base, exp int64) (int64, error) {
	if exp < 0 || int64(int(exp)) != exp {
		return 0, ErrExponent
	}
	result, b := int64(1), base
	for e := exp; e > 0; e >>= 1 {
		var err error
		if e&1 == 1 {
			if result, err = mul(result, b); err != nil {
				return 0, err
			}
		}
		if e > 1 {
			if b, err = mul(b, b); err != nil {
				return 0, err
			}
		}
	}
	return result, nil
}

// mul returns a*b, or ErrOverflow if the product does not fit int64.
func mul(a, b int64) (int64, error) {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if a < 0 { // correct the unsigned 128-bit product to signed
		hi -= uint64(b)
	}
	if b < 0 {
		hi -= uint64(a)
	}
	if hi != uint64(int64(lo)>>63) {
		return 0, ErrOverflow
	}
	return int64(lo), nil
}

// SelfCheck verifies that a parseExpr call at min_bp 40 (operand of a
// prefix unary minus) stops after one lbp comparison regardless of the
// input ^-chain length m. The unexported counter is read only in here.
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		toks := make([]lex.Token, 0, 2*m)
		for i := 0; i < m; i++ {
			toks = append(toks, lex.Token{Kind: lex.NUMBER, Val: 2}, lex.Token{Kind: lex.CARET})
		}
		p := &parser{toks: toks[:len(toks)-1]}
		n, err := p.parseExpr(40)
		if err != nil || n.Kind != Num || p.cmp != 1 {
			return errSelfChk
		}
	}
	return nil
}
