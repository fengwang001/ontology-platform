// Naive step-by-step simulation of the settlement rules, written directly
// from the spec with big.Int arithmetic. It shares no code with the
// engine and serves as the differential-testing oracle.
package settlement

import (
	"math/big"
	"sort"
)

type naiveErr int

const (
	naiveOK naiveErr = iota
	naiveInvalid
	naiveExpired
	naiveSelfTrade
	naiveNoLong
)

type naiveBatch struct {
	seq  uint64
	acct string
	n    uint64
}

type naiveEngine struct {
	kind OptionType
	k    uint64
	mult uint64
	th   uint64

	seq       uint64
	totalQty  uint64
	longs     map[string][]naiveBatch
	shorts    []naiveBatch
	margins   map[string]*big.Int
	abstained map[string]bool
	seen      map[string]bool
	settled   bool
}

func newNaive(kind OptionType, k, mult, th uint64) *naiveEngine {
	return &naiveEngine{
		kind:      kind,
		k:         k,
		mult:      mult,
		th:        th,
		longs:     make(map[string][]naiveBatch),
		margins:   make(map[string]*big.Int),
		abstained: make(map[string]bool),
		seen:      make(map[string]bool),
	}
}

func (e *naiveEngine) trade(buyer, seller string, n uint64) naiveErr {
	if buyer == "" || seller == "" || n < 1 || n > 1_000_000 ||
		e.totalQty+n > 1_000_000_000 {
		return naiveInvalid
	}
	if e.settled {
		return naiveExpired
	}
	if buyer == seller {
		return naiveSelfTrade
	}
	e.seq++
	e.longs[buyer] = append(e.longs[buyer], naiveBatch{seq: e.seq, acct: buyer, n: n})
	e.shorts = append(e.shorts, naiveBatch{seq: e.seq, acct: seller, n: n})
	e.totalQty += n
	e.seen[buyer] = true
	e.seen[seller] = true
	return naiveOK
}

func (e *naiveEngine) margin(acct string, g uint64) naiveErr {
	if acct == "" || g < 1 || g > 1_000_000_000_000 {
		return naiveInvalid
	}
	cur := e.margins[acct]
	if cur == nil {
		cur = new(big.Int)
	}
	if new(big.Int).Add(cur, new(big.Int).SetUint64(g)).Cmp(big.NewInt(1_000_000_000_000_000)) > 0 {
		return naiveInvalid
	}
	if e.settled {
		return naiveExpired
	}
	e.margins[acct] = new(big.Int).Add(cur, new(big.Int).SetUint64(g))
	e.seen[acct] = true
	return naiveOK
}

func (e *naiveEngine) abstain(acct string) naiveErr {
	if acct == "" {
		return naiveInvalid
	}
	if e.settled {
		return naiveExpired
	}
	if len(e.longs[acct]) == 0 {
		return naiveNoLong
	}
	e.abstained[acct] = true
	return naiveOK
}

type naiveResult struct {
	v           uint64
	q           uint64
	exercises   []Exercise
	assignments []Assignment
	defaults    []Default
	net         []NetCash
}

func (e *naiveEngine) settle(s uint64) (*naiveResult, naiveErr) {
	if s < 1 || s > 1_000_000 {
		return nil, naiveInvalid
	}
	if e.settled {
		return nil, naiveExpired
	}
	e.settled = true

	var v uint64
	if e.kind == Call && s > e.k {
		v = s - e.k
	}
	if e.kind == Put && e.k > s {
		v = e.k - s
	}
	res := &naiveResult{v: v}

	// Exercise.
	recv := make(map[string]*big.Int)
	if v >= e.th {
		var accts []string
		for acct := range e.longs {
			accts = append(accts, acct)
		}
		sort.Strings(accts)
		for _, acct := range accts {
			if e.abstained[acct] {
				continue
			}
			var qa uint64
			for _, b := range e.longs[acct] {
				qa += b.n
			}
			res.q += qa
			res.exercises = append(res.exercises, Exercise{Account: acct, Quantity: qa})
			r := new(big.Int).SetUint64(v)
			r.Mul(r, new(big.Int).SetUint64(qa))
			r.Mul(r, new(big.Int).SetUint64(e.mult))
			recv[acct] = r
		}
	}

	// Assignment in seq order.
	owe := make(map[string]*big.Int)
	remaining := res.q
	for _, b := range e.shorts {
		q := b.n
		if q > remaining {
			q = remaining
		}
		remaining -= q
		res.assignments = append(res.assignments, Assignment{Seq: b.seq, Account: b.acct, Quantity: q})
		if q > 0 {
			o := new(big.Int).SetUint64(v)
			o.Mul(o, new(big.Int).SetUint64(q))
			o.Mul(o, new(big.Int).SetUint64(e.mult))
			if cur := owe[b.acct]; cur != nil {
				o.Add(o, cur)
			}
			owe[b.acct] = o
		}
	}

	// Payment and defaults.
	pay := make(map[string]*big.Int)
	delta := new(big.Int)
	var defAccts []string
	for acct := range owe {
		defAccts = append(defAccts, acct)
	}
	sort.Strings(defAccts)
	for _, acct := range defAccts {
		o := owe[acct]
		m := e.margins[acct]
		if m == nil {
			m = new(big.Int)
		}
		p := new(big.Int).Set(m)
		if o.Cmp(p) < 0 {
			p = new(big.Int).Set(o)
		}
		pay[acct] = p
		d := new(big.Int).Sub(o, p)
		if d.Sign() > 0 {
			res.defaults = append(res.defaults, Default{Account: acct, Amount: d.Uint64()})
			delta.Add(delta, d)
		}
	}

	// Loss sharing with big.Int: loss_a = floor(delta*recv_a/sumRecv),
	// remainder +1 to each exercising account in byte order.
	loss := make(map[string]*big.Int)
	if delta.Sign() > 0 {
		sumRecv := new(big.Int)
		for _, r := range recv {
			sumRecv.Add(sumRecv, r)
		}
		shared := new(big.Int)
		var exercisers []string
		for _, ex := range res.exercises {
			exercisers = append(exercisers, ex.Account)
		}
		for _, acct := range exercisers {
			l := new(big.Int).Mul(delta, recv[acct])
			l.Quo(l, sumRecv)
			loss[acct] = l
			shared.Add(shared, l)
		}
		rho := new(big.Int).Sub(delta, shared)
		one := big.NewInt(1)
		for i := 0; rho.Sign() > 0; i = (i + 1) % len(exercisers) {
			loss[exercisers[i]].Add(loss[exercisers[i]], one)
			rho.Sub(rho, one)
		}
	}

	// Net cash for every seen account.
	var accts []string
	for acct := range e.seen {
		accts = append(accts, acct)
	}
	sort.Strings(accts)
	for _, acct := range accts {
		net := new(big.Int)
		if r := recv[acct]; r != nil {
			net.Add(net, r)
		}
		if l := loss[acct]; l != nil {
			net.Sub(net, l)
		}
		if p := pay[acct]; p != nil {
			net.Sub(net, p)
		}
		res.net = append(res.net, NetCash{Account: acct, Net: net.Int64()})
	}
	return res, naiveOK
}
