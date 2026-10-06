package ontology

import "sync"

// System 是寄售库存领用结算系统的入口（门面）。
// 所有操作在单一互斥锁下串行化，并发结果等价于某个串行顺序；
// 查询操作在同一把锁下读取一致快照，不可能读到领用拆分到一半的状态。
type System struct {
	mu sync.Mutex

	clock  int64
	limits map[productKey]int64
	onHand map[productKey]int64

	batchSeq map[productKey]BatchID
	batches  map[productKey]map[BatchID]*Batch

	prices *priceBook
	index  *batchIndex

	lineSeq LineID
	lines   map[LineID]*SettlementLine
	ledger  []ledgerEntry
}

// ledgerEntry 是按发生时刻有序的金额流水（领用为正，冲销为负）。
type ledgerEntry struct {
	time     int64
	supplier ID
	amount   int64
	reversal bool
}

// NewSystem 创建空系统，初始时钟为 0。
func NewSystem() *System {
	return &System{
		limits:   map[productKey]int64{},
		onHand:   map[productKey]int64{},
		batchSeq: map[productKey]BatchID{},
		batches:  map[productKey]map[BatchID]*Batch{},
		prices:   newPriceBook(),
		index:    newBatchIndex(),
		lines:    map[LineID]*SettlementLine{},
	}
}

func isNonNeg(v int64) bool { return v >= 0 }

// checkClock 校验时钟单调；调用方须持锁。
func (s *System) checkClock(t int64) error {
	if t < s.clock {
		return newErr(KindClockRollback, "operation time is before the current clock")
	}
	return nil
}

func (s *System) batchMap(key productKey) map[BatchID]*Batch {
	m := s.batches[key]
	if m == nil {
		m = map[BatchID]*Batch{}
		s.batches[key] = m
	}
	return m
}

// expiredOnHand 重算某 (供应商,商品) 在时刻 now 的已到期剩余量。
// 到期是时刻的纯函数，直接遍历该键下批次即可；批次数量只随该键的
// 到货数增长（不随已耗尽批次或历史结算行增长），故查询开销可接受。
func (s *System) expiredOnHand(key productKey, now int64) int64 {
	var expired int64
	for _, b := range s.batches[key] {
		if b.ExpiredAtTime(now) {
			expired += b.Remaining
		}
	}
	return expired
}

// SetLimit 设置某供应商某商品的在库量上限。
func (s *System) SetLimit(t int64, supplier, product ID, limit int64) error {
	if !isNonNeg(t) || !isNonNeg(limit) {
		return newErr(KindInvalidParam, "time and limit must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	s.limits[productKey{supplier, product}] = limit
	s.clock = t
	return nil
}

// RegisterPrice 登记价格协议，[start,end) 左闭右开，同键区间不得重叠。
func (s *System) RegisterPrice(t int64, supplier, product ID, start, end, price int64) error {
	if !isNonNeg(t) || start < 0 || end <= start || price < 0 {
		return newErr(KindInvalidParam, "bad interval or negative price")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	if err := s.prices.register(supplier, product, start, end, price); err != nil {
		return err
	}
	s.clock = t
	return nil
}

// Receive 到货一批寄售货物。在库量包含已到期但尚未退回的剩余量，
// 到货后恰等于上限允许，超出则整批拒绝。
func (s *System) Receive(t int64, supplier, product ID, quantity, duration int64) (BatchID, error) {
	if !isNonNeg(t) || quantity <= 0 || duration < 0 {
		return 0, newErr(KindInvalidParam, "quantity must be positive and duration non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return 0, err
	}
	key := productKey{supplier, product}
	limit, ok := s.limits[key]
	if !ok {
		return 0, newErr(KindNotFound, "no capacity limit registered for supplier/product")
	}
	if s.onHand[key]+quantity > limit {
		return 0, newErr(KindOverCap, "receipt would exceed the on-hand capacity")
	}
	s.clock = t
	seq := s.batchSeq[key] + 1
	s.batchSeq[key] = seq
	b := &Batch{
		Supplier:  supplier,
		Product:   product,
		Number:    seq,
		Arrival:   t,
		Quantity:  quantity,
		Remaining: quantity,
		Duration:  duration,
	}
	s.batchMap(key)[seq] = b
	s.index.push(supplier, product, seq, t)
	s.onHand[key] += quantity
	return seq, nil
}

// Return 退回指定批次的部分或全部剩余量，将其移出寄售库存。
func (s *System) Return(t int64, supplier, product ID, number BatchID, quantity int64) error {
	if !isNonNeg(t) || quantity <= 0 {
		return newErr(KindInvalidParam, "quantity must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	key := productKey{supplier, product}
	b, ok := s.batches[key][number]
	if !ok {
		return newErr(KindNotFound, "batch not found")
	}
	if quantity > b.Remaining {
		return newErr(KindExcessQuantity, "return quantity exceeds batch remaining")
	}
	s.clock = t
	b.Remaining -= quantity
	s.onHand[key] -= quantity
	return nil
}

// Consume 按 FIFO 领用某商品；跨供应商统一排序，整笔成功或整笔拒绝。
func (s *System) Consume(t int64, product ID, quantity int64) (*Receipt, error) {
	if !isNonNeg(t) || quantity <= 0 {
		return nil, newErr(KindInvalidParam, "quantity must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return nil, err
	}

	stateOf := func(supplier ID, number BatchID) (int64, bool, bool) {
		b, ok := s.batches[productKey{supplier, product}][number]
		if !ok {
			return 0, false, false
		}
		return b.Remaining, true, b.ExpiredAtTime(t)
	}

	// 纯扫描：在堆快照上做 FIFO 分配，不修改真实堆与任何状态。
	allocs, survivors, dropped, involved, satisfied := s.index.scan(product, t, quantity, stateOf)
	if !satisfied {
		// 价格检查优先级高于库存不足：涉及供应商无有效价时报无有效价格。
		for sup := range involved {
			if _, ok := s.prices.priceAt(sup, product, t); !ok {
				return nil, newErr(KindNoValidPrice, "no valid price for an involved supplier")
			}
		}
		return nil, newErr(KindInsufficientStock, "available stock is less than requested")
	}

	// 全部满足：先做价格校验，任一供应商缺价则整笔拒绝。
	unitPrice := map[ID]int64{}
	for sup := range involved {
		p, ok := s.prices.priceAt(sup, product, t)
		if !ok {
			return nil, newErr(KindNoValidPrice, "no valid price for an involved supplier")
		}
		unitPrice[sup] = p
	}

	// 提交：此时才推进时钟、重建堆、扣减批次并写结算行与流水。
	s.clock = t
	s.index.commitScan(product, survivors, dropped)
	r := &Receipt{Time: t}
	for _, a := range allocs {
		key := productKey{a.supplier, product}
		b := s.batches[key][a.number]
		b.Remaining -= a.qty
		s.onHand[key] -= a.qty
		s.lineSeq++
		line := &SettlementLine{
			ID:          s.lineSeq,
			ConsumeTime: t,
			Supplier:    a.supplier,
			Product:     product,
			BatchNumber: a.number,
			Quantity:    a.qty,
			UnitPrice:   unitPrice[a.supplier],
		}
		s.lines[line.ID] = line
		r.Lines = append(r.Lines, *line)
		s.ledger = append(s.ledger, ledgerEntry{
			time:     t,
			supplier: a.supplier,
			amount:   a.qty * unitPrice[a.supplier],
		})
	}
	return r, nil
}

// Reverse 冲销某结算行尚未冲销的部分；数量按原批次、原单价回退。
func (s *System) Reverse(t int64, line LineID, quantity int64) error {
	if !isNonNeg(t) || quantity <= 0 {
		return newErr(KindInvalidParam, "quantity must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	l, ok := s.lines[line]
	if !ok {
		return newErr(KindNotFound, "settlement line not found")
	}
	open := l.Quantity - l.Reversed
	if quantity > open {
		return newErr(KindExcessQuantity, "reversal quantity exceeds the un-reversed portion")
	}
	key := productKey{l.Supplier, l.Product}
	alreadyExpired := s.batches[key][l.BatchNumber].ExpiredAtTime(t)
	s.clock = t
	// 冲销同样占用在库上限额度（已到期部分也占额度）。
	if s.onHand[key]+quantity > s.limits[key] {
		return newErr(KindOverCap, "reversal would exceed the on-hand capacity")
	}

	b := s.batches[key][l.BatchNumber]
	b.Remaining += quantity
	s.onHand[key] += quantity
	if !alreadyExpired {
		// 冲销时刻未到期则重新可领用（到期与否只看当前时刻）。
		s.index.reactivate(l.Supplier, l.Product, l.BatchNumber, b.Arrival, false)
	}
	l.Reversed += quantity
	s.ledger = append(s.ledger, ledgerEntry{
		time:     t,
		supplier: l.Supplier,
		amount:   quantity * l.UnitPrice,
		reversal: true,
	})
	return nil
}

// Statement 生成某供应商 [start,end) 周期的对账单。
// end 不得大于当前时钟，否则报周期未结束。
func (s *System) Statement(supplier ID, start, end int64) (*Statement, error) {
	if start < 0 || end < start {
		return nil, newErr(KindInvalidParam, "bad statement period")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if end > s.clock {
		return nil, newErr(KindPeriodOpen, "statement period has not ended")
	}
	st := &Statement{Supplier: supplier, Start: start, End: end}
	for _, e := range s.ledger {
		if e.supplier != supplier || e.time < start || e.time >= end {
			continue
		}
		if e.reversal {
			st.Reversed += e.amount
		} else {
			st.Consumed += e.amount
		}
	}
	st.Net = st.Consumed - st.Reversed
	return st, nil
}

// OnHand 查询某供应商某商品的在库量及其中已到期部分。
// 只读、不推进时钟；到期以调用时的当前时钟判定。
func (s *System) OnHand(supplier, product ID) (total, expired int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := productKey{supplier, product}
	return s.onHand[key], s.expiredOnHand(key, s.clock)
}

// ReversedQty 查询某结算行已冲销数量；只读。
func (s *System) ReversedQty(line LineID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.lines[line]
	if !ok {
		return 0, newErr(KindNotFound, "settlement line not found")
	}
	return l.Reversed, nil
}

// Clock 返回当前时钟（只读）。
func (s *System) Clock() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}
