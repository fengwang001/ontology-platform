package settlement

import "math/bits"

// merchant owns all mutable state for one merchant. Methods are not internally
// synchronized; System serializes access.
type merchant struct {
	id  string
	cfg MerchantConfig
	cal *calendar

	seenTxn map[string]struct{}

	// Settlement frontier.
	lastSettledIdx int // dense index of the last settled business day
	settled        bool

	// Unsettled transactions grouped by their raw transaction day.
	pending *dayAggregator
	// Active reserve batches in retention-day order.
	reserve *reserveQueue

	// carry is the negative balance (<= 0) carried into the next business day.
	carry Amount

	payouts     []Payout
	totalPayout Amount
}

// DayDetail exposes the full determination for one settled business day; tests
// and logs use it to show the judgment basis.
type DayDetail struct {
	Day             Day
	BoundaryDay     Day
	ExtractedTxns   Amount
	NegativeCarry   Amount // carry entering the day (<= 0)
	SettleableNet   Amount
	Released        Amount
	NewRetained     Amount
	Consumed        Amount
	CarryAfter      Amount
	Payout          Amount
	ReleasedBatches []ReserveBatch
}

func newMerchant(id string, cfg MerchantConfig, cal *calendar) *merchant {
	return &merchant{
		id:             id,
		cfg:            cfg,
		cal:            cal,
		seenTxn:        make(map[string]struct{}),
		lastSettledIdx: -1,
		pending:        newDayAggregator(),
		reserve:        newReserveQueue(),
	}
}

// ceilDivBps computes ceil(net*bps/10000) for net >= 0 using 128-bit
// intermediate arithmetic, avoiding int64 overflow.
func ceilDivBps(net Amount, bps int) Amount {
	hi, lo := bits.Mul64(uint64(net), uint64(bps))
	const den = 10000
	q, rem := bits.Div64(hi, lo, den)
	out := Amount(q)
	if rem != 0 {
		out++
	}
	return out
}

func (m *merchant) addTransaction(tx Transaction) {
	m.seenTxn[tx.ID] = struct{}{}
	m.pending.add(tx.Day, tx.Amount)
}

func (m *merchant) hasTxn(id string) bool {
	_, ok := m.seenTxn[id]
	return ok
}

// lastSettledDay returns the frontier day; the boolean is false before the
// first settlement.
func (m *merchant) lastSettledDay() (Day, bool) {
	if !m.settled {
		return 0, false
	}
	return m.cal.days[m.lastSettledIdx], true
}

// settleOne processes exactly one business day t (given by dense index idx).
func (m *merchant) settleOne(idx int, t Day) DayDetail {
	detail := DayDetail{Day: t, NegativeCarry: m.carry}

	boundary, ok := m.cal.nthOnOrBefore(t, m.cfg.SettleDelayN)
	if !ok {
		// Fewer than N business days exist up to t: nothing is settleable yet.
		detail.BoundaryDay = 0
		detail.SettleableNet = m.carry
	} else {
		detail.BoundaryDay = boundary
		detail.ExtractedTxns = m.pending.removeThrough(boundary)
		detail.SettleableNet = m.carry + detail.ExtractedTxns
	}

	released, releasedBatches := m.reserve.releaseDue(t)
	detail.Released = released
	detail.ReleasedBatches = releasedBatches

	if detail.SettleableNet >= 0 {
		net := detail.SettleableNet
		retain := ceilDivBps(net, m.cfg.ReserveBps)
		detail.NewRetained = retain
		if retain > 0 {
			releaseDay, ok := m.cal.hthAfter(t, m.cfg.ReserveHorizonH)
			batch := ReserveBatch{
				RetainDay: t,
				Amount:    retain,
			}
			if ok {
				batch.ReleaseDay = releaseDay
			} else {
				// Beyond the configured calendar: release can never trigger.
				batch.ReleaseDay = Day(1 << 62)
			}
			m.reserve.push(batch)
		}
		detail.Payout = net - retain + released
		m.carry = 0
		detail.CarryAfter = 0
	} else {
		// Negative net: release is booked first, then older non-due batches
		// fill the remaining deficit; anything still negative carries forward.
		available := detail.SettleableNet + released
		if available >= 0 {
			detail.Payout = available
			m.carry = 0
		} else {
			// Release did not cover the deficit: consume non-due batches to
			// pull the balance toward zero. Consumed reserve offsets the
			// negative (it funds refunds/chargebacks), it is NOT paid to the
			// merchant, so the payout is 0; only a remainder still negative
			// carries to the next business day.
			deficit := -available
			left := m.reserve.consumeFromOlder(deficit)
			detail.Consumed = deficit - left
			detail.Payout = 0
			if left > 0 {
				m.carry = -left
			} else {
				m.carry = 0
			}
		}
		detail.CarryAfter = m.carry
	}

	m.totalPayout += detail.Payout
	m.payouts = append(m.payouts, Payout{MerchantID: m.id, Day: t, Amount: detail.Payout})
	return detail
}

// settleThrough processes every unprocessed business day up to d inclusive.
// Caller guarantees d is a business day after the current frontier.
func (m *merchant) settleThrough(d Day) []DayDetail {
	endIdx, _ := m.cal.index(d)
	startIdx := 0
	if m.settled {
		startIdx = m.lastSettledIdx + 1
	}
	details := make([]DayDetail, 0, endIdx-startIdx+1)
	for idx := startIdx; idx <= endIdx; idx++ {
		details = append(details, m.settleOne(idx, m.cal.days[idx]))
	}
	m.lastSettledIdx = endIdx
	m.settled = true
	return details
}

// state builds a deep-copied snapshot.
func (m *merchant) state() MerchantState {
	last := Day(0)
	if m.settled {
		last = m.cal.days[m.lastSettledIdx]
	}
	payouts := make([]Payout, len(m.payouts))
	copy(payouts, m.payouts)
	return MerchantState{
		ID:             m.id,
		Config:         m.cfg,
		LastSettledDay: last,
		Settled:        m.settled,
		Payouts:        payouts,
		Batches:        m.reserve.snapshot(),
		ReserveBalance: m.reserve.reserveBalance(),
		NegativeCarry:  m.carry,
		TotalPayout:    m.totalPayout,
	}
}
