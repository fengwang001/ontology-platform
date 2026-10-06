package deposit_test

import (
	"math/big"
	"sort"

	"ontology/deposit"
)

// naiveItem is the reference model's deduction record.
type naiveItem struct {
	id                int
	cat               deposit.Category
	amount            int64
	day               int
	order             int
	revoked, disputed bool
	adjudged          bool
	award             int64
	adjudgeDay        int
}

type naiveBatch struct {
	amount   int64
	deadline int
	paid     bool
	paidDay  int
}

type naiveRefund struct {
	day     int
	amount  int64
	penalty *big.Rat
}

// NaiveModel is an independently written reference: it keeps the raw event
// log and recomputes every allocation from scratch by sorting all active
// items each time.  It intentionally shares no code with the real service.
type NaiveModel struct {
	cfg deposit.Config

	created   bool
	dep       int64
	checked   bool
	checkout  int
	lastNow   int
	items     []*naiveItem
	itemByID  map[int]*naiveItem
	nextID    int
	finalized bool
	batches   []*naiveBatch
	refunds   []naiveRefund
}

func NewNaive(cfg deposit.Config) *NaiveModel {
	return &NaiveModel{cfg: cfg, nextID: 1, itemByID: map[int]*naiveItem{}}
}

func (m *NaiveModel) declClose() int    { return m.checkout + m.cfg.A }
func (m *NaiveModel) disputeClose() int { return m.declClose() + m.cfg.B }

type naiveView struct {
	refunded, landlord, frozen, pending, receivable, refundable int64
	penalty                                                     *big.Rat
	satisfied                                                   map[int]int64
	award                                                       map[int]int64
	refunds                                                     []naiveRefund
}

func validCatNaive(c deposit.Category) bool { return c >= deposit.Rent && c <= deposit.Other }

func (m *NaiveModel) CreateLease(id string, dep int64, now int) error {
	if id == "" {
		return deposit.ErrInvalidParam
	}
	if m.created {
		return deposit.ErrInvalidParam
	}
	if dep <= 0 {
		return deposit.ErrAmount
	}
	m.created = true
	m.dep = dep
	m.lastNow = now
	return nil
}

func (m *NaiveModel) Checkout(id string, now int) error {
	if id == "" {
		return deposit.ErrInvalidParam
	}
	if !m.created {
		return deposit.ErrNoLease
	}
	if now < m.lastNow {
		return deposit.ErrClockRollback
	}
	if m.checked {
		return deposit.ErrState
	}
	m.checked = true
	m.checkout = now
	m.lastNow = now
	return nil
}

func (m *NaiveModel) Declare(id string, cat deposit.Category, amount int64, now int) (int, error) {
	if id == "" || !validCatNaive(cat) {
		return 0, deposit.ErrInvalidParam
	}
	if id != "L" {
		return 0, deposit.ErrNoLease
	}
	if !m.created {
		return 0, deposit.ErrNoLease
	}
	if now < m.lastNow {
		return 0, deposit.ErrClockRollback
	}
	if !m.checked {
		return 0, deposit.ErrNotOut
	}
	if amount <= 0 {
		return 0, deposit.ErrAmount
	}
	if now > m.declClose() {
		return 0, deposit.ErrLateDeclare
	}
	it := &naiveItem{id: m.nextID, cat: cat, amount: amount, day: now, order: len(m.items), award: -1}
	m.nextID++
	m.items = append(m.items, it)
	m.itemByID[it.id] = it
	m.lastNow = now
	return it.id, nil
}

func (m *NaiveModel) Revoke(id string, dedID int, now int) error {
	if id == "" || dedID <= 0 {
		return deposit.ErrInvalidParam
	}
	if id != "L" {
		return deposit.ErrNoLease
	}
	if !m.created {
		return deposit.ErrNoLease
	}
	if now < m.lastNow {
		return deposit.ErrClockRollback
	}
	if !m.checked {
		return deposit.ErrNotOut
	}
	it := m.itemByID[dedID]
	if it == nil {
		return deposit.ErrState
	}
	if now > m.declClose() {
		return deposit.ErrLateDeclare
	}
	if it.revoked {
		return deposit.ErrState
	}
	it.revoked = true
	m.lastNow = now
	return nil
}

// recomputeSatisfied is the deliberately slow O(n log n) allocation:
// sort a fresh copy by category then declaration order every time.
func (m *NaiveModel) recomputeSatisfied() map[int]int64 {
	active := make([]*naiveItem, 0, len(m.items))
	for _, it := range m.items {
		if !it.revoked {
			active = append(active, it)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].cat != active[j].cat {
			return active[i].cat < active[j].cat
		}
		return active[i].order < active[j].order
	})
	out := map[int]int64{}
	remaining := m.dep
	for _, it := range active {
		give := it.amount
		if give > remaining {
			give = remaining
		}
		out[it.id] = give
		remaining -= give
	}
	return out
}

func (m *NaiveModel) materialize(now int) {
	if !m.finalized {
		m.finalized = true
		sat := m.recomputeSatisfied()
		var committed int64
		for _, it := range m.items {
			if !it.revoked {
				committed += sat[it.id]
			}
		}
		if base := m.dep - committed; base > 0 {
			m.batches = append(m.batches, &naiveBatch{amount: base, deadline: m.declClose() + m.cfg.C})
		}
	}
}

func (m *NaiveModel) Dispute(id string, dedID int, now int) error {
	if id == "" || dedID <= 0 {
		return deposit.ErrInvalidParam
	}
	if id != "L" {
		return deposit.ErrNoLease
	}
	if !m.created {
		return deposit.ErrNoLease
	}
	if now < m.lastNow {
		return deposit.ErrClockRollback
	}
	if !m.checked {
		return deposit.ErrNotOut
	}
	it := m.itemByID[dedID]
	if it == nil {
		return deposit.ErrState
	}
	if now <= m.declClose() || now > m.disputeClose() {
		return deposit.ErrLateDispute
	}
	if it.revoked || it.disputed {
		return deposit.ErrState
	}
	m.materialize(now)
	it.disputed = true
	m.lastNow = now
	return nil
}

func (m *NaiveModel) Adjudicate(id string, dedID int, award int64, now int) error {
	if id == "" || dedID <= 0 {
		return deposit.ErrInvalidParam
	}
	if id != "L" {
		return deposit.ErrNoLease
	}
	if !m.created {
		return deposit.ErrNoLease
	}
	if now < m.lastNow {
		return deposit.ErrClockRollback
	}
	if !m.checked {
		return deposit.ErrNotOut
	}
	it := m.itemByID[dedID]
	if it == nil {
		return deposit.ErrState
	}
	if !it.disputed || it.adjudged {
		return deposit.ErrState
	}
	if award < 0 || award > it.amount {
		return deposit.ErrAmount
	}
	m.materialize(now)
	it.adjudged = true
	it.award = award
	it.adjudgeDay = now
	sat := m.recomputeSatisfied()
	frozen := sat[it.id]
	released := frozen - min64(award, frozen)
	if released > 0 {
		m.batches = append(m.batches, &naiveBatch{amount: released, deadline: now + m.cfg.C})
	}
	m.lastNow = now
	return nil
}

func (m *NaiveModel) penalty(amount int64, deadline, now int) *big.Rat {
	days := int64(now - 1 - deadline)
	if days <= 0 {
		return new(big.Rat)
	}
	return new(big.Rat).SetFrac(big.NewInt(amount*days*m.cfg.RateNum), big.NewInt(m.cfg.RateDen))
}

func (m *NaiveModel) Refund(id string, now int) (*deposit.RefundRecord, error) {
	if id == "" {
		return nil, deposit.ErrInvalidParam
	}
	if id != "L" {
		return nil, deposit.ErrNoLease
	}
	if !m.created {
		return nil, deposit.ErrNoLease
	}
	if now < m.lastNow {
		return nil, deposit.ErrClockRollback
	}
	if !m.checked {
		return nil, deposit.ErrNotOut
	}
	if now <= m.declClose() {
		return nil, deposit.ErrState
	}
	m.materialize(now)

	var total int64
	pen := new(big.Rat)
	var paying []*naiveBatch
	for _, b := range m.batches {
		if !b.paid {
			paying = append(paying, b)
			total += b.amount
			pen.Add(pen, m.penalty(b.amount, b.deadline, now))
		}
	}
	if total == 0 {
		return nil, deposit.ErrState
	}
	for _, b := range paying {
		b.paid = true
		b.paidDay = now
	}
	r := &deposit.RefundRecord{Day: now, Amount: total, Penalty: pen}
	m.refunds = append(m.refunds, naiveRefund{day: now, amount: total, penalty: pen})
	m.lastNow = now
	return r, nil
}

func (m *NaiveModel) viewExists(now int) *naiveView {
	v := &naiveView{
		penalty:   new(big.Rat),
		satisfied: map[int]int64{},
		award:     map[int]int64{},
	}
	for _, it := range m.items {
		v.award[it.id] = -1
	}
	if now < m.lastNow {
		return nil
	}
	for _, r := range m.refunds {
		v.refunded += r.amount
	}
	// Default award sentinel for every known item.
	if !m.checked {
		for _, it := range m.items {
			v.satisfied[it.id] = 0
		}
		v.pending = m.dep
		return v
	}
	if !m.finalized {
		for _, it := range m.items {
			v.satisfied[it.id] = 0
		}
		v.pending = m.dep
		return v
	}
	sat := m.recomputeSatisfied()
	for _, it := range m.items {
		v.satisfied[it.id] = sat[it.id]
		if it.adjudged {
			v.award[it.id] = it.award
		} else {
			v.award[it.id] = -1
		}
		if it.revoked {
			continue
		}
		s := sat[it.id]
		switch {
		case !m.finalized:
		case it.adjudged:
			upheld := min64(it.award, s)
			v.landlord += upheld
			owed := it.award
			if owed > s {
				v.receivable += owed - s
			}
		case it.disputed:
			v.frozen += s
			if it.amount > s {
				v.receivable += it.amount - s
			}
		default:
			v.landlord += s
			if it.amount > s {
				v.receivable += it.amount - s
			}
		}
	}
	if m.finalized {
		for _, b := range m.batches {
			if !b.paid {
				v.refundable += b.amount
				v.penalty.Add(v.penalty, m.penalty(b.amount, b.deadline, now))
			}
		}
	}
	var awaiting int64
	for _, b := range m.batches {
		if !b.paid {
			awaiting += b.amount
		}
	}
	v.pending = m.dep - v.refunded - v.landlord - v.frozen - awaiting
	return v
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// View handles missing-lease/clock checks like the real Snapshot, then
// delegates to the pure view computation.
func (m *NaiveModel) ViewAt(id string, now int) *naiveView {
	if id != "L" || !m.created {
		return nil
	}
	if now < m.lastNow {
		return nil
	}
	return m.viewExists(now)
}
