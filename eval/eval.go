package eval

import (
	"math"
	"math/big"
)

// Eval parses and evaluates expr. Intermediate arithmetic is always exact
// rational arithmetic (big.Rat); float64 is never used before the end.
//
// The returned value is:
//   - int64 when the exact result is an integer fitting in int64;
//   - float64 when the exact result is non-integral but exactly representable
//     as a float64.
//
// Otherwise an error classed with ErrOverflow or ErrNotRepresentable is
// returned (distinguishable via errors.Is).
func Eval(expr string) (any, Node, error) {
	tokens, err := lex(expr)
	if err != nil {
		return nil, nil, err
	}
	root, err := parse(tokens)
	if err != nil {
		return nil, nil, err
	}
	rat, err := evalNode(root)
	if err != nil {
		return nil, root, err
	}
	value, err := ratToResult(rat)
	if err != nil {
		return nil, root, err
	}
	return value, root, nil
}

func evalNode(node Node) (*big.Rat, error) {
	switch n := node.(type) {
	case *NumberNode:
		rat, ok := new(big.Rat).SetString(n.Literal)
		if !ok {
			return nil, posError(ErrBadLiteral, n.pos)
		}
		return rat, nil
	case *UnaryNode:
		operand, err := evalNode(n.Operand)
		if err != nil {
			return nil, err
		}
		return new(big.Rat).Neg(operand), nil
	case *BinaryNode:
		left, err := evalNode(n.Left)
		if err != nil {
			return nil, err
		}
		right, err := evalNode(n.Right)
		if err != nil {
			return nil, err
		}
		return evalBinary(n.Op, left, right, n.pos)
	default:
		panic("eval: unknown node type")
	}
}

func evalBinary(op byte, left, right *big.Rat, pos int) (*big.Rat, error) {
	switch op {
	case '+':
		return new(big.Rat).Add(left, right), nil
	case '-':
		return new(big.Rat).Sub(left, right), nil
	case '*':
		return new(big.Rat).Mul(left, right), nil
	case '/':
		if right.Sign() == 0 {
			return nil, &PosError{Err: ErrDivideByZero, Pos: pos}
		}
		return new(big.Rat).Quo(left, right), nil
	default:
		panic("eval: unknown operator")
	}
}

// ratToResult converts the exact rational result to int64 or, when
// non-integral, an exactly-representable float64.
func ratToResult(rat *big.Rat) (any, error) {
	if rat.IsInt() {
		num := rat.Num()
		if !num.IsInt64() {
			return nil, ErrOverflow
		}
		return num.Int64(), nil
	}
	f, exact := rat.Float64()
	if !exact || math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, ErrNotRepresentable
	}
	return f, nil
}
