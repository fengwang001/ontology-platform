// Package mtm 提供逐日盯市结算与保证金的纯函数计算。
package mtm

import (
	"math/big"

	"ontology/lot"
)

// Contract 为合约参数，费率均为万分比。
type Contract struct {
	Mult int64
	Mr   int64
	Fo   int64
	Fy   int64
	Ft   int64
}

// CeilRate 返回 ceil(amount*rate/10000)，乘积用 big.Int 防止溢出。
func CeilRate(amount, rate int64) int64 {
	if amount <= 0 || rate <= 0 {
		return 0
	}
	z := new(big.Int).Mul(big.NewInt(amount), big.NewInt(rate))
	z.Add(z, big.NewInt(9999))
	z.Div(z, big.NewInt(10000))
	return z.Int64()
}

// Margin 对单个持仓取整一次：
// ceil((Qy*Sp0 + 各今仓批次手数*开仓价之和)*mult*mr/10000)。
func Margin(p *lot.Position, c Contract) *big.Int {
	v := new(big.Int).Mul(big.NewInt(p.Qy), big.NewInt(p.Sp0))
	for _, b := range p.Today {
		v.Add(v, new(big.Int).Mul(big.NewInt(b.Qty), big.NewInt(b.Price)))
	}
	v.Mul(v, big.NewInt(c.Mult))
	v.Mul(v, big.NewInt(c.Mr))
	v.Add(v, big.NewInt(9999))
	v.Div(v, big.NewInt(10000))
	return v
}

// SettlePnL 返回该持仓以结算价 sp 计量的盯市盈亏（不修改持仓）。
func SettlePnL(p *lot.Position, dir lot.Direction, sp int64, c Contract) int64 {
	units := (sp - p.Sp0) * p.Qy
	for _, b := range p.Today {
		units += (sp - b.Price) * b.Qty
	}
	return dir.Sign() * units * c.Mult
}
