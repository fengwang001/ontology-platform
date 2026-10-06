package transfer

import "sync"

// System 是在途调拨管理系统。所有方法可并发调用，
// 结果等价于某个串行顺序；相同操作序列重放得到完全相同的结果。
type System struct {
	mu sync.RWMutex

	cfg Config

	// 仓库 -> 商品 -> 库存
	warehouses map[string]map[string]*stock
	// 单号 -> 调拨单
	orders map[string]*order
	// 商品 -> 初始化时的全网总量
	initial map[string]int64
	// 已出现过的全部商品（用于守恒核验）
	products map[string]struct{}

	lastAccepted int64 // 上一次被接受操作的时刻；-1 表示尚无
}

type stock struct {
	available int64
	frozen    int64
}

type orderLine struct {
	product  string
	qty      int64 // 发出量（等于创建时的行数量）
	received int64 // 累计收货量
	shortage int64 // 当前短缺
	surplus  int64 // 累计超收盈余
}

type order struct {
	id        string
	src       string
	dst       string
	lines     []orderLine
	status    OrderStatus
	shippedAt int64
}

// NewSystem 以初始库存构建系统。stock 为 仓库 -> 商品 -> 可用量。
func NewSystem(cfg Config, initialStock map[string]map[string]int64) (*System, error) {
	if cfg.OverReceiptTolerancePermille < 0 || cfg.OverReceiptTolerancePermille > 1000 {
		return nil, ErrInvalidParam
	}
	if cfg.CloseWaitSeconds < 0 {
		return nil, ErrInvalidParam
	}
	s := &System{
		cfg:          cfg,
		warehouses:   make(map[string]map[string]*stock, len(initialStock)),
		orders:       make(map[string]*order),
		initial:      make(map[string]int64),
		products:     make(map[string]struct{}),
		lastAccepted: -1,
	}
	for wh, stocks := range initialStock {
		if wh == "" {
			return nil, ErrInvalidParam
		}
		if _, dup := s.warehouses[wh]; dup {
			return nil, ErrInvalidParam
		}
		m := make(map[string]*stock, len(stocks))
		for product, qty := range stocks {
			if product == "" || qty < 0 {
				return nil, ErrInvalidParam
			}
			m[product] = &stock{available: qty}
			s.initial[product] += qty
			s.products[product] = struct{}{}
		}
		s.warehouses[wh] = m
	}
	return s, nil
}

// checkClock 校验时刻合法性与时钟回退。
func (s *System) checkClock(now int64) error {
	if now < 0 {
		return ErrInvalidParam
	}
	if now < s.lastAccepted {
		return ErrClockRegression
	}
	return nil
}

// accept 在操作被接受后推进时钟。被拒绝的操作不得改变任何状态与时钟。
func (s *System) accept(now int64) {
	if now > s.lastAccepted {
		s.lastAccepted = now
	}
}

// stockOf 取某仓某商品的库存记录；仓库不存在返回 nil，商品不存在则补零记录。
func (s *System) stockOf(warehouse, product string) *stock {
	m, ok := s.warehouses[warehouse]
	if !ok {
		return nil
	}
	st, ok := m[product]
	if !ok {
		st = &stock{}
		m[product] = st
	}
	return st
}

// tolerance 返回超收容忍额：发出量 * 全局千分比，向下取整。
func (s *System) tolerance(shipped int64) int64 {
	return shipped * s.cfg.OverReceiptTolerancePermille / 1000
}

func (s *System) orderOrNotFound(id string) (*order, error) {
	o, ok := s.orders[id]
	if !ok {
		return nil, ErrOrderNotFound
	}
	return o, nil
}

func validateLineIndex(o *order, lineIndex int) error {
	if lineIndex < 0 || lineIndex >= len(o.lines) {
		return ErrInvalidParam
	}
	return nil
}

// CreateOrder 创建调拨单并在源仓把各行数量从可用转入冻结（整单全有或全无）。
func (s *System) CreateOrder(now int64, id string, src, dst string, lines []Line) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. 参数非法
	if id == "" || src == "" || dst == "" || src == dst || len(lines) == 0 {
		return ErrInvalidParam
	}
	seen := make(map[string]struct{}, len(lines))
	for _, ln := range lines {
		if ln.Product == "" || ln.Qty <= 0 {
			return ErrInvalidParam
		}
		if _, dup := seen[ln.Product]; dup {
			return ErrInvalidParam
		}
		seen[ln.Product] = struct{}{}
	}
	if _, ok := s.warehouses[src]; !ok {
		return ErrInvalidParam
	}
	if _, ok := s.warehouses[dst]; !ok {
		return ErrInvalidParam
	}
	if _, dup := s.orders[id]; dup {
		return ErrDuplicateOrder
	}
	// 2. 时钟回退
	if err := s.checkClock(now); err != nil {
		return err
	}
	// 5. 业务拒绝：任一行可用量不足则整单拒绝，报下标最小的不足行。
	for i, ln := range lines {
		st := s.stockOf(src, ln.Product)
		if st.available < ln.Qty {
			return &InsufficientStockError{LineIndex: i, Product: ln.Product, Want: ln.Qty, Have: st.available}
		}
	}
	// 全部校验通过，整单应用。
	o := &order{id: id, src: src, dst: dst, status: StatusCreated, lines: make([]orderLine, len(lines))}
	for i, ln := range lines {
		st := s.stockOf(src, ln.Product)
		st.available -= ln.Qty
		st.frozen += ln.Qty
		o.lines[i] = orderLine{product: ln.Product, qty: ln.Qty}
		s.products[ln.Product] = struct{}{}
	}
	s.orders[id] = o
	s.accept(now)
	return nil
}

// CancelOrder 取消尚未发出的调拨单，释放冻结。
func (s *System) CancelOrder(now int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, err := s.orderOrNotFound(id)
	if err != nil {
		return err
	}
	switch o.status {
	case StatusShipped:
		return ErrOrderAlreadyShipped
	case StatusClosed:
		return ErrOrderClosed
	case StatusCancelled:
		return ErrOrderCancelled
	}
	for _, ln := range o.lines {
		st := s.stockOf(o.src, ln.product)
		st.frozen -= ln.qty
		st.available += ln.qty
	}
	o.status = StatusCancelled
	s.accept(now)
	return nil
}

// ShipOrder 发出调拨单：各行冻结量转为在途并从源仓扣除。只允许一次。
func (s *System) ShipOrder(now int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, err := s.orderOrNotFound(id)
	if err != nil {
		return err
	}
	switch o.status {
	case StatusShipped:
		return ErrOrderAlreadyShipped
	case StatusClosed:
		return ErrOrderClosed
	case StatusCancelled:
		return ErrOrderCancelled
	}
	for _, ln := range o.lines {
		st := s.stockOf(o.src, ln.product)
		st.frozen -= ln.qty // 冻结量转为在途，从源仓扣除
	}
	o.status = StatusShipped
	o.shippedAt = now
	s.accept(now)
	return nil
}

// Receive 对已发出未关闭单的某行收货，可分批，数量为正。
func (s *System) Receive(now int64, id string, lineIndex int, qty int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" || qty <= 0 {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, err := s.orderOrNotFound(id)
	if err != nil {
		return err
	}
	if err := validateLineIndex(o, lineIndex); err != nil {
		return err
	}
	switch o.status {
	case StatusCreated:
		return ErrOrderNotShipped
	case StatusClosed:
		return ErrOrderClosed
	case StatusCancelled:
		return ErrOrderCancelled
	}
	ln := &o.lines[lineIndex]
	limit := ln.qty + s.tolerance(ln.qty)
	if ln.received+qty > limit {
		return &OverReceiveError{LineIndex: lineIndex, Limit: limit, Attempt: ln.received + qty}
	}
	excessBefore := ln.received - ln.qty
	if excessBefore < 0 {
		excessBefore = 0
	}
	excessAfter := ln.received + qty - ln.qty
	if excessAfter < 0 {
		excessAfter = 0
	}
	ln.surplus += excessAfter - excessBefore // 超出发出量的部分登记为超收盈余
	ln.received += qty
	st := s.stockOf(o.dst, ln.product)
	st.available += qty
	s.accept(now)
	return nil
}

// CloseOrder 关闭调拨单并登记短缺/盈余差异。
func (s *System) CloseOrder(now int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, err := s.orderOrNotFound(id)
	if err != nil {
		return err
	}
	switch o.status {
	case StatusCreated:
		return ErrOrderNotShipped
	case StatusClosed:
		return ErrOrderClosed
	case StatusCancelled:
		return ErrOrderCancelled
	}
	allReceived := true
	for i := range o.lines {
		if o.lines[i].received < o.lines[i].qty {
			allReceived = false
			break
		}
	}
	if !allReceived && now-o.shippedAt < s.cfg.CloseWaitSeconds {
		return &NotClosableYetError{Now: now, Earliest: o.shippedAt + s.cfg.CloseWaitSeconds}
	}
	for i := range o.lines {
		ln := &o.lines[i]
		if ln.qty > ln.received {
			ln.shortage = ln.qty - ln.received // 在途短缺，不回到源仓
		}
	}
	o.status = StatusClosed
	s.accept(now)
	return nil
}

// Recover 对已关闭单的某行短缺做事后找回。
func (s *System) Recover(now int64, id string, lineIndex int, qty int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" || qty <= 0 {
		return ErrInvalidParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, err := s.orderOrNotFound(id)
	if err != nil {
		return err
	}
	if err := validateLineIndex(o, lineIndex); err != nil {
		return err
	}
	switch o.status {
	case StatusCancelled:
		return ErrOrderCancelled
	case StatusCreated, StatusShipped:
		return ErrOrderNotClosed
	}
	ln := &o.lines[lineIndex]
	if ln.shortage == 0 {
		return ErrNoShortage
	}
	if qty > ln.shortage {
		return &RecoverExcessError{LineIndex: lineIndex, Max: ln.shortage, Attempt: qty}
	}
	ln.shortage -= qty
	st := s.stockOf(o.dst, ln.product)
	st.available += qty
	s.accept(now)
	return nil
}

// Stock 查询某仓某商品的可用量与冻结量。只读。
func (s *System) Stock(warehouse, product string) (StockSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, ok := s.warehouses[warehouse]
	if !ok {
		return StockSnapshot{}, false
	}
	st, ok := m[product]
	if !ok {
		return StockSnapshot{}, true
	}
	return StockSnapshot{Available: st.available, Frozen: st.frozen}, true
}

// OrderLines 查询某调拨单各行状态与单状态。只读。
func (s *System) OrderLines(id string) ([]LineStatus, OrderStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	o, ok := s.orders[id]
	if !ok {
		return nil, StatusCreated, ErrOrderNotFound
	}
	out := make([]LineStatus, len(o.lines))
	for i, ln := range o.lines {
		out[i] = LineStatus{
			Product:  ln.product,
			Shipped:  ln.qty,
			Received: ln.received,
			Shortage: ln.shortage,
			Surplus:  ln.surplus,
		}
	}
	return out, o.status, nil
}

// VerifyConservation 对全部商品做守恒核验，返回是否全部平衡及逐项明细。
// 对每个商品：可用 + 冻结 + 在途 + 短缺 == 初始总量 + 累计超收盈余。
func (s *System) VerifyConservation() (bool, []ConservationEntry) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	totals := make(map[string]*ConservationEntry, len(s.products))
	for p := range s.products {
		totals[p] = &ConservationEntry{Product: p, Initial: s.initial[p], Balanced: true}
	}
	for _, m := range s.warehouses {
		for product, st := range m {
			e, ok := totals[product]
			if !ok {
				continue
			}
			e.Available += st.available
			e.Frozen += st.frozen
		}
	}
	for _, o := range s.orders {
		for _, ln := range o.lines {
			e, ok := totals[ln.product]
			if !ok {
				continue
			}
			if o.status == StatusShipped && ln.received < ln.qty {
				e.InTransit += ln.qty - ln.received
			}
			e.Shortage += ln.shortage
			e.Surplus += ln.surplus
		}
	}
	all := true
	out := make([]ConservationEntry, 0, len(totals))
	for _, e := range totals {
		e.Balanced = e.Available+e.Frozen+e.InTransit+e.Shortage == e.Initial+e.Surplus
		if !e.Balanced {
			all = false
		}
		out = append(out, *e)
	}
	return all, out
}
