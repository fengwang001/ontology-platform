package greenreg

// meter is the state of one metered generation period.
type meter struct {
	qty    int64 // latest registered quantity for the period
	in     int64 // frozen input remainder when the period first joined the chain
	remain int64 // account remainder after this period was (re)computed
	live   int64 // non-revoked certificates of this period
}

// facility holds eligibility and metering state for one generation facility.
type facility struct {
	holder  string
	unit    int64
	start   int64 // eligibility start period (inclusive); immutable
	end     int64 // eligibility end period (exclusive); 0 = none
	hasEnd  bool
	balance int64 // current facility-wide sub-unit remainder
	meters  map[int64]*meter
	tail    int64 // period that currently feeds the account remainder
	hasTail bool
}

func newFacility(holder string, start, unit int64) *facility {
	return &facility{holder: holder, start: start, unit: unit, meters: make(map[int64]*meter)}
}

// eligible reports whether generation period p lies in [start, end).
func (f *facility) eligible(p int64) bool {
	if p < f.start {
		return false
	}
	if f.hasEnd && p >= f.end {
		return false
	}
	return true
}

// issuancePlan is the result of applying a (possibly corrected) metered value.
type issuancePlan struct {
	issue   int64 // new certificates to issue (serial increasing)
	revoke  int64 // live certificates that must be revoked
	balance int64 // new facility-wide remainder
}

// planIssuance computes the plan without mutating any state. input is the
// remainder that feeds this period: for a tail period it is the current
// balance; for an earlier-period correction the input is frozen at the value
// recorded when that period followed its tail.
func planIssuance(qty, input, live, unit int64) issuancePlan {
	total := qty + input
	want := total / unit
	return issuancePlan{
		issue:   max64(0, want-live),
		revoke:  max64(0, live-want),
		balance: total % unit,
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// setEnd validates and records an eligibility termination period. end=0 clears
// the termination. Moving an existing end earlier is rejected at parameter
// level; any change must not exclude already issued periods.
func (f *facility) setEnd(end int64, maxIssued int64, haveIssued bool) *RegistryError {
	if end < 0 {
		return newErr(ErrInvalid, "终止期编号越界")
	}
	if f.hasEnd && end != 0 && end < f.end {
		return newErr(ErrInvalid, "资格终止期不可提前")
	}
	if end != 0 && haveIssued && maxIssued >= end {
		return newErr(ErrConflictIssued, "终止期会使已核发证书落到资格区间之外")
	}
	if end == 0 {
		f.hasEnd = false
		f.end = 0
	} else {
		f.hasEnd = true
		f.end = end
	}
	return nil
}

// applyIssuance commits a planned issuance/correction for a period. liveAfter
// is the authoritative non-revoked certificate count. The account remainder
// chain advances in registration order: the newest period becomes the tail;
// correcting the current tail recomputes it.
func (f *facility) applyIssuance(period, qty, input, liveAfter int64, pl issuancePlan) {
	m := f.meters[period]
	if m == nil {
		m = &meter{in: input}
		f.meters[period] = m
		f.tail, f.hasTail = period, true
	}
	m.qty = qty
	m.remain = pl.balance
	m.live = liveAfter
	if f.hasTail && f.tail == period {
		f.balance = pl.balance
	}
}

func (f *facility) maxIssuedGeneration() (int64, bool) {
	best := int64(0)
	found := false
	for p, m := range f.meters {
		if m.live > 0 && (!found || p > best) {
			best, found = p, true
		}
	}
	return best, found
}
