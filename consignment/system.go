package consignment

import "sync"

// System 是寄售结算系统的门面，协调时钟、价格协议、库存与台账。
// 所有操作可并发调用：写操作互斥、读操作共享，
// 结果等价于某个串行顺序；读操作永远看到某个已完成操作之后的一致快照。
type System struct {
	mu      sync.RWMutex
	clk     clock
	prices  *pricebook
	inv     *inventory
	led     *ledger
	nextBat uint64 // 批次号递增序列（从 1 开始）
	nextLin uint64 // 结算行号递增序列（从 1 开始）
	metrics Metrics
}

// Statement 是一份对账单：某供应商在 [from, to) 周期内的应付净额。
type Statement struct {
	Supplier      uint64
	From, To      int64
	DrawTotal     int64 // 周期内领用金额之和
	ReversalTotal int64 // 周期内冲销金额之和
	Net           int64 // 应付净额 = DrawTotal - ReversalTotal
}

// Metrics 是用于验证领用考察复杂度的计数器。
type Metrics struct {
	// AllocExamined 历次领用在分配阶段考察的批次记录总数。
	AllocExamined uint64
	// ExpiryScanned 历次领用为摘除到期批次而扫描的索引记录总数。
	ExpiryScanned uint64
}

// New 创建一个空系统。
func New() *System {
	return &System{
		prices: newPricebook(),
		inv:    newInventory(),
		led:    newLedger(),
	}
}

// Now 返回当前时钟（上一次被接受操作的时刻）。只读，不推进时钟。
func (s *System) Now() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clk.now()
}

// Metrics 返回用于验证领用考察复杂度的计数器快照。
func (s *System) Metrics() Metrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.metrics
}

// SetCap 设置某供应商某商品的在库量上限。
func (s *System) SetCap(supplier, item, cap uint64, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.clk.check(t); err != nil {
		return err
	}
	s.inv.setCap(supplier, item, cap)
	s.clk.accept(t)
	return nil
}

// AddPrice 登记价格协议，生效区间 [from, to)。
// 同一供应商同一商品的协议区间不得重叠。
func (s *System) AddPrice(supplier, item uint64, from, to, unitPrice int64, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from < 0 || to <= from || unitPrice <= 0 {
		return ErrInvalidParam
	}
	// 区间重叠属参数非法类，先于时钟回退判定。
	a := agreement{from: from, to: to, unitPrice: unitPrice}
	if s.prices.overlaps(supplier, item, a) {
		return ErrOverlappingAgreement
	}
	if err := s.clk.check(t); err != nil {
		return err
	}
	s.prices.insert(supplier, item, a)
	s.clk.accept(t)
	return nil
}

// Arrive 登记到货批次，返回批次号。
// 在库量含已到期未退回部分；到货后恰等于上限允许，超出则整批拒绝。
func (s *System) Arrive(supplier, item, qty uint64, period int64, t int64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if qty == 0 || period <= 0 {
		return 0, ErrInvalidParam
	}
	if err := s.clk.check(t); err != nil {
		return 0, err
	}
	if s.inv.onHandOf(supplier, item)+qty > s.inv.capOf(supplier, item) {
		return 0, ErrOverCap
	}
	s.nextBat++
	id := s.nextBat
	s.inv.arrive(id, supplier, item, qty, period, t)
	s.clk.accept(t)
	return id, nil
}

// Return 退回某批次的部分或全部剩余量。
func (s *System) Return(batchID, qty uint64, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if qty == 0 {
		return ErrInvalidParam
	}
	if err := s.clk.check(t); err != nil {
		return err
	}
	b := s.inv.get(batchID)
	if b == nil {
		return ErrNotFound
	}
	if qty > b.remaining {
		return ErrOverAmount
	}
	s.inv.removeQty(b, qty)
	s.clk.accept(t)
	return nil
}

// Draw 领用某商品指定数量，按先到先出跨供应商分配批次。
// 成功时返回拆分出的结算行；任何拒绝都不改变任何状态与时钟。
func (s *System) Draw(item, qty uint64, t int64) ([]Line, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if qty == 0 {
		return nil, ErrInvalidParam
	}
	if err := s.clk.check(t); err != nil {
		return nil, err
	}
	st := s.inv.stockOf(item)
	// 以不超过当前时钟的时刻物理清理到期批次：对任何操作都安全。
	s.metrics.ExpiryScanned += uint64(s.inv.purgeExpired(st, s.clk.now()))
	allocs, examined, skipped := s.inv.allocate(item, qty, t)
	s.metrics.AllocExamined += uint64(examined)
	s.metrics.ExpiryScanned += uint64(skipped)
	// 无有效价格先于库存不足判定：任一涉及供应商缺价即整笔拒绝。
	var total uint64
	unitOf := make(map[uint64]int64)
	for _, a := range allocs {
		total += a.take
		if _, ok := unitOf[a.b.supplier]; ok {
			continue
		}
		p, ok := s.prices.priceAt(a.b.supplier, item, t)
		if !ok {
			return nil, ErrNoValidPrice
		}
		unitOf[a.b.supplier] = p
	}
	if total < qty {
		return nil, ErrInsufficientStock
	}
	s.inv.commit(allocs)
	// 领用被接受后 t 成为新时钟，此刻物理清理到期批次安全。
	s.metrics.ExpiryScanned += uint64(s.inv.purgeExpired(st, t))
	lines := make([]Line, 0, len(allocs))
	for _, a := range allocs {
		p := unitOf[a.b.supplier]
		s.nextLin++
		line := Line{
			ID: s.nextLin, Supplier: a.b.supplier, Item: item,
			BatchID: a.b.id, Qty: a.take, UnitPrice: p,
			Amount: int64(a.take) * p, Time: t,
		}
		cp := line
		s.led.addLine(&cp)
		lines = append(lines, line)
	}
	s.clk.accept(t)
	return lines, nil
}

// Reverse 冲销某结算行的一部分，数量退回原批次。
// 金额按原结算行单价计，不使用冲销时刻的价格。
func (s *System) Reverse(lineID, qty uint64, t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if qty == 0 {
		return ErrInvalidParam
	}
	if err := s.clk.check(t); err != nil {
		return err
	}
	line := s.led.get(lineID)
	if line == nil {
		return ErrNotFound
	}
	// 超上限先于过量类判定。
	if s.inv.onHandOf(line.Supplier, line.Item)+qty > s.inv.capOf(line.Supplier, line.Item) {
		return ErrOverCap
	}
	if qty > line.Qty-line.Reversed {
		return ErrOverAmount
	}
	s.led.recordReversal(line, qty, t)
	s.inv.restore(s.inv.get(line.BatchID), qty, t)
	s.clk.accept(t)
	return nil
}

// OnHand 查询某供应商某商品的在库量及其中已到期部分。
// 以调用时的当前时钟判定到期，只读，不推进时钟。
func (s *System) OnHand(supplier, item uint64) (total, expired uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.inv.onHandAndExpired(supplier, item, s.clk.now())
}

// ReversedQty 查询某结算行已冲销的数量。只读。
func (s *System) ReversedQty(lineID uint64) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.led.get(lineID)
	if line == nil {
		return 0, ErrNotFound
	}
	return line.Reversed, nil
}

// Statement 出具某供应商在 [from, to) 周期内的对账单。
// 右端点不得大于当前时钟，否则报周期未结束。只读，不推进时钟。
func (s *System) Statement(supplier uint64, from, to int64) (Statement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Statement{Supplier: supplier, From: from, To: to}
	if from < 0 || to <= from {
		return st, ErrInvalidParam
	}
	if to > s.clk.now() {
		return st, ErrPeriodNotEnded
	}
	st.DrawTotal, st.ReversalTotal = s.led.statement(supplier, from, to)
	st.Net = st.DrawTotal - st.ReversalTotal
	return st, nil
}
