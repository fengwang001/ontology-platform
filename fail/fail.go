// Package fail 提供交收失败的纯规则：归责、罚金、逾期强制买入赔付。
package fail

import "math/big"

// Liability 归责结果。
type Liability struct {
	Seller bool
	Buyer  bool
}

// Fine 计算单笔罚金：ceil(remain×price×rate/10000)。
// 入参非负；用 big.Int 求积后对 10000 向上取整，避免中间乘积溢出。
func Fine(remain, price, rate int64) int64 {
	if remain <= 0 || rate <= 0 {
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(remain), big.NewInt(price))
	n.Mul(n, big.NewInt(rate))
	n.Add(n, big.NewInt(9999))
	n.Quo(n, big.NewInt(10000))
	return n.Int64()
}

// Attribute 依据交付前的可交量 a、可付倍数 b（股数）、剩余 r 归责。
// a<r 卖方有责；b<r 买方有责；可同时有责。
func Attribute(a, b, r int64) Liability {
	return Liability{Seller: a < r, Buyer: b < r}
}

// BuyInComp 计算强制买入赔付：max(0, remain×price-(amount-paid))。
func BuyInComp(remain, price, amount, paid int64) int64 {
	v := new(big.Int).Mul(big.NewInt(remain), big.NewInt(price))
	v.Sub(v, big.NewInt(amount-paid))
	if v.Sign() < 0 {
		return 0
	}
	return v.Int64()
}
