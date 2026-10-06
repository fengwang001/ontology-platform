package fulfillment

import (
	"fmt"
	"sync"
)

// weather 天气事件：覆盖左闭右开区间 [start, end)，附带延展量。
type weather struct {
	start int64
	end   int64
	ext   int64
}

// System 履约时效承诺与超时赔付系统。
// 所有公开操作可并发调用，内部以单互斥量串行化，
// 结果等价于某个串行顺序，重放相同操作序列得到相同结果。
type System struct {
	mu         sync.Mutex
	params     Params
	clock      int64
	hasClock   bool
	orders     map[string]*order
	pending    map[string]*order // 已接受、未送达、未取消
	weathers   []weather
	weatherIDs map[string]bool
	ledger     []Verdict
	scans      int64 // 测试钩子：对天气/待办集合的全量扫描计数，裁决路径不得增加
}

// NewSystem 校验构造参数并创建系统。
func NewSystem(p Params) (*System, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &System{
		params:     p.clone(),
		orders:     make(map[string]*order),
		pending:    make(map[string]*order),
		weatherIDs: make(map[string]bool),
	}, nil
}

// checkClock 全局单调时钟：被接受操作的时刻不得早于此前被接受操作的最大时刻。
func (s *System) checkClock(ts int64) error {
	if s.hasClock && ts < s.clock {
		return ErrClockRollback
	}
	return nil
}

func (s *System) advance(ts int64) {
	s.clock, s.hasClock = ts, true
}

// UpdateParams 变更构造参数，仅影响之后接受的订单。
func (s *System) UpdateParams(p Params, ts int64) error {
	if err := p.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(ts); err != nil {
		return err
	}
	s.params = p.clone()
	s.advance(ts)
	return nil
}

// Accept 接受订单并冻结承诺：承诺送达时刻 = 接受时刻 + 承诺时长。
func (s *System) Accept(orderID string, ts int64) error {
	if orderID == "" {
		return fmt.Errorf("%w: empty order id", ErrInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(ts); err != nil {
		return err
	}
	if _, ok := s.orders[orderID]; ok {
		return ErrOrderExists
	}
	o := &order{
		id:          orderID,
		params:      s.params.clone(),
		stage:       StageAccepted,
		acceptTs:    ts,
		origPromise: ts + s.params.PromiseDuration,
	}
	// 已登记天气事件：原始承诺时刻落在区间内即生效一次。
	for _, w := range s.weathers {
		s.scans++
		if w.start <= o.origPromise && o.origPromise < w.end {
			o.extend(w.ext)
		}
	}
	s.orders[orderID] = o
	s.pending[orderID] = o
	s.advance(ts)
	return nil
}

// step 推进五段事件：参数、时钟、存在性、取消、次序依次检查，被拒绝不改任何状态。
func (s *System) step(orderID string, ts int64, expect Stage, apply func(o *order)) error {
	if orderID == "" {
		return fmt.Errorf("%w: empty order id", ErrInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(ts); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return ErrOrderNotFound
	}
	if o.cancelled {
		return ErrOrderCancelled
	}
	if o.stage != expect {
		return ErrEventOrder
	}
	apply(o)
	s.advance(ts)
	return nil
}

// Dispatch 派单。
func (s *System) Dispatch(orderID string, ts int64) error {
	return s.step(orderID, ts, StageAccepted, func(o *order) {
		o.dispatchTs, o.stage = ts, StageDispatched
	})
}

// MealReady 出餐就绪。
func (s *System) MealReady(orderID string, ts int64) error {
	return s.step(orderID, ts, StageDispatched, func(o *order) {
		o.mealReadyTs, o.stage = ts, StageMealReady
	})
}

// Pickup 取货。
func (s *System) Pickup(orderID string, ts int64) error {
	return s.step(orderID, ts, StageMealReady, func(o *order) {
		o.pickupTs, o.stage = ts, StagePickedUp
	})
}

// Deliver 送达。
func (s *System) Deliver(orderID string, ts int64) error {
	return s.step(orderID, ts, StagePickedUp, func(o *order) {
		o.deliverTs, o.stage = ts, StageDelivered
		delete(s.pending, orderID)
	})
}

// Cancel 取消订单。
func (s *System) Cancel(orderID string, ts int64) error {
	if orderID == "" {
		return fmt.Errorf("%w: empty order id", ErrInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(ts); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return ErrOrderNotFound
	}
	if o.cancelled {
		return ErrOrderCancelled
	}
	o.cancelled = true
	delete(s.pending, orderID)
	s.advance(ts)
	return nil
}

// ChangeAddress 用户在送达前改址一次，承诺延后固定延展量。
func (s *System) ChangeAddress(orderID string, ts int64) error {
	if orderID == "" {
		return fmt.Errorf("%w: empty order id", ErrInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(ts); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return ErrOrderNotFound
	}
	if o.cancelled {
		return ErrOrderCancelled
	}
	if o.stage == StageDelivered {
		return ErrEventOrder
	}
	if o.addrChanged {
		return ErrAddrChanged
	}
	o.addrChanged, o.addrChangeTs = true, ts
	o.extend(o.params.AddressExtension)
	s.advance(ts)
	return nil
}

// RegisterWeather 登记天气事件。原始承诺时刻落在 [start, end) 内、
// 且尚未送达的订单获得延展；同一事件对每个订单只生效一次。
func (s *System) RegisterWeather(id string, start, end, ext, ts int64) error {
	if id == "" {
		return fmt.Errorf("%w: empty weather id", ErrInvalidParam)
	}
	if start >= end {
		return fmt.Errorf("%w: weather interval must be non-empty", ErrInvalidParam)
	}
	if ext < 0 {
		return fmt.Errorf("%w: weather extension must be non-negative", ErrInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(ts); err != nil {
		return err
	}
	if s.weatherIDs[id] {
		return fmt.Errorf("%w: duplicate weather id", ErrInvalidParam)
	}
	s.weatherIDs[id] = true
	s.weathers = append(s.weathers, weather{start: start, end: end, ext: ext})
	for _, o := range s.pending {
		s.scans++
		if start <= o.origPromise && o.origPromise < end {
			o.extend(ext)
		}
	}
	s.advance(ts)
	return nil
}

// Claim 用户在申请窗口内申请赔付，被接受即裁决并记账。
func (s *System) Claim(orderID string, ts int64) (Verdict, error) {
	if orderID == "" {
		return Verdict{}, fmt.Errorf("%w: empty order id", ErrInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(ts); err != nil {
		return Verdict{}, err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return Verdict{}, ErrOrderNotFound
	}
	if o.cancelled {
		return Verdict{}, ErrOrderCancelled
	}
	if o.settled {
		return Verdict{}, ErrAlreadyPaid
	}
	if o.stage != StageDelivered {
		return Verdict{}, ErrNotDelivered
	}
	if ts >= o.deliverTs+o.params.ClaimWindow {
		return Verdict{}, ErrWindowExpired
	}
	if o.delay() <= 0 {
		return Verdict{}, ErrNoDelay
	}
	v := adjudicate(o, false)
	s.ledger = append(s.ledger, v)
	s.advance(ts)
	return v, nil
}

// AutoSettle 平台在窗口结束后，对未申请且延误落入最高档的订单自动裁决。
func (s *System) AutoSettle(orderID string, ts int64) (Verdict, error) {
	if orderID == "" {
		return Verdict{}, fmt.Errorf("%w: empty order id", ErrInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(ts); err != nil {
		return Verdict{}, err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return Verdict{}, ErrOrderNotFound
	}
	if o.cancelled {
		return Verdict{}, ErrOrderCancelled
	}
	if o.settled {
		return Verdict{}, ErrAlreadyPaid
	}
	if o.stage != StageDelivered {
		return Verdict{}, ErrNotDelivered
	}
	if ts < o.deliverTs+o.params.ClaimWindow {
		return Verdict{}, ErrWindowNotEnded
	}
	if o.delay() <= 0 {
		return Verdict{}, ErrNoDelay
	}
	if o.delay() < o.params.Tiers[len(o.params.Tiers)-1].Threshold {
		return Verdict{}, ErrNotTopTier
	}
	v := adjudicate(o, true)
	s.ledger = append(s.ledger, v)
	s.advance(ts)
	return v, nil
}

// Snapshot 订单状态快照，用于查询与测试。
type Snapshot struct {
	Stage       Stage
	OrigPromise int64
	Promise     int64
	Extension   int64
	Delay       int64
	Cancelled   bool
	Settled     bool
}

// Snapshot 返回订单状态快照。
func (s *System) Snapshot(orderID string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return Snapshot{}, false
	}
	snap := Snapshot{
		Stage:       o.stage,
		OrigPromise: o.origPromise,
		Promise:     o.promise(),
		Extension:   o.extension,
		Cancelled:   o.cancelled,
		Settled:     o.settled,
	}
	if o.stage == StageDelivered {
		snap.Delay = o.delay()
	}
	return snap, true
}

// VerdictOf 返回已裁决订单的裁决结果。
func (s *System) VerdictOf(orderID string) (Verdict, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok || !o.settled {
		return Verdict{}, false
	}
	return o.verdict, true
}

// Ledger 返回赔付账目副本。
func (s *System) Ledger() []Verdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Verdict(nil), s.ledger...)
}
