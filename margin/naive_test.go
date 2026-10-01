package margin

import (
	"fmt"
	"math/big"
	"sort"
)

// 朴素模拟器：与引擎实现完全独立，按题面规则用最直白的方式逐步执行。
// 交叉相乘比较一律用 math/big；账户、Z、B 的演化顺序与题面逐字对应。

type naiveErrKind int

const (
	nOK naiveErrKind = iota
	nInvalid
	nNotFound
	nConflict
	nNoPosition
	nInsufficient
)

type naiveErr struct{ kind naiveErrKind }

func (e naiveErr) Error() string { return fmt.Sprintf("naive error %d", e.kind) }

type naiveAccount struct {
	m, q, c int64
}

type naiveEngine struct {
	iRate, mRate, fRate int
	accounts            map[string]*naiveAccount
	z, b, lastMark      int64

	// 守恒式审计计数。
	totalFines    int64
	totalAbsorbed int64
	totalDeficit  int64
	totalADL      int64
}

func newNaive(ir, mr, fr int) (*naiveEngine, naiveErrKind) {
	if mr < 1 || ir <= mr || ir > 10000 || fr < 0 || fr > 10000 {
		return nil, nInvalid
	}
	return &naiveEngine{
		iRate: ir, mRate: mr, fRate: fr,
		accounts: map[string]*naiveAccount{},
	}, nOK
}

func i64abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func ceil10k(v int64) int64 { return (v + 9999) / 10000 }

func (n *naiveEngine) deposit(a string, x int64) naiveErrKind {
	if a == "" || x < 1 || x > 1_000_000_000_000 {
		return nInvalid
	}
	acc := n.accounts[a]
	if acc == nil {
		if x > 1_000_000_000_000_000 {
			return nInvalid
		}
		n.accounts[a] = &naiveAccount{m: x}
		return nOK
	}
	if acc.m > 1_000_000_000_000_000-x {
		return nInvalid
	}
	acc.m += x
	return nOK
}

func (n *naiveEngine) open(a string, dir int, qty, price int64) naiveErrKind {
	if a == "" || (dir != 1 && dir != -1) || qty < 1 || qty > 1_000_000 ||
		price < 1 || price > 1_000_000 {
		return nInvalid
	}
	acc := n.accounts[a]
	if acc == nil {
		return nNotFound
	}
	signed := int64(dir) * qty
	if acc.q != 0 && ((acc.q > 0) != (signed > 0)) {
		return nConflict
	}
	nq := acc.q + signed
	nc := acc.c + signed*price
	if i64abs(nq) > 1_000_000 {
		return nInvalid
	}
	req := ceil10k(i64abs(nc) * int64(n.iRate))
	if acc.m < req {
		return nInsufficient
	}
	acc.q, acc.c = nq, nc
	return nOK
}

func (n *naiveEngine) close(a string, price int64) naiveErrKind {
	if a == "" || price < 1 || price > 1_000_000 {
		return nInvalid
	}
	acc := n.accounts[a]
	if acc == nil {
		return nNotFound
	}
	if acc.q == 0 {
		return nNoPosition
	}
	e := acc.m + acc.q*price - acc.c
	if e >= 0 {
		acc.m = e
	} else {
		d := -e
		n.totalDeficit += d
		acc.m = 0
		u := d
		if u > n.z {
			u = n.z
		}
		n.z -= u
		n.totalAbsorbed += u
		n.b += d - u
	}
	acc.q, acc.c = 0, 0
	return nOK
}

func (n *naiveEngine) withdraw(a string, x int64) naiveErrKind {
	if a == "" || x < 1 || x > 1_000_000_000_000 {
		return nInvalid
	}
	acc := n.accounts[a]
	if acc == nil {
		return nNotFound
	}
	if acc.q != 0 {
		return nConflict
	}
	if x > acc.m {
		return nInsufficient
	}
	acc.m -= x
	return nOK
}

type naiveCandidate struct {
	id   string
	acc  *naiveAccount
	pnl  int64
	absC int64
}

func (n *naiveEngine) mark(price int64) ([]LiquidationItem, naiveErrKind) {
	if price < 1 || price > 1_000_000 {
		return nil, nInvalid
	}

	// 第一步：按 Mark 开始时状态确定 Λ。
	type tgt struct {
		id string
		e  int64
	}
	var lambda []tgt
	inLambda := map[string]struct{}{}
	for id, acc := range n.accounts {
		if acc.q == 0 {
			continue
		}
		e := acc.m + acc.q*price - acc.c
		r := ceil10k(i64abs(acc.q) * price * int64(n.mRate))
		if e < r {
			lambda = append(lambda, tgt{id, e})
			inLambda[id] = struct{}{}
		}
	}
	sort.Slice(lambda, func(i, j int) bool { return lambda[i].id < lambda[j].id })

	// 第二步：逐个执行，每次 ADL 都以当时的 C 重新收集与排序。
	items := []LiquidationItem{}
	for _, t := range lambda {
		acc := n.accounts[t.id]
		item := LiquidationItem{Account: t.id, E: t.e, ADL: []ADLItem{}}
		if t.e >= 0 {
			f := i64abs(acc.q) * price * int64(n.fRate) / 10000
			if f > t.e {
				f = t.e
			}
			acc.m = t.e - f
			n.z += f
			n.totalFines += f
			item.Fine = f
		} else {
			acc.m = 0
			d := -t.e
			n.totalDeficit += d
			u := d
			if u > n.z {
				u = n.z
			}
			n.z -= u
			n.totalAbsorbed += u
			item.Absorbed = u
			rem := d - u
			if rem > 0 {
				for rem > 0 {
					// 以当前 C 重新枚举候选。
					var cands []naiveCandidate
					for id, c := range n.accounts {
						if c.q == 0 || (c.q > 0) == (acc.q > 0) {
							continue
						}
						if _, ok := inLambda[id]; ok {
							continue
						}
						pnl := c.q*price - c.c
						if pnl <= 0 {
							continue
						}
						cands = append(cands, naiveCandidate{id, c, pnl, i64abs(c.c)})
					}
					sort.Slice(cands, func(i, j int) bool {
						lhs := new(big.Int).Mul(big.NewInt(cands[i].pnl), big.NewInt(cands[j].absC))
						rhs := new(big.Int).Mul(big.NewInt(cands[j].pnl), big.NewInt(cands[i].absC))
						if lhs.Cmp(rhs) != 0 {
							return lhs.Cmp(rhs) > 0
						}
						return cands[i].id < cands[j].id
					})
					if len(cands) == 0 {
						break
					}
					cd := cands[0]
					take := rem
					if take > cd.pnl {
						take = cd.pnl
					}
					cd.acc.c += take
					rem -= take
					n.totalADL += take
					item.ADL = append(item.ADL, ADLItem{Account: cd.id, Take: take})
				}
				if rem > 0 {
					n.b += rem
					item.BadDebt = rem
				}
			}
		}
		acc.q, acc.c = 0, 0
		items = append(items, item)
	}

	n.lastMark = price
	return items, nOK
}

func (n *naiveEngine) invariantsHold() bool {
	if n.z != n.totalFines-n.totalAbsorbed {
		return false
	}
	if n.totalDeficit != n.totalAbsorbed+n.totalADL+n.b {
		return false
	}
	for _, acc := range n.accounts {
		if acc.m < 0 || n.z < 0 || n.b < 0 {
			return false
		}
		if (acc.q == 0) != (acc.c == 0) {
			return false
		}
		if (acc.q > 0 && acc.c <= 0) || (acc.q < 0 && acc.c >= 0) {
			return false
		}
	}
	return true
}
