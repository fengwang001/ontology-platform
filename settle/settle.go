// Package settle 在 instr 指令簿之上执行逐日批处理与部分交付。
package settle

import (
	"math/big"

	"ontology/fail"
	"ontology/instr"
)

// Step 是批处理中一条指令被触碰后的判定依据（用于日志与对账）。
type Step struct {
	ID              string
	SD              int
	BeforeDelivered int64 // d（本次交付前）
	BeforePaid      int64 // paid
	Remain          int64 // r=q-d
	SellerAvail     int64 // a：卖方可交量
	BuyerAfford     int64 // b：买方可付的最大 U 倍数（股数）
	Deliver         int64 // k=min(a,b)
	Pay             int64 // 本次付款
	AfterRemain     int64 // r'=r-k
	SellerLiable    bool
	BuyerLiable     bool
	SellerFine      int64
	BuyerFine       int64
	BuyIn           bool  // 是否强制买入
	Cancelled       bool  // 是否仅买方有责而取消
	Comp            int64 // 买入赔付
	FinalStatus     instr.Status
}

// Result 是一次 RunSettle 的完整结果（判定依据）。
type Result struct {
	Day   int
	Steps []Step
}

// Engine 嵌入指令簿，新增批处理能力。
type Engine struct {
	*instr.Book
}

// New 用已有指令簿构造批处理引擎。
func New(b *instr.Book) *Engine { return &Engine{Book: b} }

// cumPay 返回累计口径下买方在已收 d 股后再多收 k 股应支付的增量款：
// floor(amount*(d+k)/q) - paid。用 big.Int 防 amount*q 级溢出。
func cumPay(amount, q, d, paid, k int64) int64 {
	t := new(big.Int).Mul(big.NewInt(amount), big.NewInt(d+k))
	t.Quo(t, big.NewInt(q))
	return t.Int64() - paid
}

// buyerAfford 求不超过 remain 的最大 U 倍数 k，使本次增量付款<=现金；k 可为 0。
// 令 n=k/U，则 n∈[0, floor(remain/U)]，付款对 k 单调不减，二分上界。
func buyerAfford(amount, q, d, paid, cash, u, remain int64) int64 {
	hi := remain / u
	lo, hi2 := int64(0), hi
	for lo < hi2 {
		mid := lo + (hi2-lo+1)/2
		if cumPay(amount, q, d, paid, mid*u) <= cash {
			lo = mid
		} else {
			hi2 = mid - 1
		}
	}
	return lo * u
}

// RunSettle 执行 day 日批处理。
// 全部 sd<=day 的未了结指令按 (sd,seq) 升序只处理一遍；当批到货可被后续
// 指令使用，但已处理指令当日不回头。之后按同一次序处理逾期买入/取消。
func (e *Engine) RunSettle(day int) (*Result, error) {
	e.Lock()
	defer e.Unlock()
	if err := e.BeginRun(day); err != nil {
		return nil, err
	}
	u, rs, rb, age := e.Params()
	due := e.OpenDue(day)
	e.SetTouched(len(due))

	res := &Result{Day: day, Steps: make([]Step, 0, len(due))}

	// 第一遍：逐条部分交付 + 归责 + 当日罚金。
	for _, ins := range due {
		d, paid := ins.Delivered, ins.Paid
		r := ins.Qty - d

		// 卖方可交量：min(r, floor(持券/U)*U)
		held := e.HoldingsLocked(ins.Seller, ins.Sym)
		a := r
		if v := held / u * u; v < a {
			a = v
		}
		// 买方可付的最大 U 倍数
		cash := e.CashLocked(ins.Buyer)
		b := buyerAfford(ins.Amount, ins.Qty, d, paid, cash, u, r)

		k := a
		if b < k {
			k = b
		}
		pay := cumPay(ins.Amount, ins.Qty, d, paid, k)

		after := e.ApplyDelivery(ins.ID, k, pay)
		r2 := after.Qty - after.Delivered

		st := Step{
			ID: ins.ID, SD: ins.SD, BeforeDelivered: d, BeforePaid: paid, Remain: r,
			SellerAvail: a, BuyerAfford: b, Deliver: k, Pay: pay,
			AfterRemain: r2, FinalStatus: after.Status,
		}

		if r2 > 0 {
			liab := fail.Attribute(a, b, r) // 归责取交付前 a,b,r
			price := e.PriceLocked(ins.Sym)
			st.SellerLiable = liab.Seller
			st.BuyerLiable = liab.Buyer
			if liab.Seller {
				st.SellerFine = fail.Fine(r2, price, rs)
			}
			if liab.Buyer {
				st.BuyerFine = fail.Fine(r2, price, rb)
			}
			e.AddFines(ins.ID, st.SellerFine, st.BuyerFine)
		}
		res.Steps = append(res.Steps, st)
	}

	// 第二遍：当日全部处理完后，对 day-sd>=A 的未了结指令按同一次序处置。
	for i := range res.Steps {
		st := &res.Steps[i]
		if st.AfterRemain <= 0 || day-st.SD < age {
			continue
		}
		ins, _ := e.GetLocked(st.ID) // 已更新视图（持锁）
		if st.SellerLiable {
			// 强制买入：剩余取消，卖方赔付买方，罚金当日已记。
			comp := fail.BuyInComp(st.AfterRemain, e.PriceLocked(ins.Sym),
				ins.Amount, ins.Paid)
			e.BuyIn(st.ID, comp)
			st.BuyIn = true
			st.Comp = comp
			st.FinalStatus = instr.BoughtIn
		} else {
			// 仅买方有责：取消，无赔付；罚金照记。
			e.CancelOpen(st.ID)
			st.Cancelled = true
			st.FinalStatus = instr.Cancelled
		}
	}

	return res, nil
}
