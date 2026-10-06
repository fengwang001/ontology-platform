package transfer

import (
	"sort"
	"sync"

	"ontology/internal/clock"
	"ontology/internal/inventory"
)

// getOrder 在读锁下取出订单指针；注册表中的单只增不删，
// 取出后即可释放注册表锁，后续互斥由订单自身锁保证。
func (s *Service) getOrder(id string) (*Order, bool) {
	s.registryMu.RLock()
	defer s.registryMu.RUnlock()
	o, ok := s.registry[id]
	return o, ok
}

// Service 是在途调拨管理系统的入口，协调时钟、库存与调拨单三个模块。
type Service struct {
	cfg        Config
	clk        *clock.Clock
	store      *inventory.Store
	initial    map[string]int64 // 商品 → 系统初始化总量
	registry   map[string]*Order
	registryMu sync.RWMutex
}

// Config 配置全局超收容忍（千分比）与提前关闭所需等待秒数。
type Config struct {
	TolerancePermille int64
	CloseWaitSeconds  int64
}

func NewService(cfg Config, initial map[string]map[string]int64) *Service {
	if cfg.TolerancePermille < 0 {
		cfg.TolerancePermille = 0
	}
	if cfg.CloseWaitSeconds < 0 {
		cfg.CloseWaitSeconds = 0
	}
	totals := make(map[string]int64)
	for _, items := range initial {
		for item, qty := range items {
			totals[item] += qty
		}
	}
	return &Service{
		cfg:      cfg,
		clk:      clock.New(),
		store:    inventory.NewStore(initial),
		initial:  totals,
		registry: make(map[string]*Order),
	}
}

func validateLines(lines []Line) error {
	if len(lines) == 0 {
		return fail(ErrInvalidArgument, "order must have at least one line")
	}
	seen := make(map[string]struct{}, len(lines))
	for i, ln := range lines {
		if ln.Item == "" {
			return fail(ErrInvalidArgument, "empty item at line %d", i)
		}
		if ln.Qty <= 0 {
			return fail(ErrInvalidArgument, "non-positive qty %d at line %d", ln.Qty, i)
		}
		if _, dup := seen[ln.Item]; dup {
			return fail(ErrInvalidArgument, "duplicate item %q at line %d", ln.Item, i)
		}
		seen[ln.Item] = struct{}{}
	}
	return nil
}

// Create 创建调拨单：整单在源仓把可用转冻结，全有或全无。
func (s *Service) Create(id, source, dest string, lines []Line, at int64) error {
	if id == "" {
		return fail(ErrInvalidArgument, "empty transfer id")
	}
	if source == "" || dest == "" {
		return fail(ErrInvalidArgument, "empty warehouse")
	}
	if source == dest {
		return fail(ErrInvalidArgument, "source and destination must differ")
	}
	if at < 0 {
		return fail(ErrInvalidArgument, "negative time")
	}
	if err := validateLines(lines); err != nil {
		return err
	}

	// 创建全程独占注册表：任何并发查询都不会看到“冻结到一半”的单。
	s.registryMu.Lock()
	defer s.registryMu.Unlock()

	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	if existing, ok := s.registry[id]; ok {
		return failState(existing.Status, "create")
	}

	reqs := make([]inventory.Req, len(lines))
	for i, ln := range lines {
		reqs[i] = inventory.Req{Warehouse: source, Item: ln.Item, Qty: ln.Qty}
	}
	failIdx, ok := s.store.FreezeAll(reqs)
	if !ok {
		return fail(ErrInsufficientStock, "insufficient stock at line %d (item %q)", failIdx, lines[failIdx].Item)
	}

	order := newOrder(id, source, dest, lines)
	if !s.clk.Advance(at) {
		// 单元锁此刻仍被 FreezeAll 持有（其返回时未释放），直接回滚后释放。
		for _, r := range reqs {
			r.Cell.Apply(r.Qty, -r.Qty)
		}
		s.store.UnlockReqs(reqs)
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	s.registry[id] = order
	s.store.UnlockReqs(reqs)
	return nil
}

// Cancel 取消尚未发出的调拨单并释放冻结。
func (s *Service) Cancel(id string, at int64) error {
	if id == "" || at < 0 {
		return fail(ErrInvalidArgument, "invalid argument")
	}
	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	order, ok := s.getOrder(id)
	if !ok {
		return fail(ErrTransferNotFound, "transfer %q not found", id)
	}

	order.mu.Lock()
	defer order.mu.Unlock()

	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	if order.Status != StatusCreated {
		return failState(order.Status, "cancel")
	}

	reqs := make([]inventory.Req, len(order.lines))
	for i, ln := range order.lines {
		reqs[i] = inventory.Req{Warehouse: order.Source, Item: ln.Item, Qty: ln.Requested}
	}
	locked := s.store.LockCells(reqs)
	committed := false
	defer func() {
		if !committed {
			s.store.UnlockReqs(locked)
		}
	}()

	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	for _, r := range locked {
		a, f := r.Cell.Get()
		if f < r.Qty || a < 0 {
			return fail(ErrInsufficientStock, "internal frozen shortage")
		}
	}
	if !s.clk.Advance(at) {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	for _, r := range locked {
		r.Cell.Apply(r.Qty, -r.Qty)
	}
	order.Status = StatusCancelled
	committed = true
	s.store.UnlockReqs(locked)
	return nil
}

// Ship 发出调拨单：冻结 → 在途，记录发出时刻；只允许一次。
func (s *Service) Ship(id string, at int64) error {
	if id == "" || at < 0 {
		return fail(ErrInvalidArgument, "invalid argument")
	}
	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	order, ok := s.getOrder(id)
	if !ok {
		return fail(ErrTransferNotFound, "transfer %q not found", id)
	}

	order.mu.Lock()
	defer order.mu.Unlock()

	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	if order.Status != StatusCreated {
		return failState(order.Status, "ship")
	}

	reqs := make([]inventory.Req, len(order.lines))
	for i, ln := range order.lines {
		reqs[i] = inventory.Req{Warehouse: order.Source, Item: ln.Item, Qty: ln.Requested}
	}
	locked := s.store.LockCells(reqs)
	committed := false
	defer func() {
		if !committed {
			s.store.UnlockReqs(locked)
		}
	}()

	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	for _, r := range locked {
		_, f := r.Cell.Get()
		if f < r.Qty {
			return fail(ErrInsufficientStock, "internal frozen shortage")
		}
	}
	if !s.clk.Advance(at) {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	for i, r := range locked {
		r.Cell.Apply(0, -r.Qty)
		order.lines[i].Issued = r.Qty
	}
	order.Status = StatusShipped
	order.ShipTime = at
	committed = true
	s.store.UnlockReqs(locked)
	return nil
}

// Receive 对某行分批收货，计入目的仓可用量；受超收容忍额约束。
func (s *Service) Receive(id, item string, qty, at int64) error {
	if id == "" || item == "" || qty <= 0 || at < 0 {
		return fail(ErrInvalidArgument, "invalid argument")
	}
	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	order, ok := s.getOrder(id)
	if !ok {
		return fail(ErrTransferNotFound, "transfer %q not found", id)
	}

	order.mu.Lock()
	defer order.mu.Unlock()

	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	if order.Status != StatusShipped {
		return failState(order.Status, "receive")
	}
	idx, exists := order.line(item)
	if !exists {
		return fail(ErrInvalidArgument, "item %q not in transfer %q", item, id)
	}
	if !order.canReceive(int64(idx), qty, s.cfg.TolerancePermille) {
		ln := order.lines[idx]
		return fail(ErrOverReceipt,
			"over receipt: received=%d qty=%d issued=%d tolerance=%d",
			ln.Received, qty, ln.Issued, order.tolerance(idx, s.cfg.TolerancePermille))
	}

	cell := s.store.LockCell(order.Dest, item)
	committed := false
	defer func() {
		if !committed {
			cell.Unlock()
		}
	}()

	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	if !order.canReceive(int64(idx), qty, s.cfg.TolerancePermille) {
		return fail(ErrOverReceipt, "over receipt after lock contention")
	}
	if !s.clk.Advance(at) {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	cell.Apply(qty, 0)
	order.lines[idx].Received += qty
	committed = true
	cell.Unlock()
	return nil
}

// Close 关闭调拨单并逐行登记短缺/盈余；收齐可提前关闭，否则须等待满时长。
func (s *Service) Close(id string, at int64) error {
	if id == "" || at < 0 {
		return fail(ErrInvalidArgument, "invalid argument")
	}
	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	order, ok := s.getOrder(id)
	if !ok {
		return fail(ErrTransferNotFound, "transfer %q not found", id)
	}

	order.mu.Lock()
	defer order.mu.Unlock()

	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	if order.Status != StatusShipped {
		return failState(order.Status, "close")
	}
	if allowed, why := order.canClose(at, s.cfg.CloseWaitSeconds); !allowed {
		return fail(ErrCloseTooEarly, "%s: ship=%d at=%d wait=%d", why, order.ShipTime, at, s.cfg.CloseWaitSeconds)
	}
	if !s.clk.Advance(at) {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	order.registerDiff()
	order.Status = StatusClosed
	return nil
}

// Recover 对已关闭单的短缺行事后找回，计入目的仓可用量并减少短缺。
func (s *Service) Recover(id, item string, qty, at int64) error {
	if id == "" || item == "" || qty <= 0 || at < 0 {
		return fail(ErrInvalidArgument, "invalid argument")
	}
	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	order, ok := s.getOrder(id)
	if !ok {
		return fail(ErrTransferNotFound, "transfer %q not found", id)
	}

	order.mu.Lock()
	defer order.mu.Unlock()

	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	if order.Status != StatusClosed {
		return failState(order.Status, "recover")
	}
	idx, exists := order.line(item)
	if !exists {
		return fail(ErrInvalidArgument, "item %q not in transfer %q", item, id)
	}
	if order.lines[idx].Shortage == 0 {
		return fail(ErrNoShortage, "no shortage for item %q in transfer %q", item, id)
	}
	if qty > order.lines[idx].Shortage {
		return fail(ErrRecoveryExceed, "recovery exceed: qty=%d shortage=%d", qty, order.lines[idx].Shortage)
	}

	cell := s.store.LockCell(order.Dest, item)
	committed := false
	defer func() {
		if !committed {
			cell.Unlock()
		}
	}()

	if at < s.clk.Peek() {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	if order.lines[idx].Shortage == 0 {
		return fail(ErrNoShortage, "no shortage after lock contention")
	}
	if qty > order.lines[idx].Shortage {
		return fail(ErrRecoveryExceed, "recovery exceed after lock contention")
	}
	if !s.clk.Advance(at) {
		return fail(ErrClockRollback, "clock rollback: at=%d", at)
	}
	cell.Apply(qty, 0)
	order.lines[idx].Shortage -= qty
	committed = true
	cell.Unlock()
	return nil
}

// Stock 查询某仓某商品的可用量与冻结量（只读快照）。
func (s *Service) Stock(warehouse, item string) (available, frozen int64) {
	return s.store.Snapshot(warehouse, item)
}

// OrderLines 查询某单各行的发出量、累计收货量、短缺与盈余（只读快照）。
func (s *Service) OrderLines(id string) ([]LineState, error) {
	order, ok := s.getOrder(id)
	if !ok {
		return nil, fail(ErrTransferNotFound, "transfer %q not found", id)
	}
	order.mu.Lock()
	defer order.mu.Unlock()
	return order.snapshotLines(), nil
}

// OrderStatus 查询某单状态。
func (s *Service) OrderStatus(id string) (Status, error) {
	order, ok := s.getOrder(id)
	if !ok {
		return 0, fail(ErrTransferNotFound, "transfer %q not found", id)
	}
	order.mu.Lock()
	defer order.mu.Unlock()
	return order.Status, nil
}

// VerifyItem 校验某商品的全网守恒：
// 可用 + 冻结 + 在途 + 累计短缺 == 初始化总量 + 累计超收盈余。
func (s *Service) VerifyItem(item string) bool {
	// 与创建互斥：保证注册表、订单与库存对应同一个已提交状态。
	s.registryMu.RLock()
	defer s.registryMu.RUnlock()

	ids := make([]string, 0)
	for id, o := range s.registry {
		if o.hasItem(item) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	orders := make([]*Order, 0, len(ids))
	for _, id := range ids {
		orders = append(orders, s.registry[id])
	}

	for _, o := range orders {
		o.mu.Lock()
	}
	defer func() {
		for _, o := range orders {
			o.mu.Unlock()
		}
	}()

	refs := s.store.ItemCells(item)
	for _, ref := range refs {
		ref.Cell.Lock()
	}
	defer func() {
		for _, ref := range refs {
			ref.Cell.Unlock()
		}
	}()

	var available, frozen, transit, shortage, overage int64
	for _, ref := range refs {
		a, f := ref.Cell.Get()
		if a < 0 || f < 0 {
			return false
		}
		available += a
		frozen += f
	}
	for _, o := range orders {
		idx, ok := o.line(item)
		if !ok {
			continue
		}
		ln := o.lines[idx]
		switch o.Status {
		case StatusShipped:
			if ln.Received < ln.Issued {
				transit += ln.Issued - ln.Received
			} else if ln.Received > ln.Issued {
				overage += ln.Received - ln.Issued
			}
		case StatusClosed:
			shortage += ln.Shortage
			overage += ln.Overage
		}
	}
	return available+frozen+transit+shortage == s.initial[item]+overage
}

// VerifyAll 校验系统当前涉及的全部商品，返回首个不守恒的商品（空串表示全部成立）。
func (s *Service) VerifyAll() string {
	items := make(map[string]struct{})
	for item := range s.initial {
		items[item] = struct{}{}
	}
	for item := range items {
		if !s.VerifyItem(item) {
			return item
		}
	}
	return ""
}

func (o *Order) hasItem(item string) bool {
	_, ok := o.itemIndex[item]
	return ok
}
