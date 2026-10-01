// Package settlement implements a single-series cash-settled option
// expiration engine: exercise, assignment, default and cash movement.
package settlement

import (
	"errors"
	"math/big"
	"sort"
	"sync"
)

// OptionType is the kind of the option series.
type OptionType int

const (
	// Call pays max(S-K, 0) per unit at settlement.
	Call OptionType = iota
	// Put pays max(K-S, 0) per unit at settlement.
	Put
)

// Distinguishable rejection reasons. Returned wrapped; test with errors.Is.
var (
	// ErrInvalidParam: constructor or method argument out of range, empty
	// account name, or cumulative limit (trade total / margin total) exceeded.
	ErrInvalidParam = errors.New("settlement: invalid parameter")
	// ErrExpired: the series has already been settled.
	ErrExpired = errors.New("settlement: series already expired")
	// ErrSelfTrade: buyer equals seller.
	ErrSelfTrade = errors.New("settlement: self trade")
	// ErrNoLongPosition: Abstain on an account with no long batch.
	ErrNoLongPosition = errors.New("settlement: no long position")
)

const (
	maxStrike     = 1_000_000
	maxMultiplier = 1_000
	maxThreshold  = 1_000_000
	maxTradeQty   = 1_000_000
	maxTotalQty   = 1_000_000_000
	maxMargin     = 1_000_000_000_000
	maxTotalMgn   = 1_000_000_000_000_000
	maxSettlePx   = 1_000_000
)

// batch is one open lot. Long and short batches of one Trade share seq.
type batch struct {
	seq  uint64
	acct string
	n    uint64
}

// Exercise is one exercising account and its total exercised quantity.
type Exercise struct {
	Account  string
	Quantity uint64
}

// Assignment is one short batch (all of them, by seq) and the quantity
// assigned to it; zero when the batch was not reached.
type Assignment struct {
	Seq      uint64
	Account  string
	Quantity uint64
}

// Default is one account's unpaid shortfall D_a > 0.
type Default struct {
	Account string
	Amount  uint64
}

// NetCash is one account's final net cash: recv_a - loss_a - pay_a.
type NetCash struct {
	Account string
	Net     int64
}

// Settlement is the full, deterministic outcome of a successful Settle.
type Settlement struct {
	Value       uint64 // per-unit intrinsic value v
	Total       uint64 // total exercised quantity Q
	Exercises   []Exercise
	Assignments []Assignment
	Defaults    []Default
	NetCash     []NetCash
}

// Engine is a single option series. All methods are safe for concurrent
// use; the result is equivalent to some serial order.
type Engine struct {
	mu sync.Mutex

	kind OptionType
	k    uint64
	mult uint64
	th   uint64

	seq        uint64
	totalQty   uint64
	longs      map[string][]batch
	shorts     []batch
	margins    map[string]uint64
	abstained  map[string]bool
	seen       map[string]bool
	settled    bool
	settlement *Settlement
}

// NewEngine validates the series parameters and returns an engine.
func NewEngine(kind OptionType, k, mult, threshold uint64) (*Engine, error) {
	if kind != Call && kind != Put {
		return nil, ErrInvalidParam
	}
	if k < 1 || k > maxStrike || mult < 1 || mult > maxMultiplier ||
		threshold < 1 || threshold > maxThreshold {
		return nil, ErrInvalidParam
	}
	return &Engine{
		kind:      kind,
		k:         k,
		mult:      mult,
		th:        threshold,
		longs:     make(map[string][]batch),
		margins:   make(map[string]uint64),
		abstained: make(map[string]bool),
		seen:      make(map[string]bool),
	}, nil
}

// Trade opens a long batch for buyer and a short batch for seller and
// returns the shared batch sequence number (starting at 1).
func (e *Engine) Trade(buyer, seller string, n uint64) (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if buyer == "" || seller == "" || n < 1 || n > maxTradeQty ||
		e.totalQty+n > maxTotalQty {
		return 0, ErrInvalidParam
	}
	if e.settled {
		return 0, ErrExpired
	}
	if buyer == seller {
		return 0, ErrSelfTrade
	}

	e.seq++
	b := batch{seq: e.seq, n: n}
	e.longs[buyer] = append(e.longs[buyer], b)
	e.shorts = append(e.shorts, batch{seq: e.seq, acct: seller, n: n})
	e.totalQty += n
	e.seen[buyer] = true
	e.seen[seller] = true
	return e.seq, nil
}

// Margin adds g to acct's margin balance.
func (e *Engine) Margin(acct string, g uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if acct == "" || g < 1 || g > maxMargin ||
		e.margins[acct]+g > maxTotalMgn {
		return ErrInvalidParam
	}
	if e.settled {
		return ErrExpired
	}

	e.margins[acct] += g
	e.seen[acct] = true
	return nil
}

// Abstain registers account-level opt-out of auto exercise.
func (e *Engine) Abstain(acct string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if acct == "" {
		return ErrInvalidParam
	}
	if e.settled {
		return ErrExpired
	}
	if len(e.longs[acct]) == 0 {
		return ErrNoLongPosition
	}

	e.abstained[acct] = true
	return nil
}

// Settle expires the series at price s. Succeeds exactly once.
func (e *Engine) Settle(s uint64) (*Settlement, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if s < 1 || s > maxSettlePx {
		return nil, ErrInvalidParam
	}
	if e.settled {
		return nil, ErrExpired
	}
	e.settled = true

	var v uint64
	switch e.kind {
	case Call:
		if s > e.k {
			v = s - e.k
		}
	default: // Put
		if e.k > s {
			v = e.k - s
		}
	}

	res := &Settlement{Value: v}

	// Exercise: every long batch of every non-abstained account, when v >= T.
	recv := make(map[string]uint64)
	var exercisers []string
	if v >= e.th {
		for acct, lots := range e.longs {
			if e.abstained[acct] {
				continue
			}
			var qa uint64
			for _, b := range lots {
				qa += b.n
			}
			if qa == 0 {
				continue
			}
			exercisers = append(exercisers, acct)
			recv[acct] = v * qa * e.mult
			res.Total += qa
		}
		sort.Strings(exercisers)
		for _, acct := range exercisers {
			var qa uint64
			for _, b := range e.longs[acct] {
				qa += b.n
			}
			res.Exercises = append(res.Exercises, Exercise{Account: acct, Quantity: qa})
		}
	}

	// Assignment: short batches in seq order, fill until Q is exhausted.
	owe := make(map[string]uint64)
	remaining := res.Total
	for _, b := range e.shorts {
		q := b.n
		if q > remaining {
			q = remaining
		}
		remaining -= q
		res.Assignments = append(res.Assignments, Assignment{
			Seq:      b.seq,
			Account:  b.acct,
			Quantity: q,
		})
		if q > 0 {
			owe[b.acct] += v * q * e.mult
		}
	}

	// Payment: pay_a = min(margin_a, owe_a); shortfall D_a = owe_a - pay_a.
	pay := make(map[string]uint64)
	var delta uint64
	for acct, o := range owe {
		p := o
		if m := e.margins[acct]; m < p {
			p = m
		}
		pay[acct] = p
		if d := o - p; d > 0 {
			res.Defaults = append(res.Defaults, Default{Account: acct, Amount: d})
			delta += d
		}
	}
	sort.Slice(res.Defaults, func(i, j int) bool {
		return res.Defaults[i].Account < res.Defaults[j].Account
	})

	// Loss sharing: loss_a = floor(delta*recv_a/sumRecv), remainder +1 each
	// to exercising accounts in byte order. delta*recv_a needs 128 bits.
	loss := make(map[string]uint64)
	if delta > 0 {
		var sumRecv uint64
		for _, r := range recv {
			sumRecv += r
		}
		bigDelta := new(big.Int).SetUint64(delta)
		bigSum := new(big.Int).SetUint64(sumRecv)
		var shared uint64
		for _, acct := range exercisers {
			prod := new(big.Int).Mul(bigDelta, new(big.Int).SetUint64(recv[acct]))
			l := prod.Quo(prod, bigSum).Uint64()
			loss[acct] = l
			shared += l
		}
		for i, rho := 0, delta-shared; rho > 0; i = (i + 1) % len(exercisers) {
			loss[exercisers[i]]++
			rho--
		}
	}

	// Net cash for every account ever seen: recv_a - loss_a - pay_a.
	accts := make([]string, 0, len(e.seen))
	for acct := range e.seen {
		accts = append(accts, acct)
	}
	sort.Strings(accts)
	for _, acct := range accts {
		net := int64(recv[acct]) - int64(loss[acct]) - int64(pay[acct])
		res.NetCash = append(res.NetCash, NetCash{Account: acct, Net: net})
	}

	e.settlement = res
	return res, nil
}

// Settled reports whether the series has expired.
func (e *Engine) Settled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settled
}

// MarginOf returns the current margin balance of acct.
func (e *Engine) MarginOf(acct string) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.margins[acct]
}

// TotalTraded returns the sum of n over all successful trades.
func (e *Engine) TotalTraded() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.totalQty
}
