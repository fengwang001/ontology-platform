package fencing

import "sync"

// System 是配送范围围栏与动态收缩系统的门面。
// 所有操作在全局互斥锁下串行化，天然满足线性一致；
// clock 为已接受操作的最大时刻，全局单调不减。
type System struct {
	mu        sync.Mutex
	clock     int64
	regions   map[string]*regionState
	merchants map[string]*merchant
	orders    map[string]*order
	events    map[string]*shrinkEvent // 全部已登记事件的索引（含已结束），用于终止与存在性判定
}

func NewSystem() *System {
	return &System{
		regions:   make(map[string]*regionState),
		merchants: make(map[string]*merchant),
		orders:    make(map[string]*order),
		events:    make(map[string]*shrinkEvent),
	}
}

func (s *System) region(id string) *regionState {
	r, ok := s.regions[id]
	if !ok {
		r = newRegionState()
		s.regions[id] = r
	}
	return r
}

// checkClock 拒绝时钟回退；被拒绝的操作不推进时钟。
func (s *System) checkClock(now int64) error {
	if now < s.clock {
		return newErr(ErrKindClockRollback, "time %d is before max accepted time %d", now, s.clock)
	}
	return nil
}

// effectiveLevelAt 返回商家在时刻 t 的有效等级：
// 覆盖 t 的平台事件等级最大值与商家等级的较大者。
// 区域结构只按已提交时钟（s.clock）做破坏式清理，对 t 的求值是只读的。
func (s *System) effectiveLevelAt(m *merchant, t int64) int {
	level := m.level
	if r, ok := s.regions[m.region]; ok {
		r.prune(s.clock)
		if top := r.maxLevelAt(t); top > level {
			level = top
		}
	}
	return level
}

// RegisterMerchant 登记商家及其基础范围；区域按名隐式创建。
func (s *System) RegisterMerchant(now int64, id, region string, cells []Cell) error {
	if id == "" || region == "" {
		return newErr(ErrKindInvalidParam, "merchant and region ids must be non-empty")
	}
	if now < 0 {
		return newErr(ErrKindInvalidParam, "negative time %d", now)
	}
	m, err := newMerchant(id, region, cells)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.merchants[id]; dup {
		return newErr(ErrKindInvalidParam, "merchant %q already registered", id)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.merchants[id] = m
	s.region(region)
	s.clock = now
	return nil
}

// RegisterEvent 在区域登记平台收缩事件，区间 [start, end) 左闭右开。
func (s *System) RegisterEvent(now int64, id, region string, start, end int64, level int) error {
	if id == "" || region == "" {
		return newErr(ErrKindInvalidParam, "event and region ids must be non-empty")
	}
	if now < 0 || start < 0 {
		return newErr(ErrKindInvalidParam, "negative time (now=%d, start=%d)", now, start)
	}
	if end <= start {
		return newErr(ErrKindInvalidParam, "event %q: end %d must be greater than start %d", id, end, start)
	}
	if level < 0 {
		return newErr(ErrKindInvalidParam, "event %q: negative level %d", id, level)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.events[id]; dup {
		return newErr(ErrKindInvalidParam, "event %q already registered", id)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	ev := &shrinkEvent{id: id, region: region, start: start, end: end, level: level}
	s.events[id] = ev
	s.clock = now
	s.region(region).add(ev, now)
	return nil
}

// TerminateEvent 提前终止事件；终止时刻不得早于事件起点。
func (s *System) TerminateEvent(now int64, id string) error {
	if id == "" {
		return newErr(ErrKindInvalidParam, "event id must be non-empty")
	}
	if now < 0 {
		return newErr(ErrKindInvalidParam, "negative time %d", now)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	ev, ok := s.events[id]
	if !ok {
		return newErr(ErrKindEventNotFound, "event %q not found", id)
	}
	if ev.terminated {
		return newErr(ErrKindEventTerminated, "event %q already terminated", id)
	}
	if now < ev.start {
		return newErr(ErrKindInvalidParam, "termination time %d is before event %q start %d", now, id, ev.start)
	}
	ev.terminated = true
	ev.terminatedAt = now
	s.clock = now
	if r, ok := s.regions[ev.region]; ok {
		r.prune(now) // now 已提交，可安全清理（含本事件）
	}
	return nil
}

// SetMerchantLevel 设置商家自设收缩等级，立即生效且无截止；可重复设置。
func (s *System) SetMerchantLevel(now int64, merchantID string, level int) error {
	if merchantID == "" {
		return newErr(ErrKindInvalidParam, "merchant id must be non-empty")
	}
	if level < 0 {
		return newErr(ErrKindInvalidParam, "negative level %d", level)
	}
	if now < 0 {
		return newErr(ErrKindInvalidParam, "negative time %d", now)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	m, ok := s.merchants[merchantID]
	if !ok {
		return newErr(ErrKindMerchantNotFound, "merchant %q not found", merchantID)
	}
	m.level = level
	s.clock = now
	if r, ok := s.regions[m.region]; ok {
		r.prune(now)
	}
	return nil
}

// PlaceOrder 下单：校验送达单元当前可达，成功后进入待接单。
func (s *System) PlaceOrder(now int64, orderID, merchantID, cellID string) error {
	if orderID == "" || merchantID == "" || cellID == "" {
		return newErr(ErrKindInvalidParam, "order, merchant and cell ids must be non-empty")
	}
	if now < 0 {
		return newErr(ErrKindInvalidParam, "negative time %d", now)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.orders[orderID]; dup {
		return newErr(ErrKindInvalidParam, "order %q already exists", orderID)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	m, ok := s.merchants[merchantID]
	if !ok {
		return newErr(ErrKindMerchantNotFound, "merchant %q not found", merchantID)
	}
	switch m.reachability(cellID, s.effectiveLevelAt(m, now)) {
	case UnreachablePermanent:
		return newErr(ErrKindPermanentOutOfRange, "cell %q is not in merchant %q base range", cellID, merchantID)
	case UnreachableTemporary:
		return newErr(ErrKindTemporarilyUnreachable, "cell %q is shrunk out of merchant %q range", cellID, merchantID)
	}
	s.orders[orderID] = &order{id: orderID, merchantID: merchantID, cell: cellID, state: stPending}
	s.clock = now
	return nil
}

// AcceptOrder 接单：再次校验可达；若因收缩变为不可达，订单自动转为
// 因收缩取消的终态（有可见副作用，故推进时钟），并向调用方报暂时不可达。
func (s *System) AcceptOrder(now int64, orderID string) error {
	if orderID == "" {
		return newErr(ErrKindInvalidParam, "order id must be non-empty")
	}
	if now < 0 {
		return newErr(ErrKindInvalidParam, "negative time %d", now)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return newErr(ErrKindOrderNotFound, "order %q not found", orderID)
	}
	switch o.state {
	case stAccepted:
		return newErr(ErrKindOrderAccepted, "order %q already accepted", orderID)
	case stDelivered:
		return newErr(ErrKindOrderDelivered, "order %q already delivered", orderID)
	case stCancelledShrink:
		return newErr(ErrKindOrderCancelled, "order %q already cancelled", orderID)
	}
	m := s.merchants[o.merchantID]
	level := s.effectiveLevelAt(m, now)
	ring, ok := m.cells[o.cell]
	if !ok {
		panic("fencing: invariant violated: order cell left merchant base range")
	}
	if ring > m.effectiveRadius(level) {
		o.state = stCancelledShrink
		s.clock = now
		return newErr(ErrKindTemporarilyUnreachable, "order %q auto-cancelled: cell %q shrunk out of range", orderID, o.cell)
	}
	o.state = stAccepted
	s.clock = now
	return nil
}

// DeliverOrder 送达；已接单订单不受之后任何收缩影响。
func (s *System) DeliverOrder(now int64, orderID string) error {
	if orderID == "" {
		return newErr(ErrKindInvalidParam, "order id must be non-empty")
	}
	if now < 0 {
		return newErr(ErrKindInvalidParam, "negative time %d", now)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return newErr(ErrKindOrderNotFound, "order %q not found", orderID)
	}
	switch o.state {
	case stPending:
		return newErr(ErrKindOrderNotAccepted, "order %q is not accepted yet", orderID)
	case stDelivered:
		return newErr(ErrKindOrderDelivered, "order %q already delivered", orderID)
	case stCancelledShrink:
		return newErr(ErrKindOrderCancelled, "order %q already cancelled", orderID)
	}
	o.state = stDelivered
	s.clock = now
	return nil
}

// ChangeAddress 送达前允许改址一次，按当前有效等级校验；
// 不可达则拒绝且订单保持原地址，被拒绝时不改任何状态。
func (s *System) ChangeAddress(now int64, orderID, cellID string) error {
	if orderID == "" || cellID == "" {
		return newErr(ErrKindInvalidParam, "order and cell ids must be non-empty")
	}
	if now < 0 {
		return newErr(ErrKindInvalidParam, "negative time %d", now)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return newErr(ErrKindOrderNotFound, "order %q not found", orderID)
	}
	switch o.state {
	case stDelivered:
		return newErr(ErrKindOrderDelivered, "order %q already delivered", orderID)
	case stCancelledShrink:
		return newErr(ErrKindOrderCancelled, "order %q already cancelled", orderID)
	}
	if o.changed {
		return newErr(ErrKindAddressChanged, "order %q address already changed once", orderID)
	}
	m := s.merchants[o.merchantID]
	switch m.reachability(cellID, s.effectiveLevelAt(m, now)) {
	case UnreachablePermanent:
		return newErr(ErrKindPermanentOutOfRange, "cell %q is not in merchant %q base range", cellID, o.merchantID)
	case UnreachableTemporary:
		return newErr(ErrKindTemporarilyUnreachable, "cell %q is shrunk out of merchant %q range", cellID, o.merchantID)
	}
	o.cell = cellID
	o.changed = true
	s.clock = now
	return nil
}

// Reachable 只读可达查询，按当前系统时钟求值，不改任何状态。
func (s *System) Reachable(merchantID, cellID string) (Reachability, error) {
	if merchantID == "" || cellID == "" {
		return 0, newErr(ErrKindInvalidParam, "merchant and cell ids must be non-empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.merchants[merchantID]
	if !ok {
		return 0, newErr(ErrKindMerchantNotFound, "merchant %q not found", merchantID)
	}
	return m.reachability(cellID, s.effectiveLevelAt(m, s.clock)), nil
}

// RecoveryKind 是下一次恢复时刻查询的结果类别。
type RecoveryKind int

const (
	RecoveryNow       RecoveryKind = iota // 当前可达
	RecoveryAt                            // 在 At 时刻最早重新可达
	RecoveryNone                          // 商家等级本身已使其不可达，无恢复时刻
	RecoveryPermanent                     // 单元不在基础范围内，永久不可达
)

func (k RecoveryKind) String() string {
	switch k {
	case RecoveryNow:
		return "reachable_now"
	case RecoveryAt:
		return "recovery_at"
	case RecoveryNone:
		return "no_recovery"
	case RecoveryPermanent:
		return "permanent_out_of_range"
	}
	return "unknown"
}

// Recovery 是下一次恢复时刻查询的结果。
type Recovery struct {
	Kind RecoveryKind
	At   int64 // 仅 Kind == RecoveryAt 时有效
}

// NextRecovery 只读查询：对暂时不可达的单元，给出在当前已登记事件按计划
// 结束的前提下最早重新可达的时刻；不改任何状态。
func (s *System) NextRecovery(merchantID, cellID string) (Recovery, error) {
	if merchantID == "" || cellID == "" {
		return Recovery{}, newErr(ErrKindInvalidParam, "merchant and cell ids must be non-empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.merchants[merchantID]
	if !ok {
		return Recovery{}, newErr(ErrKindMerchantNotFound, "merchant %q not found", merchantID)
	}
	ring, ok := m.cells[cellID]
	if !ok {
		return Recovery{Kind: RecoveryPermanent}, nil
	}
	threshold := m.baseRadius - ring // 可容忍的最大有效等级
	if m.level > threshold {
		return Recovery{Kind: RecoveryNone}, nil
	}
	if s.effectiveLevelAt(m, s.clock) <= threshold {
		return Recovery{Kind: RecoveryNow, At: s.clock}, nil
	}
	r := s.regions[m.region]
	return Recovery{Kind: RecoveryAt, At: r.recoveryFrom(s.clock, threshold)}, nil
}
