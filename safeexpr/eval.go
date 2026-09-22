package safeexpr

import "math/big"

var (
	bigZero   = big.NewRat(0, 1)
	bigMinInt = big.NewInt(minInt64)
	bigMaxInt = big.NewInt(maxInt64)
)

const (
	minInt64 = -1 << 63
	maxInt64 = 1<<63 - 1
)

// Eval 解析并求值。
//
// 返回类型规则：
//   - 精确的整数结果（含可整除的除法）：int64；超出 int64 范围报 ErrOverflow。
//   - 非整数结果：float64。
//   - 除数为 0：ErrDivisionByZero。
//
// 所有中间运算均在 big.Rat 上进行，绝不提前转 float64。十进制字面量若
// 本身无法被 float64 精确表示（如 0.1），立即报 ErrInexact。
func Eval(src string) (any, error) {
	node, err := Parse(src)
	if err != nil {
		return nil, err
	}
	v, err := evalRat(node)
	if err != nil {
		return nil, err
	}
	return finalize(v)
}

// evalRat 以 big.Rat 精确求值整棵树；错误自身已携带字节位置。
func evalRat(node Node) (*big.Rat, error) {
	switch n := node.(type) {
	case *Number:
		if !n.Value.IsInt() {
			if _, exact := n.Value.Float64(); !exact {
				return nil, newError(ErrInexact, "十进制字面量 "+n.Raw+" 无法被 float64 精确表示", n.PosVal)
			}
		}
		return new(big.Rat).Set(n.Value), nil
	case *Unary:
		v, err := evalRat(n.Child)
		if err != nil {
			return nil, err
		}
		return v.Neg(v), nil
	case *Binary:
		l, err := evalRat(n.Left)
		if err != nil {
			return nil, err
		}
		r, err := evalRat(n.Right)
		if err != nil {
			return nil, err
		}
		v := new(big.Rat)
		switch n.Op {
		case tokPlus:
			v.Add(l, r)
		case tokMinus:
			v.Sub(l, r)
		case tokStar:
			v.Mul(l, r)
		case tokSlash:
			if r.Cmp(bigZero) == 0 {
				return nil, newError(ErrDivisionByZero, "除数为 0", n.OpPos)
			}
			v.Quo(l, r)
		default:
			return nil, newError(ErrSyntax, "未知的运算符", n.OpPos)
		}
		return v, nil
	default:
		return nil, newError(ErrSyntax, "未知的语法节点", -1)
	}
}

// finalize 把精确有理数转换为最终的 int64 或 float64。
func finalize(v *big.Rat) (any, error) {
	if v.IsInt() {
		num := v.Num()
		if num.Cmp(bigMinInt) < 0 || num.Cmp(bigMaxInt) > 0 {
			return nil, newError(ErrOverflow, "整数结果超出 int64 范围", -1)
		}
		return num.Int64(), nil
	}
	f, _ := v.Float64()
	return f, nil
}
