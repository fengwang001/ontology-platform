package settle

import (
	"math/big"

	"ontology/fail"
	"ontology/instr"
)

// naive 是独立按题面规则逐步书写的参考实现（不调用 settle 的生产函数）。
type naive struct {
	u, rs, rb int64
	age       int
	maxDay    int64
	lastRun   int64
	hold      map[string]map[string]int64
	cash      map[string]int64
	price     map[string]int64
	payable   map[string]int64
	receiv    map[string]int64
	orders    map[string]*norder
	seq       int
}

type norder struct {
	id                 string
	seq                int
	seller, buyer, sym string
	qty, amount        int64
	sd                 int
	d, paid            int64
	status             instr.Status
	sFine, bFine, comp int64
}

func newNaive(u, rs, rb int64, age int) *naive {
	return &naive{
		u: u, rs: rs, rb: rb, age: age, lastRun: -1,
		hold: map[string]map[string]int64{}, cash: map[string]int64{},
		price: map[string]int64{}, payable: map[string]int64{},
		receiv: map[string]int64{}, orders: map[string]*norder{},
	}
}

func floorCum(amount, q, d, paid, k int64) int64 {
	t := new(big.Int).Mul(big.NewInt(amount), big.NewInt(d+k))
	t.Quo(t, big.NewInt(q))
	return t.Int64() - paid
}

func (n *naive) checkDay(day int) error {
	if day < 0 || day > 1_000_000 {
		return instr.ErrInvalid
	}
	if int64(day) < n.maxDay {
		return instr.ErrRollback
	}
	return nil
}

func (n *naive) bump(day int) {
	if int64(day) > n.maxDay {
		n.maxDay = int64(day)
	}
}

func (n *naive) credit(day int, acct, sym string, qty int64) error {
	if day < 0 || day > 1e6 || len(acct) == 0 || len(sym) == 0 ||
		qty < 1 || qty > 1e12 {
		return instr.ErrInvalid
	}
	if err := n.checkDay(day); err != nil {
		return err
	}
	if n.hold[acct] == nil {
		n.hold[acct] = map[string]int64{}
	}
	n.hold[acct][sym] += qty
	n.bump(day)
	return nil
}

func (n *naive) creditCash(day int, acct string, amt int64) error {
	if day < 0 || day > 1e6 || len(acct) == 0 || amt < 1 || amt > 1e12 {
		return instr.ErrInvalid
	}
	if err := n.checkDay(day); err != nil {
		return err
	}
	n.cash[acct] += amt
	n.bump(day)
	return nil
}

func (n *naive) setPrice(day int, sym string, p int64) error {
	if day < 0 || day > 1e6 || len(sym) == 0 || p < 1 || p > 1e6 {
		return instr.ErrInvalid
	}
	if err := n.checkDay(day); err != nil {
		return err
	}
	n.price[sym] = p
	n.bump(day)
	return nil
}

func (n *naive) instruct(day int, id, s, b, sym string, qty, amount int64, sd int) error {
	if day < 0 || day > 1e6 || len(id) == 0 || len(s) == 0 || len(b) == 0 ||
		len(sym) == 0 || s == b || qty <= 0 || qty > 1e9 || qty%n.u != 0 ||
		amount < 1 || amount > 1e15 || sd <= day || sd < 0 || sd > 1e6 {
		return instr.ErrInvalid
	}
	if err := n.checkDay(day); err != nil {
		return err
	}
	if _, ok := n.orders[id]; ok {
		return instr.ErrDuplicate
	}
	if _, ok := n.price[sym]; !ok {
		return instr.ErrNoPrice
	}
	n.seq++
	n.orders[id] = &norder{
		id: id, seq: n.seq, seller: s, buyer: b, sym: sym,
		qty: qty, amount: amount, sd: sd, status: instr.Open,
	}
	n.bump(day)
	return nil
}

func (n *naive) cancel(day int, id string) error {
	if day < 0 || day > 1e6 || len(id) == 0 {
		return instr.ErrInvalid
	}
	if err := n.checkDay(day); err != nil {
		return err
	}
	o, ok := n.orders[id]
	if !ok {
		return instr.ErrNotFound
	}
	if o.status != instr.Open || day >= o.sd {
		return instr.ErrState
	}
	o.status = instr.Cancelled
	n.bump(day)
	return nil
}

type nstep struct {
	id                                         string
	d, paid, r, a, b, k, pay, r2, sf, bf, comp int64
	sd                                         int
	sellerL, buyerL, buyIn, cancelled          bool
	status                                     instr.Status
}

func (n *naive) runSettle(day int) ([]nstep, error) {
	if day < 0 || day > 1e6 {
		return nil, instr.ErrInvalid
	}
	if int64(day) < n.maxDay {
		return nil, instr.ErrRollback
	}
	if int64(day) == n.lastRun {
		return nil, instr.ErrState
	}
	n.lastRun = int64(day)
	n.bump(day)
	var due []*norder
	for _, o := range n.orders {
		if o.status == instr.Open && o.sd <= day {
			due = append(due, o)
		}
	}
	for i := 1; i < len(due); i++ {
		for j := i; j > 0; j-- {
			x, y := due[j-1], due[j]
			if x.sd > y.sd || (x.sd == y.sd && x.seq > y.seq) {
				due[j-1], due[j] = y, x
			} else {
				break
			}
		}
	}
	steps := make([]nstep, 0, len(due))
	for _, o := range due {
		r := o.qty - o.d
		a := r
		if h := n.hold[o.seller][o.sym] / n.u * n.u; h < a {
			a = h
		}
		var bv int64 // 朴素逐倍数试探求最大可付倍数
		for k := int64(0); k <= r/n.u; k++ {
			if floorCum(o.amount, o.qty, o.d, o.paid, k*n.u) <= n.cash[o.buyer] {
				bv = k * n.u
			}
		}
		k := a
		if bv < k {
			k = bv
		}
		pay := floorCum(o.amount, o.qty, o.d, o.paid, k)
		o.d += k
		o.paid += pay
		if n.hold[o.seller] == nil {
			n.hold[o.seller] = map[string]int64{}
		}
		n.hold[o.seller][o.sym] -= k
		if n.hold[o.buyer] == nil {
			n.hold[o.buyer] = map[string]int64{}
		}
		n.hold[o.buyer][o.sym] += k
		n.cash[o.buyer] -= pay
		n.cash[o.seller] += pay
		r2 := o.qty - o.d
		if r2 == 0 {
			o.status = instr.Settled
		}
		st := nstep{id: o.id, sd: o.sd, d: o.d - k, paid: o.paid - pay,
			r: r, a: a, b: bv, k: k, pay: pay, r2: r2, status: o.status}
		if r2 > 0 {
			liab := fail.Attribute(a, bv, r)
			st.sellerL, st.buyerL = liab.Seller, liab.Buyer
			p := n.price[o.sym]
			if liab.Seller {
				st.sf = fail.Fine(r2, p, n.rs)
				o.sFine += st.sf
				n.payable[o.seller] += st.sf
			}
			if liab.Buyer {
				st.bf = fail.Fine(r2, p, n.rb)
				o.bFine += st.bf
				n.payable[o.buyer] += st.bf
			}
		}
		steps = append(steps, st)
	}
	for i := range steps {
		st := &steps[i]
		if st.r2 <= 0 || day-st.sd < n.age {
			continue
		}
		o := n.orders[st.id]
		if st.sellerL {
			st.comp = fail.BuyInComp(st.r2, n.price[o.sym], o.amount, o.paid)
			o.comp += st.comp
			n.payable[o.seller] += st.comp
			n.receiv[o.buyer] += st.comp
			o.status = instr.BoughtIn
			st.buyIn, st.status = true, instr.BoughtIn
		} else {
			o.status = instr.Cancelled
			st.cancelled, st.status = true, instr.Cancelled
		}
	}
	return steps, nil
}
