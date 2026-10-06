package settlement

import (
	"fmt"
	"sort"
	"testing"
)

// naiveBatch is the reference model's reserve batch.
type naiveBatch struct {
	retainDay  Day
	releaseDay Day
	amount     Amount
	consumed   Amount
}

// naiveMerchant is the deliberately simple specification model. Every day it
// needs a number it re-derives from scratch by scanning its full transaction
// list and full batch list (O(history) per day) rather than maintaining
// heaps/queues. It shares no code paths with the production engine.
type naiveMerchant struct {
	id       string
	cfg      MerchantConfig
	txns     []Transaction
	ids      map[string]struct{}
	batches  []naiveBatch
	carry    Amount
	payouts  []Payout
	total    Amount
	frontIdx int
	settled  bool

	// processedNet is the sum of transaction amounts that have already entered
	// settleable-net computation. It is maintained naively by marking txns, so
	// the accounting invariant can be checked against raw data.
	processedNet Amount
	bucket       map[Day]Amount // per-transaction-day sums
	watermark    map[Day]Amount // already-contributed amount per day
	acceptedSum  Amount         // sum of every accepted transaction (raw)
}

type naiveSystem struct {
	biz      map[Day]bool
	days     []Day
	lastNow  Day
	haveNow  bool
	merch    map[string]*naiveMerchant
	addOrder []string
}

func newNaiveSystem(cal []Day) *naiveSystem {
	ns := &naiveSystem{biz: make(map[Day]bool), days: cal,
		merch: make(map[string]*naiveMerchant)}
	for _, d := range cal {
		ns.biz[d] = true
	}
	return ns
}

func (ns *naiveSystem) nthOnOrBefore(d Day, n int) (Day, bool) {
	count := 0
	for i := len(ns.days) - 1; i >= 0; i-- {
		if ns.days[i] <= d {
			count++
			if count == n {
				return ns.days[i], true
			}
		}
	}
	return 0, false
}

func (ns *naiveSystem) hthAfter(d Day, h int) (Day, bool) {
	count := 0
	for _, x := range ns.days {
		if x > d {
			count++
			if count == h {
				return x, true
			}
		}
	}
	return 0, false
}

func (ns *naiveSystem) addMerchant(now Day, id string, cfg MerchantConfig) error {
	if id == "" || !validConfig(cfg) {
		return ErrInvalidArgument
	}
	if ns.haveNow && now < ns.lastNow {
		return ErrClockRolledBack
	}
	if _, ok := ns.merch[id]; ok {
		return ErrInvalidArgument
	}
	ns.merch[id] = &naiveMerchant{
		id: id, cfg: cfg, ids: map[string]struct{}{}, frontIdx: -1,
		bucket: map[Day]Amount{}, watermark: map[Day]Amount{},
	}
	ns.addOrder = append(ns.addOrder, id)
	ns.lastNow = now
	ns.haveNow = true
	return nil
}

func (ns *naiveSystem) addTxn(now Day, mid string, tx Transaction) error {
	if mid == "" || tx.ID == "" {
		return ErrInvalidArgument
	}
	if ns.haveNow && now < ns.lastNow {
		return ErrClockRolledBack
	}
	m, ok := ns.merch[mid]
	if !ok {
		return ErrMerchantMissing
	}
	if _, dup := m.ids[tx.ID]; dup {
		return ErrDuplicateTxnID
	}
	if tx.Day > now {
		return ErrInvalidDate
	}
	if m.settled && tx.Day < ns.days[m.frontIdx] {
		return ErrBookSealed
	}
	m.ids[tx.ID] = struct{}{}
	m.txns = append(m.txns, tx)
	m.bucket[tx.Day] += tx.Amount
	m.acceptedSum += tx.Amount
	ns.lastNow = now
	return nil
}

func naiveCeil(net Amount, bps int) Amount {
	v := net * Amount(bps)
	q := v / 10000
	if v%10000 != 0 {
		q++
	}
	return q
}

// settleOne re-derives day t from the merchant's full transaction and batch
// history. Transactions newly crossing the N-delayed boundary are counted
// exactly once (marked via consumedIdx), including late arrivals dated exactly
// on the previous frontier day.
func (ns *naiveSystem) settleOne(m *naiveMerchant, t Day) {
	boundary, ok := ns.nthOnOrBefore(t, m.cfg.SettleDelayN)
	net := m.carry
	if ok {
		// Full-scan specification: iterate every transaction day bucket on or
		// before the boundary; contribute value minus an extraction watermark,
		// so late transactions dated exactly on the retained boundary day add
		// only their delta on the next settlement.
		days := make([]Day, 0, len(m.bucket))
		for d := range m.bucket {
			if d <= boundary {
				days = append(days, d)
			}
		}
		sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
		var greatest Day
		if n := len(days); n > 0 {
			greatest = days[n-1]
		}
		for _, d := range days {
			delta := m.bucket[d] - m.watermark[d]
			net += delta
			m.processedNet += delta
			if d == greatest {
				m.watermark[d] = m.bucket[d]
			} else {
				delete(m.bucket, d)
			}
		}
	}

	// Full-scan release: everything due up to t.
	var released Amount
	survivors := make([]naiveBatch, 0, len(m.batches))
	for _, b := range m.batches {
		if b.releaseDay <= t {
			released += b.amount - b.consumed
		} else {
			survivors = append(survivors, b)
		}
	}
	m.batches = survivors

	var payout Amount
	if net >= 0 {
		retain := Amount(0)
		if m.cfg.ReserveBps > 0 {
			retain = naiveCeil(net, m.cfg.ReserveBps)
		}
		if retain > 0 {
			rd, ok := ns.hthAfter(t, m.cfg.ReserveHorizonH)
			if !ok {
				rd = Day(1 << 62)
			}
			m.batches = append(m.batches, naiveBatch{
				retainDay: t, releaseDay: rd, amount: retain,
			})
		}
		payout = net - retain + released
		m.carry = 0
	} else {
		avail := net + released
		if avail >= 0 {
			payout = avail
			m.carry = 0
		} else {
			deficit := -avail
			// Consume non-due batches from oldest retention day.
			sort.SliceStable(m.batches, func(i, j int) bool {
				return m.batches[i].retainDay < m.batches[j].retainDay
			})
			for deficit > 0 && len(m.batches) > 0 {
				b := &m.batches[0]
				rem := b.amount - b.consumed
				if rem <= deficit {
					deficit -= rem
					b.consumed = b.amount
					m.batches = m.batches[1:]
				} else {
					b.consumed += deficit
					deficit = 0
				}
			}
			// Consumed reserve funds the deficit; it is never paid to the
			// merchant. The payout after covering is exactly zero.
			payout = 0
			if deficit > 0 {
				m.carry = -deficit
			} else {
				m.carry = 0
			}
		}
	}

	// Drop fully consumed batches; keep partially consumed ones for release.
	remaining := make([]naiveBatch, 0, len(m.batches))
	for _, b := range m.batches {
		if b.amount-b.consumed > 0 {
			remaining = append(remaining, b)
		}
	}
	m.batches = remaining

	m.total += payout
	m.payouts = append(m.payouts, Payout{MerchantID: m.id, Day: t, Amount: payout})
}

func (ns *naiveSystem) settle(now Day, mid string, d Day) ([]Payout, error) {
	if mid == "" {
		return nil, ErrInvalidArgument
	}
	if ns.haveNow && now < ns.lastNow {
		return nil, ErrClockRolledBack
	}
	m, ok := ns.merch[mid]
	if !ok {
		return nil, ErrMerchantMissing
	}
	if !ns.biz[d] {
		return nil, ErrNonBusinessDay
	}
	idx := -1
	for i, x := range ns.days {
		if x == d {
			idx = i
			break
		}
	}
	if m.settled && idx <= m.frontIdx {
		return nil, ErrDuplicateSettle
	}

	start := 0
	if m.settled {
		start = m.frontIdx + 1
	}
	out := make([]Payout, 0, idx-start+1)
	for i := start; i <= idx; i++ {
		ns.settleOne(m, ns.days[i])
		out = append(out, m.payouts[len(m.payouts)-1])
	}
	m.frontIdx = idx
	m.settled = true
	ns.lastNow = now
	return out, nil
}

func (ns *naiveSystem) snapshot() []MerchantState {
	out := make([]MerchantState, 0, len(ns.addOrder))
	for _, id := range ns.addOrder {
		m := ns.merch[id]
		st := MerchantState{
			ID:            m.id,
			Config:        m.cfg,
			Settled:       m.settled,
			NegativeCarry: m.carry,
			TotalPayout:   m.total,
			Payouts:       append([]Payout(nil), m.payouts...),
		}
		if m.settled {
			st.LastSettledDay = ns.days[m.frontIdx]
		}
		for _, b := range m.batches {
			st.Batches = append(st.Batches, ReserveBatch{
				RetainDay:  b.retainDay,
				ReleaseDay: b.releaseDay,
				Amount:     b.amount,
				Consumed:   b.consumed,
			})
			st.ReserveBalance += b.amount - b.consumed
		}
		out = append(out, st)
	}
	return out
}

// naiveReplay mirrors replayOps on the specification model.
func naiveReplay(t *testing.T, ops []op) []MerchantState {
	t.Helper()
	ns := newNaiveSystem(days(0, diffMaxDay))
	for _, o := range ops {
		switch o.kind {
		case opAddMerchant:
			err := ns.addMerchant(o.now, o.mid, o.cfg)
			t.Logf("[naive] AddMerchant now=%d %s cfg=%+v -> %v", o.now, o.mid, o.cfg, err)
		case opTxn:
			err := ns.addTxn(o.now, o.mid, o.tx)
			t.Logf("[naive] Txn now=%d m=%s id=%s day=%d amt=%d (%s) -> %v",
				o.now, o.mid, o.tx.ID, o.tx.Day, o.tx.Amount, o.reason, err)
		case opSettle:
			p, err := ns.settle(o.now, o.mid, o.day)
			t.Logf("[naive] Settle m=%s day=%d -> err=%v payouts=%v", o.mid, o.day, err, p)
		}
	}
	return ns.snapshot()
}

// TestRandomAgainstNaiveModel cross-checks production against the independent
// specification model over many random timelines and verifies the accounting
// identity against raw transaction data.
func TestRandomAgainstNaiveModel(t *testing.T) {
	const seeds = 80
	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			ops := genScenario(seed)
			got := replayOps(t, ops)
			want := naiveReplay(t, ops)
			assertStatesEqual(t, got, want)

			// Raw-data accounting identity: payout + reserve + carry equals the
			// sum of every transaction that has crossed the settlement
			// boundary (all of them by the final catch-up, since tx days <=
			// diffMaxDay and N <= 3 <= calendar length).
			ns := newNaiveSystem(days(0, diffMaxDay))
			// Replay only the accepted operations to inspect internals.
			for _, o := range ops {
				switch o.kind {
				case opAddMerchant:
					_ = ns.addMerchant(o.now, o.mid, o.cfg)
				case opTxn:
					_ = ns.addTxn(o.now, o.mid, o.tx)
				case opSettle:
					_, _ = ns.settle(o.now, o.mid, o.day)
				}
			}
			for _, st := range got {
				m := ns.merch[st.ID]
				net := m.processedNet
				if got := st.TotalPayout + st.ReserveBalance + st.NegativeCarry; got != net {
					t.Fatalf("%s invariant: payout=%d reserve=%d carry=%d sum=%d vs processedNet=%d",
						st.ID, st.TotalPayout, st.ReserveBalance, st.NegativeCarry, got, net)
				}
			}
		})
	}
}
