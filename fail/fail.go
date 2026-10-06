// Package fail 实现交收失败处理：归责、罚金与逾期强制买入。
// 全部函数约定调用方已持有 instr.Book 的锁（由 settle 包串行驱动）。
package fail

import (
	"math/big"

	"ontology/instr"
)

// Blame 按本次交付前的 a（卖方可交量）、b（买方可付量）、r（剩余量）
// 判定归责，双方可同时有责。
func Blame(a, b, r int64) (sellerFault, buyerFault bool) {
	return a < r, b < r
}

// Penalty 计算罚金 ceil(r*price*rate/10000)。乘积可达 1e19，用 big.Int。
func Penalty(r, price, rate int64) int64 {
	v := new(big.Int).Mul(big.NewInt(r), big.NewInt(price))
	v.Mul(v, big.NewInt(rate))
	v.Add(v, big.NewInt(9999))
	v.Div(v, big.NewInt(10000))
	return v.Int64()
}

// Apply 记录当日归责并把罚金计入各责方应付账；罚金不动用现金与持券。
// a、b、r 取本次交付前的值，罚金基数 r' 为交付后的剩余量。
func Apply(b *instr.Book, ins *instr.Instruction, a, bq, r int64) {
	sellerFault, buyerFault := Blame(a, bq, r)
	ins.SellerFault, ins.BuyerFault = sellerFault, buyerFault
	rAfter := ins.Qty - ins.Delivered
	price := b.Price(ins.Sym)
	rs, rb := b.Rates()
	if sellerFault {
		b.AddPayable(ins.Seller, Penalty(rAfter, price, rs))
	}
	if buyerFault {
		b.AddPayable(ins.Buyer, Penalty(rAfter, price, rb))
	}
}

// Compensation 计算强制买入赔付 max(0, r*price-(amount-paid))。
func Compensation(r, price, amount, paid int64) int64 {
	c := r*price - (amount - paid)
	if c < 0 {
		return 0
	}
	return c
}

// ProcessOverdue 在当日全部指令处理完后，对 day-sd>=A 的未了结指令
// 按同一次序处理：当日卖方有责则强制买入并记赔付，否则取消、无赔付。
func ProcessOverdue(b *instr.Book, due []*instr.Instruction, day int) {
	age := b.BuyInAge()
	for _, ins := range due {
		if ins.Status != instr.Open || day-ins.Sd < age {
			continue
		}
		r := ins.Qty - ins.Delivered
		if ins.SellerFault {
			comp := Compensation(r, b.Price(ins.Sym), ins.Amount, ins.Paid)
			b.AddPayable(ins.Seller, comp)
			b.AddReceivable(ins.Buyer, comp)
			ins.Status = instr.BoughtIn
		} else {
			ins.Status = instr.Cancelled
		}
	}
}
