package freeze

import (
	"fmt"
)

// naiveModel is an independent, straight-from-spec transcription used only by
// the randomized differential test.
type naiveModel struct {
	m        int64
	accounts map[string]*naiveAccount
}

type naiveAccount struct {
	balance int64
	queue   []naiveOrder
}

type naiveOrder struct {
	id     string
	amount int64
	exp    int64
}

type naiveSnap struct {
	balance   int64
	available int64
	orders    []naiveEffOrder
}

type naiveEffOrder struct {
	id     string
	amount int64
	eff    int64
	exp    int64
}

func newNaive() *naiveModel {
	return &naiveModel{accounts: map[string]*naiveAccount{}}
}

func (nm *naiveModel) sweep(acc *naiveAccount, t int64) {
	kept := acc.queue[:0]
	for _, o := range acc.queue {
		if o.exp != 0 && o.exp <= t {
			continue
		}
		kept = append(kept, o)
	}
	acc.queue = kept
}

func (nm *naiveModel) eff(acc *naiveAccount) []naiveEffOrder {
	out := make([]naiveEffOrder, len(acc.queue))
	used := int64(0)
	for i, o := range acc.queue {
		e := o.amount
		if r := acc.balance - used; e > r {
			e = r
		}
		if e < 0 {
			e = 0
		}
		used += e
		out[i] = naiveEffOrder{id: o.id, amount: o.amount, eff: e, exp: o.exp}
	}
	return out
}

func effTotal(orders []naiveEffOrder) int64 {
	tot := int64(0)
	for _, o := range orders {
		tot += o.eff
	}
	return tot
}

// call dispatches one operation and returns (ok, reason, snapshot-or-nil).
// Validation order follows the spec literally.
func (nm *naiveModel) call(op, acct string, t int64, id string, x, exp int64) (bool, string, *naiveSnap) {
	hasID := op == "freeze" || op == "unfreeze" || op == "seize"
	needX := op != "query"
	paramBad := acct == "" || t < 0 || t > MaxTime ||
		(needX && (x < 1 || x > MaxAmount)) ||
		(hasID && id == "") ||
		(op == "freeze" && exp != 0 && (exp <= t || exp > MaxTime))
	if paramBad {
		return false, string(ReasonInvalidParam), nil
	}
	if t < nm.m {
		return false, string(ReasonTimeRegression), nil
	}
	acc := nm.accounts[acct]
	if acc == nil && op != "deposit" {
		return false, string(ReasonAccountMissing), nil
	}

	switch op {
	case "deposit":
		cur := int64(0)
		if acc != nil {
			cur = acc.balance
		}
		if cur+x > MaxBalance {
			return false, string(ReasonInvalidParam), nil
		}
		if acc == nil {
			acc = &naiveAccount{}
			nm.accounts[acct] = acc
		}
		acc.balance += x
		nm.m = t
		return true, "", nil
	case "debit":
		nm.sweep(acc, t)
		orders := nm.eff(acc)
		v := acc.balance - effTotal(orders)
		if x > v {
			return false, string(ReasonAvailableShort), nil
		}
		acc.balance -= x
		nm.m = t
		return true, "", nil
	case "freeze":
		nm.sweep(acc, t)
		for _, o := range acc.queue {
			if o.id == id {
				return false, string(ReasonIDDuplicate), nil
			}
		}
		acc.queue = append(acc.queue, naiveOrder{id: id, amount: x, exp: exp})
		nm.m = t
		return true, "", nil
	case "unfreeze":
		nm.sweep(acc, t)
		idx := -1
		for i := range acc.queue {
			if acc.queue[i].id == id {
				idx = i
			}
		}
		if idx < 0 {
			return false, string(ReasonOrderMissing), nil
		}
		if x > acc.queue[idx].amount {
			return false, string(ReasonUnfreezeOver), nil
		}
		acc.queue[idx].amount -= x
		if acc.queue[idx].amount == 0 {
			acc.queue = append(acc.queue[:idx], acc.queue[idx+1:]...)
		}
		nm.m = t
		return true, "", nil
	case "seize":
		nm.sweep(acc, t)
		idx := -1
		for i := range acc.queue {
			if acc.queue[i].id == id {
				idx = i
			}
		}
		if idx < 0 {
			return false, string(ReasonOrderMissing), nil
		}
		orders := nm.eff(acc)
		if x > orders[idx].eff {
			return false, string(ReasonSeizeOver), nil
		}
		acc.balance -= x
		acc.queue[idx].amount -= x
		if acc.queue[idx].amount == 0 {
			acc.queue = append(acc.queue[:idx], acc.queue[idx+1:]...)
		}
		nm.m = t
		return true, "", nil
	case "seizeq":
		nm.sweep(acc, t)
		orders := nm.eff(acc)
		if x > effTotal(orders) {
			return false, string(ReasonSeizeQOver), nil
		}
		rem := x
		for i := range acc.queue {
			if rem == 0 {
				break
			}
			d := orders[i].eff
			if d > rem {
				d = rem
			}
			acc.queue[i].amount -= d
			rem -= d
		}
		kept := acc.queue[:0]
		for _, o := range acc.queue {
			if o.amount > 0 {
				kept = append(kept, o)
			}
		}
		acc.queue = kept
		acc.balance -= x
		nm.m = t
		return true, "", nil
	case "query":
		nm.sweep(acc, t)
		orders := nm.eff(acc)
		nm.m = t
		return true, "", &naiveSnap{
			balance:   acc.balance,
			available: acc.balance - effTotal(orders),
			orders:    orders,
		}
	}
	panic("unknown op " + op)
}

func snapsEqual(s Snapshot, ns *naiveSnap) bool {
	if s.Balance != ns.balance || s.Available != ns.available || len(s.Orders) != len(ns.orders) {
		return false
	}
	for i := range s.Orders {
		o, no := s.Orders[i], ns.orders[i]
		if string(o.ID) != no.id || o.Amount != no.amount || o.Effective != no.eff || o.Expire != no.exp {
			return false
		}
	}
	return true
}

type randOp struct {
	op   string
	acct string
	t    int64
	id   string
	x    int64
	exp  int64
}

func (r randOp) String() string {
	switch r.op {
	case "deposit", "debit", "seizeq":
		return fmt.Sprintf("%s(acct=%s t=%d x=%d)", r.op, r.acct, r.t, r.x)
	case "query":
		return fmt.Sprintf("query(acct=%s t=%d)", r.acct, r.t)
	default:
		return fmt.Sprintf("%s(acct=%s t=%d id=%s x=%d exp=%d)",
			r.op, r.acct, r.t, r.id, r.x, r.exp)
	}
}

func (r randOp) apply(m *Manager) (bool, string, Snapshot) {
	var err error
	var snap Snapshot
	switch r.op {
	case "deposit":
		err = m.Deposit(b(r.acct), r.t, r.x)
	case "debit":
		err = m.Debit(b(r.acct), r.t, r.x)
	case "freeze":
		err = m.Freeze(b(r.acct), r.t, b(r.id), r.x, r.exp)
	case "unfreeze":
		err = m.Unfreeze(b(r.acct), r.t, b(r.id), r.x)
	case "seize":
		err = m.Seize(b(r.acct), r.t, b(r.id), r.x)
	case "seizeq":
		err = m.SeizeQueue(b(r.acct), r.t, r.x)
	case "query":
		snap, err = m.Query(b(r.acct), r.t)
	}
	if err != nil {
		return false, string(err.(*Error).Reason), snap
	}
	return true, "", snap
}
