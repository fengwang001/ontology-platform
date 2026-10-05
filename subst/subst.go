// Package subst 提供现金替代的金额计算：申购预收、赎回偿付、
// 替代比例上限判定与日终退补差额。中间量用 big.Int 防止溢出。
package subst

import "math/big"

// Basis 为基点分母。
const Basis = 10000

// Precollect 申购预收：ceil(d*p*(Basis+prem)/Basis)。
func Precollect(d, p, prem int64) int64 {
	num := new(big.Int).Mul(big.NewInt(d), big.NewInt(p))
	num.Mul(num, big.NewInt(Basis+prem))
	q, r := new(big.Int).QuoRem(num, big.NewInt(Basis), new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.Int64()
}

// RedeemPay 赎回偿付：floor(d*p*(Basis-prem)/Basis)，与预收取整方向相反。
func RedeemPay(d, p, prem int64) int64 {
	num := new(big.Int).Mul(big.NewInt(d), big.NewInt(p))
	num.Mul(num, big.NewInt(Basis-prem))
	return num.Div(num, big.NewInt(Basis)).Int64()
}

// RatioOK 判定替代比例：X*100 <= rmax*Y（取等通过）。
func RatioOK(x, y *big.Int, rmax int64) bool {
	lhs := new(big.Int).Mul(x, big.NewInt(100))
	rhs := new(big.Int).Mul(y, big.NewInt(rmax))
	return lhs.Cmp(rhs) <= 0
}

// Settle 日终退补额：预收减实际成本，正为退给账户、负为向账户补收。
func Settle(precollected, cost int64) int64 {
	return precollected - cost
}
