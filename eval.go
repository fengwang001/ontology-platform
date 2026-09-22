package ontology

import (
	"math"
	"math/big"
)

var (
	minInt64 = big.NewInt(math.MinInt64)
	maxInt64 = big.NewInt(math.MaxInt64)
)

// Eval 解析并精确求值表达式：全程使用 big.Rat 有理数运算，
// 不做任何 float64 中间近似。结果为整数时返回 int64，
// 否则返回 float64（仅最终一步转换）。
func Eval(input string) (any, error) {
	n, err := Parse(input)
	if err != nil {
		return nil, err
	}
	r, err := evalNode(n)
	if err != nil {
		return nil, err
	}
	if r.IsInt() {
		return r.Num().Int64(), nil
	}
	f, _ := r.Float64()
	return f, nil
}

func evalNode(n Node) (*big.Rat, error) {
	switch t := n.(type) {
	case *Lit:
		return new(big.Rat).Set(t.rat), nil
	case *Unary:
		v, err := evalNode(t.X)
		if err != nil {
			return nil, err
		}
		v.Neg(v)
		if err := checkFit(v, t.Pos); err != nil {
			return nil, err
		}
		return v, nil
	case *Binary:
		l, err := evalNode(t.L)
		if err != nil {
			return nil, err
		}
		r, err := evalNode(t.R)
		if err != nil {
			return nil, err
		}
		out, err := applyOp(t, l, r)
		if err != nil {
			return nil, err
		}
		if err := checkFit(out, t.Pos); err != nil {
			return nil, err
		}
		return out, nil
	}
	return nil, errAt(ErrIllegalChar, -1, "未知语法树节点")
}

func applyOp(t *Binary, l, r *big.Rat) (*big.Rat, error) {
	switch t.Op {
	case '+':
		return new(big.Rat).Add(l, r), nil
	case '-':
		return new(big.Rat).Sub(l, r), nil
	case '*':
		return new(big.Rat).Mul(l, r), nil
	case '/':
		if r.Sign() == 0 {
			return nil, errAt(ErrDivZero, t.Pos, "")
		}
		return new(big.Rat).Quo(l, r), nil
	}
	return nil, errAt(ErrIllegalChar, t.Pos, "未知运算符 %q", t.Op)
}

// checkFit 保证整数值始终落在 int64 范围内，否则报溢出。
func checkFit(r *big.Rat, pos int) error {
	if !r.IsInt() {
		return nil
	}
	i := r.Num()
	if i.Cmp(minInt64) < 0 || i.Cmp(maxInt64) > 0 {
		return errAt(ErrOverflow, pos, "整数结果超出 int64 范围")
	}
	return nil
}
