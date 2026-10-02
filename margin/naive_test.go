package margin

// 本文件是按规格逐条写成的朴素模拟，用于与引擎实现做差分对照。
// 它刻意写得直接（线性扫描、sort.Slice、大整数交叉相乘），
// 与引擎共用错误哨兵与记录类型以便比较。

import (
	"math/big"
	"sort"
)

type naiveAccount struct {
	m, q, c int64
}

type naive struct {
	i, mm, f  int64
	accts     map[string]*naiveAccount
	z, b      int64
	markPrice int64
	hasMark   bool
}

func newNaive(i, mm, f int64) *naive {
	return &naive{i: i, mm: mm, f: f, accts: make(map[string]*naiveAccount)}
}

func nAbs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func nCeil(a, b int64) int64 { return (a + b - 1) / b }

func (n *naive) deposit(a string, x int64) error {
	if a == "" || x < 1 || x > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	acct := n.accts[a]
	if acct == nil {
		acct = &naiveAccount{}
		n.accts[a] = acct
	}
	if acct.m+x > 1_000_000_000_000_000 {
		return ErrInvalidParam
	}
	acct.m += x
	return nil
}

func (n *naive) open(a string, dir Side, qty, p int64) error {
	if a == "" || (dir != Long && dir != Short) ||
		qty < 1 || qty > 1_000_000 || p < 1 || p > 1_000_000 {
		return ErrInvalidParam
	}
	acct := n.accts[a]
	if acct == nil {
		return ErrAccountNotFound
	}
	if acct.q != 0 && (acct.q > 0) != (dir == Long) {
		return ErrPositionConflict
	}
	signed := qty
	if dir == Short {
		signed = -qty
	}
	if nAbs(acct.q+signed) > 1_000_000 {
		return ErrInvalidParam
	}
	newC := acct.c + signed*p
	if acct.m < nCeil(nAbs(newC)*n.i, 10_000) {
		return ErrInsufficientFunds
	}
	acct.q += signed
	acct.c = newC
	return nil
}

func (n *naive) close(a string, p int64) error {
	if a == "" || p < 1 || p > 1_000_000 {
		return ErrInvalidParam
	}
	acct := n.accts[a]
	if acct == nil {
		return ErrAccountNotFound
	}
	if acct.q == 0 {
		return ErrPositionConflict
	}
	equity := acct.m + acct.q*p - acct.c
	if equity >= 0 {
		acct.m = equity
	} else {
		acct.m = 0
		d := -equity
		u := min(n.z, d)
		n.z -= u
		n.b += d - u
	}
	acct.q = 0
	acct.c = 0
	return nil
}

func (n *naive) withdraw(a string, x int64) error {
	if a == "" || x < 1 || x > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	acct := n.accts[a]
	if acct == nil {
		return ErrAccountNotFound
	}
	if acct.q != 0 {
		return ErrPositionConflict
	}
	if x > acct.m {
		return ErrInsufficientFunds
	}
	acct.m -= x
	return nil
}

func (n *naive) mark(p int64) ([]Liquidation, error) {
	if p < 1 || p > 1_000_000 {
		return nil, ErrInvalidParam
	}
	// 第一步：一次性定出强平集合。
	lambda := map[string]bool{}
	for id, a := range n.accts {
		if a.q == 0 {
			continue
		}
		equity := a.m + a.q*p - a.c
		req := nCeil(nAbs(a.q)*p*n.mm, 10_000)
		if equity < req {
			lambda[id] = true
		}
	}
	// 第二步：按编号字节序升序逐个执行。
	order := make([]string, 0, len(lambda))
	for id := range lambda {
		order = append(order, id)
	}
	sort.Strings(order)
	var recs []Liquidation
	for _, id := range order {
		recs = append(recs, n.liquidate(id, p, lambda))
	}
	// 第三步：记录标记价格。
	n.markPrice, n.hasMark = p, true
	return recs, nil
}

func (n *naive) liquidate(id string, p int64, lambda map[string]bool) Liquidation {
	acct := n.accts[id]
	rec := Liquidation{Account: id, Equity: acct.m + acct.q*p - acct.c}
	if rec.Equity >= 0 {
		rec.Fee = min(rec.Equity, nAbs(acct.q)*p*n.f/10_000)
		acct.m = rec.Equity - rec.Fee
		n.z += rec.Fee
	} else {
		acct.m = 0
		d := -rec.Equity
		rec.FundUsed = min(n.z, d)
		n.z -= rec.FundUsed
		if s := d - rec.FundUsed; s > 0 {
			rec.ADL, rec.BadDebt = n.adl(id, acct.q, p, s, lambda)
			n.b += rec.BadDebt
		}
	}
	acct.q = 0
	acct.c = 0
	return rec
}

func (n *naive) adl(liquidated string, lq, p, s int64, lambda map[string]bool) ([]ADLEntry, int64) {
	type cand struct {
		id   string
		pnl  int64
		absC int64
	}
	var cands []cand
	for id, a := range n.accts {
		if id == liquidated || lambda[id] || a.q == 0 {
			continue
		}
		if (a.q > 0) == (lq > 0) {
			continue
		}
		if pnl := a.q*p - a.c; pnl > 0 {
			cands = append(cands, cand{id: id, pnl: pnl, absC: nAbs(a.c)})
		}
	}
	// 按盈利率从大到小，大整数交叉相乘比较；相等取编号小者。
	sort.Slice(cands, func(x, y int) bool {
		lhs := new(big.Int).Mul(big.NewInt(cands[x].pnl), big.NewInt(cands[y].absC))
		rhs := new(big.Int).Mul(big.NewInt(cands[y].pnl), big.NewInt(cands[x].absC))
		if c := lhs.Cmp(rhs); c != 0 {
			return c > 0
		}
		return cands[x].id < cands[y].id
	})
	var entries []ADLEntry
	for _, cd := range cands {
		if s == 0 {
			break
		}
		acct := n.accts[cd.id]
		take := min(s, acct.q*p-acct.c)
		if take <= 0 {
			continue
		}
		acct.c += take
		entries = append(entries, ADLEntry{Account: cd.id, Take: take})
		s -= take
	}
	return entries, s
}
