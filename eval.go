package ontology

import "math/big"

// Eval 解析并求值表达式。
//
// 返回类型规则：
//   - 结果为整数且落在 int64 范围内：返回 int64；
//   - 结果为整数但超出 int64：返回 ErrOverflow；
//   - 结果为非整数且可被 float64 精确表示：返回 float64；
//   - 结果为非整数但 float64 无法精确表示（如 0.1、1/3）：返回 ErrInexact。
//
// 所有中间运算都以 big.Rat 精确进行，绝不先转 float64。
func Eval(s string) (any, error) {
	n, err := Parse(s)
	if err != nil {
		return nil, err
	}
	r, err := n.eval()
	if err != nil {
		return nil, err
	}
	return ratValue(r)
}

// ratValue 把精确有理数结果转换为对外返回类型。
func ratValue(r *big.Rat) (any, error) {
	if r.IsInt() {
		n := r.Num()
		if n.IsInt64() {
			return n.Int64(), nil
		}
		return nil, errPlain(ErrOverflow, "整数结果 %s 超出 int64 范围", n.String())
	}
	f, exact := r.Float64()
	if !exact {
		return nil, errPlain(ErrInexact,
			"结果 %s 无法被 float64 精确表示", r.RatString())
	}
	return f, nil
}
