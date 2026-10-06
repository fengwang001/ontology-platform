// Package settle 实现券款对付的逐日批处理：对每个交收日，把 sd<=day 的
// 未了结指令按 (sd, 序号) 升序只过一遍，计算交付量与付款并即时过户，
// 随后交由 fail 包做归责、罚金与逾期强制买入。
package settle

import (
	"math/big"

	"ontology/fail"
	"ontology/instr"
)

// Run 执行 day 当日的交收批处理。每个 day 至多成功一次。
func Run(b *instr.Book, day int) error {
	b.Lock()
	defer b.Unlock()
	if err := b.BeginSettle(day); err != nil {
		return err
	}
	due := b.Due(day)
	unit := b.Unit()
	for _, ins := range due {
		b.NoteTouched()
		ins.SellerFault, ins.BuyerFault = false, false
		r := ins.Qty - ins.Delivered
		a := min64(r, b.Holding(ins.Seller, ins.Sym)/unit*unit)
		bq := maxAffordable(ins.Amount, ins.Delivered, ins.Qty,
			ins.Paid, b.Cash(ins.Buyer), r, unit)
		k := min64(a, bq)
		if k > 0 {
			pay := floorPay(ins.Amount, ins.Delivered+k, ins.Qty) - ins.Paid
			b.AddHolding(ins.Seller, ins.Sym, -k)
			b.AddHolding(ins.Buyer, ins.Sym, k)
			b.AddCash(ins.Buyer, -pay)
			b.AddCash(ins.Seller, pay)
			ins.Delivered += k
			ins.Paid += pay
		}
		if k == r {
			ins.Status = instr.Settled
			continue
		}
		fail.Apply(b, ins, a, bq, r)
	}
	fail.ProcessOverdue(b, due, day)
	return nil
}

func min64(x, y int64) int64 {
	if x < y {
		return x
	}
	return y
}

// floorPay 返回 floor(amount*d/q)，即累计交付 d 时累计应付的款项。
func floorPay(amount, d, q int64) int64 {
	v := new(big.Int).Mul(big.NewInt(amount), big.NewInt(d))
	v.Div(v, big.NewInt(q))
	return v.Int64()
}

// maxAffordable 返回不超过 r 的最大的 U 的倍数 k，使
// floor(amount*(d+k)/q)-paid <= cash（k 可为 0）。
// floor 关于 k 单调，解析求解 kmax = floor(((paid+cash+1)*q-1)/amount) - d。
func maxAffordable(amount, d, q, paid, cash, r, unit int64) int64 {
	lim := new(big.Int).Add(big.NewInt(paid), big.NewInt(cash))
	lim.Add(lim, big.NewInt(1))
	lim.Mul(lim, big.NewInt(q))
	lim.Sub(lim, big.NewInt(1))
	lim.Div(lim, big.NewInt(amount))
	lim.Sub(lim, big.NewInt(d))
	kmax := r
	if lim.Cmp(big.NewInt(r)) < 0 {
		if !lim.IsInt64() || lim.Sign() < 0 {
			return 0
		}
		kmax = lim.Int64()
	}
	return kmax / unit * unit
}
