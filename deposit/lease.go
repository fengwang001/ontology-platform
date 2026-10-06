package deposit

import "math/big"

// deduction is the mutable internal representation.  Satisfied is assigned
// exactly once by finalize; afterwards no other item's Satisfied ever changes.
type deduction struct {
	id        int
	cat       Category
	amount    int64
	declared  int
	order     int // global declaration sequence among non-revoked items
	revoked   bool
	disputed  bool
	adjudged  bool
	awarded   int64
	satisfied int64
}

// batch is one independently timed, all-or-nothing refundable tranche.
type batch struct {
	amount   int64
	deadline int // day by which refund incurs no penalty
	paid     bool
	paidDay  int
	penalty  *big.Rat // accrued penalty at payment
}

type lease struct {
	id       string
	deposit  int64
	checkout int
	checked  bool
	lastNow  int

	nextID int
	items  map[int]*deduction
	seq    []*deduction // declaration order

	finalized bool

	// Money buckets maintained incrementally.
	refunded int64
	landlord int64
	frozen   int64
	awaiting int64 // committed into unpaid refund batches

	batches []*batch
	refunds []RefundRecord
}

func newLease(id string, deposit int64, now int) *lease {
	return &lease{
		id:       id,
		deposit:  deposit,
		checkout: now,
		lastNow:  now,
		nextID:   1,
		items:    make(map[int]*deduction),
	}
}

func (l *lease) declClose(cfg Config) int { return l.checkout + cfg.A }
func (l *lease) disputeClose(cfg Config) int {
	return l.declClose(cfg) + cfg.B
}

// pending is whatever deposit money has not yet been committed to any of the
// four disposition buckets; conservation is deposit = refunded + landlord
// + frozen + awaiting + pending.
func (l *lease) pending() int64 {
	return l.deposit - l.refunded - l.landlord - l.frozen - l.awaiting
}

// receivable is the landlord's claim against the tenant that the deposit
// could not cover.  Upheld-but-unallocated amounts accrue here; an item lost
// on adjudication stops being receivable.
func (l *lease) receivable() int64 {
	if !l.finalized {
		return 0
	}
	var total int64
	for _, it := range l.seq {
		if it.revoked {
			continue
		}
		owed := it.amount
		if it.adjudged {
			owed = it.awarded
		}
		if owed > it.satisfied {
			total += owed - it.satisfied
		}
	}
	return total
}

// finalize freezes every item's deposit allocation once, at declaration
// close.  It runs in O(n) exactly once per lease; all subsequent dispute and
// adjudication work is O(1) per item and never re-scans this slice.
func (l *lease) finalize(cfg Config) {
	if l.finalized {
		return
	}
	l.finalized = true

	remaining := l.deposit
	for cat := Rent; cat <= Other; cat++ {
		for _, it := range l.seq {
			if it.revoked || it.cat != cat {
				continue
			}
			give := it.amount
			if give > remaining {
				give = remaining
			}
			it.satisfied = give
			remaining -= give
		}
	}

	// Committed allocations are provisionally landlord money; disputes move
	// individual items between frozen and landlord later in O(1).
	var committed int64
	for _, it := range l.seq {
		if !it.revoked {
			committed += it.satisfied
		}
	}
	l.landlord = committed

	// Everything not committed to a live item is the undisputed base tranche.
	l.addBatch(l.deposit-committed, l.declClose(cfg)+cfg.C)
}

func (l *lease) addBatch(amount int64, deadline int) {
	if amount <= 0 {
		return
	}
	l.batches = append(l.batches, &batch{amount: amount, deadline: deadline})
	l.awaiting += amount
}

// penalty computes the breach liability of one tranche as of day now:
// amount * rate * max(0, now-1-deadline).  Days from deadline+1 through the
// day before actual payment count; paying exactly on deadline day counts 0.
func penalty(amount int64, deadline, now int, cfg Config) *big.Rat {
	days := int64(now - 1 - deadline)
	if days <= 0 {
		return new(big.Rat)
	}
	r := new(big.Rat).SetFrac(big.NewInt(amount*days*cfg.RateNum), big.NewInt(cfg.RateDen))
	return r
}
