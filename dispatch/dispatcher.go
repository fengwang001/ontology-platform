package dispatch

import "sync"

// DispatchResult 为派单输出：确定性选中的骑手与插入位置。
type DispatchResult struct {
	RiderID      RiderID
	PickupIndex  int
	DropoffIndex int
	Arrival      int64 // 新订单送达的推定到达时刻
	OldExtra     int64
	ExtraTime    int64
}

// CompleteResult 为停靠完成输出。
type CompleteResult struct {
	Stop   Stop
	Arrive int64
	Depart int64
}

// Config 为构造参数。
type Config struct {
	PickupDwell int64
	DropDwell   int64
	MaxDetour   int64 // 单个在途订单单次插入允许的送达延后上限
	// RegionOf 把取货点（商家位置）映射到其所属服务区域；
	// 为 nil 时取货点自身即为区域标识。区域只用于骑手匹配，与耗时拓扑无关。
	RegionOf func(Location) Location
}

type orderRec struct {
	order  Order
	status OrderStatus
	rider  RiderID
}

// Dispatcher 是并发安全的派单协调器。单一互斥锁把所有接受/拒绝判定串行化，
// 因而并发结果等价于某一全序串行顺序；容量校验与序列变更在同一临界区完成，
// 同一骑手在并发派单下持有订单数不会超过载量。
type Dispatcher struct {
	mu     sync.Mutex
	tt     TravelTimeSource
	cfg    Config
	clock  int64
	orders map[OrderID]*orderRec
	riders map[RiderID]*Rider
	// 区域索引：候选筛选只遍历目标取货区域内的在线骑手，
	// 开销不随其他区域骑手数量增长。
	region map[Location]map[RiderID]*Rider
}

// NewDispatcher 创建协调器。
func NewDispatcher(tt TravelTimeSource, cfg Config) *Dispatcher {
	if tt == nil {
		tt = TravelMap{}
	}
	if cfg.RegionOf == nil {
		cfg.RegionOf = func(l Location) Location { return l }
	}
	return &Dispatcher{
		tt:     tt,
		cfg:    cfg,
		orders: make(map[OrderID]*orderRec),
		riders: make(map[RiderID]*Rider),
		region: make(map[Location]map[RiderID]*Rider),
	}
}

// checkClock 必须在通过参数校验后、任何状态变更前调用。
func (d *Dispatcher) checkClock(at int64) error {
	if at < d.clock {
		return ErrClockRollback
	}
	return nil
}

func (d *Dispatcher) advance(at int64) {
	if at > d.clock {
		d.clock = at
	}
}

// RegisterRider 注册一名骑手（按提交快照上线）。
func (d *Dispatcher) RegisterRider(at int64, r Rider) error {
	if r.ID == "" || r.Capacity <= 0 || r.Pos == "" {
		return ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkClock(at); err != nil {
		return err
	}
	if _, exists := d.riders[r.ID]; exists {
		return ErrInvalidArgument
	}
	stored := cloneRider(&r)
	stored.Online = true
	d.riders[r.ID] = stored
	d.indexRegion(stored)
	d.advance(at)
	return nil
}

// SetOnline 切换骑手在线状态。
func (d *Dispatcher) SetOnline(at int64, id RiderID, online bool) error {
	if id == "" {
		return ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkClock(at); err != nil {
		return err
	}
	r, ok := d.riders[id]
	if !ok {
		return ErrRiderNotFound
	}
	if r.Online != online {
		d.deindexRegion(r)
		r.Online = online
		d.indexRegion(r)
	}
	d.advance(at)
	return nil
}

// SubmitOrder 登记一笔新订单。
func (d *Dispatcher) SubmitOrder(at int64, o Order) error {
	if o.ID == "" || o.Pickup == "" || o.Dropoff == "" || o.Promise < o.ReadyAt {
		return ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkClock(at); err != nil {
		return err
	}
	if _, exists := d.orders[o.ID]; exists {
		return ErrOrderExists
	}
	d.orders[o.ID] = &orderRec{order: o, status: OrderNew}
	d.advance(at)
	return nil
}

// DispatchOrder 将已登记的新订单并入最优可行骑手序列。
func (d *Dispatcher) DispatchOrder(at int64, id OrderID) (DispatchResult, error) {
	if id == "" {
		return DispatchResult{}, ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkClock(at); err != nil {
		return DispatchResult{}, err
	}
	rec, ok := d.orders[id]
	if !ok {
		return DispatchResult{}, ErrOrderNotFound
	}
	if rec.status != OrderNew {
		return DispatchResult{}, ErrOrderAlreadyAssigned
	}

	promiseOf := func(oid OrderID) int64 { return d.orders[oid].order.Promise }

	// 只收集同区骑手；其他区域骑手不参与枚举。
	pickupRegion := d.cfg.RegionOf(rec.order.Pickup)
	group := d.region[pickupRegion]
	type scored struct {
		r *Rider
		c Candidate
	}
	var best scored
	haveBest := false
	anyCapacity := false
	anyNewPromise := false

	for _, r := range group {
		if !r.Online || r.Region != pickupRegion {
			continue
		}
		held := countHeld(r.Pending)
		if held+1 <= r.Capacity {
			anyCapacity = true
		} else {
			continue
		}
		c := EvaluateRider(d.tt, r, rec.order, d.cfg.PickupDwell, d.cfg.DropDwell, d.cfg.MaxDetour, promiseOf)
		if c.NewPromiseOK {
			anyNewPromise = true
		}
		if !c.HasBest {
			continue
		}
		if !haveBest || betterRider(c, best.c, r.ID, best.r.ID) {
			best = scored{r: r, c: c}
			haveBest = true
		}
	}

	if !haveBest {
		switch {
		case !anyCapacity:
			return DispatchResult{}, ErrNoRiderRegionCapacity
		case !anyNewPromise:
			return DispatchResult{}, ErrNewOrderPromise
		default:
			return DispatchResult{}, ErrExistingViolation
		}
	}

	// 提交：物理写入两个停靠并更新订单归属/状态。
	r := best.r
	ins := best.c.Best
	pickup := Stop{OrderID: rec.order.ID, Kind: StopPickup, At: rec.order.Pickup, ReadyAt: rec.order.ReadyAt, Dwell: d.cfg.PickupDwell}
	drop := Stop{OrderID: rec.order.ID, Kind: StopDropoff, At: rec.order.Dropoff, Dwell: d.cfg.DropDwell}
	next := make([]Stop, 0, len(r.Pending)+2)
	next = append(next, r.Pending[:ins.PickupIndex]...)
	next = append(next, pickup)
	next = append(next, r.Pending[ins.PickupIndex:ins.DropoffIndex-1]...)
	next = append(next, drop)
	next = append(next, r.Pending[ins.DropoffIndex-1:]...)
	r.Pending = next
	rec.status = OrderAssigned
	rec.rider = r.ID
	d.advance(at)

	return DispatchResult{
		RiderID:      r.ID,
		PickupIndex:  ins.PickupIndex,
		DropoffIndex: ins.DropoffIndex,
		Arrival:      ins.NewArrival,
		OldExtra:     ins.OldExtra,
		ExtraTime:    ins.ExtraTime,
	}, nil
}

// betterRider 实现骑手间确定性偏好：
// 总耗时增量最小 -> 当前持有订单数更少 -> 骑手标识字典序更小。
func betterRider(a, b Candidate, idA, idB RiderID) bool {
	if a.Best.ExtraTime != b.Best.ExtraTime {
		return a.Best.ExtraTime < b.Best.ExtraTime
	}
	if a.Held != b.Held {
		return a.Held < b.Held
	}
	return idA < idB
}

// CompleteStop 报告骑手序列首个停靠完成。报告时刻成为实际离开时刻，
// 取货早于就绪时视为在该停靠等待至就绪后离开。
func (d *Dispatcher) CompleteStop(at int64, riderID RiderID, orderID OrderID, kind StopKind) (CompleteResult, error) {
	if riderID == "" || orderID == "" || (kind != StopPickup && kind != StopDropoff) {
		return CompleteResult{}, ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkClock(at); err != nil {
		return CompleteResult{}, err
	}
	r, ok := d.riders[riderID]
	if !ok {
		return CompleteResult{}, ErrRiderNotFound
	}
	if !r.Online {
		return CompleteResult{}, ErrRiderOffline
	}
	rec, ok := d.orders[orderID]
	if !ok {
		return CompleteResult{}, ErrOrderNotFound
	}
	if rec.rider != riderID {
		return CompleteResult{}, ErrOrderNotAtRider
	}
	if len(r.Pending) == 0 {
		return CompleteResult{}, ErrStopOutOfOrder
	}
	head := r.Pending[0]
	if head.OrderID != orderID || head.Kind != kind {
		return CompleteResult{}, ErrStopOutOfOrder
	}
	switch kind {
	case StopPickup:
		if rec.status != OrderAssigned {
			return CompleteResult{}, ErrStopOutOfOrder
		}
	case StopDropoff:
		if rec.status != OrderPicked {
			return CompleteResult{}, ErrStopOutOfOrder
		}
	}

	// 以当前锚点重新推定首停靠到达，离开 = max(报告时刻, 到达[, 就绪]) + 0。
	travel, edgeOK := d.tt.Travel(r.Pos, head.At)
	if !edgeOK {
		return CompleteResult{}, ErrInvalidArgument
	}
	arrive := r.DepartedAt + travel
	depart := at
	if depart < arrive {
		depart = arrive
	}
	if kind == StopPickup && depart < head.ReadyAt {
		depart = head.ReadyAt
	}

	// 弹出首停靠，锚点前移到该停靠位置与其实际离开时刻（次序不变）。
	r.Pos = head.At
	r.DepartedAt = depart
	r.Pending = append([]Stop(nil), r.Pending[1:]...)
	if kind == StopPickup {
		rec.status = OrderPicked
	} else {
		rec.status = OrderDelivered
	}
	d.advance(at)

	return CompleteResult{Stop: head, Arrive: arrive, Depart: depart}, nil
}

// CancelOrder 取消尚未取货的在途订单。移除其两个停靠并重算推定；
// 若任何在途订单送达会因此变晚则拒绝（不变更是硬性保证）。
func (d *Dispatcher) CancelOrder(at int64, id OrderID) error {
	if id == "" {
		return ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkClock(at); err != nil {
		return err
	}
	rec, ok := d.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	if rec.status == OrderNew {
		return ErrOrderNotAssigned
	}
	if rec.status == OrderCancelled || rec.status == OrderDelivered {
		return ErrOrderNotAssigned
	}
	if rec.status == OrderPicked {
		return ErrOrderAlreadyPicked
	}
	r := d.riders[rec.rider]

	before, ok := EstimateSchedule(d.tt, r.Pos, r.DepartedAt, r.Pending)
	if !ok {
		return ErrInvalidArgument
	}
	next := make([]Stop, 0, len(r.Pending))
	for i := range r.Pending {
		if r.Pending[i].OrderID != id {
			next = append(next, r.Pending[i])
		}
	}
	after, ok := EstimateSchedule(d.tt, r.Pos, r.DepartedAt, next)
	if !ok {
		return ErrInvalidArgument
	}
	// 对比每个保留送达停靠的到达时刻，只允许不变或更早。
	ai := 0
	for bi := range r.Pending {
		if r.Pending[bi].OrderID == id {
			continue
		}
		if r.Pending[bi].Kind == StopDropoff && after[ai].Arrive > before[bi].Arrive {
			return ErrCancelWouldDelay
		}
		ai++
	}

	r.Pending = next
	rec.status = OrderCancelled
	rec.rider = ""
	d.advance(at)
	return nil
}

func (d *Dispatcher) indexRegion(r *Rider) {
	if !r.Online {
		return
	}
	set := d.region[r.Region]
	if set == nil {
		set = make(map[RiderID]*Rider)
		d.region[r.Region] = set
	}
	set[r.ID] = r
}

func (d *Dispatcher) deindexRegion(r *Rider) {
	if set := d.region[r.Region]; set != nil {
		delete(set, r.ID)
		if len(set) == 0 {
			delete(d.region, r.Region)
		}
	}
}

func cloneRider(r *Rider) *Rider {
	cp := *r
	if r.Pending != nil {
		cp.Pending = append([]Stop(nil), r.Pending...)
	}
	return &cp
}

// Now 返回已接受操作的最大时刻（全局单调时钟）。
func (d *Dispatcher) Now() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.clock
}

// RiderSnapshot 返回骑手当前状态的拷贝。
func (d *Dispatcher) RiderSnapshot(id RiderID) (Rider, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.riders[id]
	if !ok {
		return Rider{}, false
	}
	return *cloneRider(r), true
}

// OrderStatus 返回订单当前状态。
func (d *Dispatcher) OrderStatus(id OrderID) (OrderStatus, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.orders[id]
	if !ok {
		return 0, false
	}
	return rec.status, true
}

// RiderSchedule 返回骑手当前未完成序列的推定时刻。
func (d *Dispatcher) RiderSchedule(id RiderID) (Schedule, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.riders[id]
	if !ok {
		return nil, false
	}
	s, ok := EstimateSchedule(d.tt, r.Pos, r.DepartedAt, r.Pending)
	return s, ok
}
