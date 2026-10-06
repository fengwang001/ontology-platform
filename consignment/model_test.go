package consignment

import (
	"sort"
)

// 本文件包含一个独立编写的朴素模型，用于与优化实现对照。
// 模型刻意使用最直白的数据结构（切片 + 线性扫描），
// 与被测实现不共享任何代码，语义仅依据需求文档推导。

type mInterval struct {
	from, to int64
	price    int64
}

type mBatch struct {
	id, supplier, item uint64
	arrival, period    int64
	qty, remaining     uint64
}

type mLine struct {
	id, supplier, item, batch uint64
	qty, reversed             uint64
	unitPrice                 int64
	time                      int64
}

type mEvent struct {
	time           int64
	draw, reversal int64
}

type mKey struct{ supplier, item uint64 }

type model struct {
	clock    int64
	caps     map[mKey]uint64
	hasCap   map[mKey]bool
	prices   map[mKey][]mInterval
	batches  []*mBatch
	lines    map[uint64]*mLine
	events   map[uint64][]mEvent
	nextBat  uint64
	nextLine uint64
}

func newModel() *model {
	return &model{
		caps:   make(map[mKey]uint64),
		hasCap: make(map[mKey]bool),
		prices: make(map[mKey][]mInterval),
		lines:  make(map[uint64]*mLine),
		events: make(map[uint64][]mEvent),
	}
}

func (m *model) checkClock(t int64) error {
	if t < 0 {
		return ErrInvalidParam
	}
	if t < m.clock {
		return ErrClockRewind
	}
	return nil
}

func (m *model) onHand(supplier, item uint64) uint64 {
	var sum uint64
	for _, b := range m.batches {
		if b.supplier == supplier && b.item == item {
			sum += b.remaining
		}
	}
	return sum
}

func (m *model) setCap(supplier, item, cap uint64, t int64) error {
	if err := m.checkClock(t); err != nil {
		return err
	}
	k := mKey{supplier, item}
	m.caps[k] = cap
	m.hasCap[k] = true
	m.clock = t
	return nil
}

func (m *model) addPrice(supplier, item uint64, from, to, price int64, t int64) error {
	if from < 0 || to <= from || price <= 0 {
		return ErrInvalidParam
	}
	k := mKey{supplier, item}
	for _, iv := range m.prices[k] {
		if iv.from < to && from < iv.to {
			return ErrOverlappingAgreement
		}
	}
	if err := m.checkClock(t); err != nil {
		return err
	}
	m.prices[k] = append(m.prices[k], mInterval{from, to, price})
	m.clock = t
	return nil
}

func (m *model) priceAt(supplier, item uint64, t int64) (int64, bool) {
	for _, iv := range m.prices[mKey{supplier, item}] {
		if iv.from <= t && t < iv.to {
			return iv.price, true
		}
	}
	return 0, false
}

func (m *model) arrive(supplier, item, qty uint64, period int64, t int64) (uint64, error) {
	if qty == 0 || period <= 0 {
		return 0, ErrInvalidParam
	}
	if err := m.checkClock(t); err != nil {
		return 0, err
	}
	k := mKey{supplier, item}
	if m.hasCap[k] && m.onHand(supplier, item)+qty > m.caps[k] {
		return 0, ErrOverCap
	}
	m.nextBat++
	m.batches = append(m.batches, &mBatch{
		id: m.nextBat, supplier: supplier, item: item,
		arrival: t, period: period, qty: qty, remaining: qty,
	})
	m.clock = t
	return m.nextBat, nil
}

func (m *model) findBatch(id uint64) *mBatch {
	for _, b := range m.batches {
		if b.id == id {
			return b
		}
	}
	return nil
}

func (m *model) ret(batchID, qty uint64, t int64) error {
	if qty == 0 {
		return ErrInvalidParam
	}
	if err := m.checkClock(t); err != nil {
		return err
	}
	b := m.findBatch(batchID)
	if b == nil {
		return ErrNotFound
	}
	if qty > b.remaining {
		return ErrOverAmount
	}
	b.remaining -= qty
	m.clock = t
	return nil
}

func (m *model) draw(item, qty uint64, t int64) ([]Line, error) {
	if qty == 0 {
		return nil, ErrInvalidParam
	}
	if err := m.checkClock(t); err != nil {
		return nil, err
	}
	// 收集候选批次并排序：到货时刻、供应商编号、批次号。
	var cand []*mBatch
	for _, b := range m.batches {
		if b.item == item && b.remaining > 0 && t < b.arrival+b.period {
			cand = append(cand, b)
		}
	}
	sort.Slice(cand, func(i, j int) bool {
		if cand[i].arrival != cand[j].arrival {
			return cand[i].arrival < cand[j].arrival
		}
		if cand[i].supplier != cand[j].supplier {
			return cand[i].supplier < cand[j].supplier
		}
		return cand[i].id < cand[j].id
	})
	// 预分配。
	type alloc struct {
		b    *mBatch
		take uint64
	}
	var allocs []alloc
	need := qty
	for _, b := range cand {
		if need == 0 {
			break
		}
		take := b.remaining
		if take > need {
			take = need
		}
		allocs = append(allocs, alloc{b, take})
		need -= take
	}
	// 无有效价格先于库存不足。
	seen := make(map[uint64]bool)
	for _, a := range allocs {
		if seen[a.b.supplier] {
			continue
		}
		seen[a.b.supplier] = true
		if _, ok := m.priceAt(a.b.supplier, item, t); !ok {
			return nil, ErrNoValidPrice
		}
	}
	if need > 0 {
		return nil, ErrInsufficientStock
	}
	var lines []Line
	for _, a := range allocs {
		p, _ := m.priceAt(a.b.supplier, item, t)
		a.b.remaining -= a.take
		m.nextLine++
		ln := &mLine{
			id: m.nextLine, supplier: a.b.supplier, item: item,
			batch: a.b.id, qty: a.take, unitPrice: p, time: t,
		}
		m.lines[ln.id] = ln
		m.events[a.b.supplier] = append(m.events[a.b.supplier],
			mEvent{time: t, draw: int64(a.take) * p})
		lines = append(lines, Line{
			ID: ln.id, Supplier: ln.supplier, Item: item, BatchID: ln.batch,
			Qty: ln.qty, UnitPrice: p, Amount: int64(a.take) * p, Time: t,
		})
	}
	m.clock = t
	return lines, nil
}

func (m *model) reverse(lineID, qty uint64, t int64) error {
	if qty == 0 {
		return ErrInvalidParam
	}
	if err := m.checkClock(t); err != nil {
		return err
	}
	ln := m.lines[lineID]
	if ln == nil {
		return ErrNotFound
	}
	k := mKey{ln.supplier, ln.item}
	if m.hasCap[k] && m.onHand(ln.supplier, ln.item)+qty > m.caps[k] {
		return ErrOverCap
	}
	if qty > ln.qty-ln.reversed {
		return ErrOverAmount
	}
	ln.reversed += qty
	m.findBatch(ln.batch).remaining += qty
	m.events[ln.supplier] = append(m.events[ln.supplier],
		mEvent{time: t, reversal: int64(qty) * ln.unitPrice})
	m.clock = t
	return nil
}

func (m *model) onHandExpired(supplier, item uint64) (total, expired uint64) {
	for _, b := range m.batches {
		if b.supplier == supplier && b.item == item {
			total += b.remaining
			if b.remaining > 0 && m.clock >= b.arrival+b.period {
				expired += b.remaining
			}
		}
	}
	return total, expired
}

func (m *model) reversedQty(lineID uint64) (uint64, error) {
	ln := m.lines[lineID]
	if ln == nil {
		return 0, ErrNotFound
	}
	return ln.reversed, nil
}

func (m *model) statement(supplier uint64, from, to int64) (Statement, error) {
	st := Statement{Supplier: supplier, From: from, To: to}
	if from < 0 || to <= from {
		return st, ErrInvalidParam
	}
	if to > m.clock {
		return st, ErrPeriodNotEnded
	}
	for _, e := range m.events[supplier] {
		if from <= e.time && e.time < to {
			st.DrawTotal += e.draw
			st.ReversalTotal += e.reversal
		}
	}
	st.Net = st.DrawTotal - st.ReversalTotal
	return st, nil
}
