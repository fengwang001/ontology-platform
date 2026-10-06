package dispatch

import "sort"

// NaiveModel 是独立实现的穷举朴素对照模型：
// 自行维护订单/骑手/时钟状态，每次派单对同区全部有余量在线骑手
// 暴力枚举所有 (pi,di) 插入位置；不调用 Dispatcher、enumerate、EvaluateRider
// 或任何索引结构，只共用基础类型与 EstimateSchedule 的推定规则。
type NaiveModel struct {
	tt     TravelTimeSource
	cfg    Config
	clock  int64
	orders map[OrderID]naiveOrder
	riders map[RiderID]*Rider
}

type naiveOrder struct {
	o      Order
	status OrderStatus
	rider  RiderID
}

// NewNaiveModel 创建朴素模型。
func NewNaiveModel(tt TravelTimeSource, cfg Config) *NaiveModel {
	if cfg.RegionOf == nil {
		cfg.RegionOf = func(l Location) Location { return l }
	}
	return &NaiveModel{
		tt:     tt,
		cfg:    cfg,
		orders: make(map[OrderID]naiveOrder),
		riders: make(map[RiderID]*Rider),
	}
}

// NaiveOp 为重放日志中的一步操作（判别式联合）。
type NaiveOp struct {
	Kind     string // register|online|submit|dispatch|complete|cancel
	At       int64
	Order    Order
	OrderID  OrderID
	Rider    Rider
	RiderID  RiderID
	Online   bool
	StopKind StopKind
}

// NaiveOutcome 为一步重放的输出。
type NaiveOutcome struct {
	OK       bool
	Err      error
	Dispatch DispatchResult
}

// Replay 重放整条操作序列，返回每一步结果。
func (m *NaiveModel) Replay(ops []NaiveOp) []NaiveOutcome {
	out := make([]NaiveOutcome, len(ops))
	for i, op := range ops {
		out[i] = m.apply(op)
	}
	return out
}

func (m *NaiveModel) apply(op NaiveOp) NaiveOutcome {
	switch op.Kind {
	case "register":
		return m.okErr(m.register(op))
	case "online":
		return m.okErr(m.setOnline(op))
	case "submit":
		return m.okErr(m.submit(op))
	case "dispatch":
		res, err := m.dispatch(op)
		return NaiveOutcome{OK: err == nil, Err: err, Dispatch: res}
	case "complete":
		return m.okErr(m.complete(op))
	case "cancel":
		return m.okErr(m.cancel(op))
	default:
		return NaiveOutcome{Err: ErrInvalidArgument}
	}
}

func (m *NaiveModel) okErr(err error) NaiveOutcome {
	return NaiveOutcome{OK: err == nil, Err: err}
}

func (m *NaiveModel) rollback(at int64) error {
	if at < m.clock {
		return ErrClockRollback
	}
	return nil
}

func (m *NaiveModel) register(op NaiveOp) error {
	r := op.Rider
	if r.ID == "" || r.Capacity <= 0 || r.Pos == "" {
		return ErrInvalidArgument
	}
	if err := m.rollback(op.At); err != nil {
		return err
	}
	if _, e := m.riders[r.ID]; e {
		return ErrInvalidArgument
	}
	cp := r
	cp.Online = true
	m.riders[r.ID] = &cp
	m.clock = op.At
	return nil
}

func (m *NaiveModel) setOnline(op NaiveOp) error {
	if op.RiderID == "" {
		return ErrInvalidArgument
	}
	if err := m.rollback(op.At); err != nil {
		return err
	}
	r, ok := m.riders[op.RiderID]
	if !ok {
		return ErrRiderNotFound
	}
	r.Online = op.Online
	m.clock = op.At
	return nil
}

func (m *NaiveModel) submit(op NaiveOp) error {
	o := op.Order
	if o.ID == "" || o.Pickup == "" || o.Dropoff == "" || o.Promise < o.ReadyAt {
		return ErrInvalidArgument
	}
	if err := m.rollback(op.At); err != nil {
		return err
	}
	if _, e := m.orders[o.ID]; e {
		return ErrOrderExists
	}
	m.orders[o.ID] = naiveOrder{o: o, status: OrderNew}
	m.clock = op.At
	return nil
}

type naivePos struct {
	rider                           RiderID
	pi, di                          int
	newArrival, oldExtra, extraTime int64
	held                            int
	stops                           []Stop
}

func (m *NaiveModel) dispatch(op NaiveOp) (DispatchResult, error) {
	if op.OrderID == "" {
		return DispatchResult{}, ErrInvalidArgument
	}
	if err := m.rollback(op.At); err != nil {
		return DispatchResult{}, err
	}
	rec, ok := m.orders[op.OrderID]
	if !ok {
		return DispatchResult{}, ErrOrderNotFound
	}
	if rec.status != OrderNew {
		return DispatchResult{}, ErrOrderAlreadyAssigned
	}

	ids := make([]RiderID, 0)
	helds := make(map[RiderID]int)
	for id, r := range m.riders {
		if r.Online && r.Region == m.cfg.RegionOf(rec.o.Pickup) && countHeld(r.Pending)+1 <= r.Capacity {
			ids = append(ids, id)
			helds[id] = countHeld(r.Pending)
		}
	}
	sort.Strings(ids)

	// 每个骑手先按“位置规则”选出其唯一最优位置，再在骑手间比较；
	// 两级规则不可扁平混排，否则会拿某骑手非最优位置的增量参与骑手比较。
	bestByRider := make(map[RiderID]*naivePos)
	anyCapacity := len(ids) > 0
	anyNewPromise := false

	for _, id := range ids {
		r := m.riders[id]
		oldSched, eok := EstimateSchedule(m.tt, r.Pos, r.DepartedAt, r.Pending)
		if !eok {
			continue
		}
		oldDur := scheduleDuration(r.DepartedAt, oldSched)
		n := len(r.Pending)
		pickup := Stop{OrderID: rec.o.ID, Kind: StopPickup, At: rec.o.Pickup, ReadyAt: rec.o.ReadyAt, Dwell: m.cfg.PickupDwell}
		drop := Stop{OrderID: rec.o.ID, Kind: StopDropoff, At: rec.o.Dropoff, Dwell: m.cfg.DropDwell}

		for pi := 0; pi <= n; pi++ {
			for di := pi + 1; di <= n+1; di++ {
				cand := make([]Stop, 0, n+2)
				cand = append(cand, r.Pending[:pi]...)
				cand = append(cand, pickup)
				cand = append(cand, r.Pending[pi:di-1]...)
				cand = append(cand, drop)
				cand = append(cand, r.Pending[di-1:]...)
				sched, ok := EstimateSchedule(m.tt, r.Pos, r.DepartedAt, cand)
				if !ok {
					continue
				}
				newArr := sched[di].Arrive
				if newArr <= rec.o.Promise {
					anyNewPromise = true
				} else {
					continue
				}
				feasible := true
				var oldExtra int64
				for k := range r.Pending {
					if r.Pending[k].Kind != StopDropoff {
						continue
					}
					oid := r.Pending[k].OrderID
					ni := mappedIndex(k, pi, di)
					if sched[ni].Arrive > m.orders[oid].o.Promise {
						feasible = false
						break
					}
					delay := sched[ni].Arrive - oldSched[k].Arrive
					if delay > m.cfg.MaxDetour {
						feasible = false
						break
					}
					if delay > 0 {
						oldExtra += delay
					}
				}
				if !feasible {
					continue
				}
				pos := &naivePos{
					rider: id, pi: pi, di: di, newArrival: newArr,
					oldExtra: oldExtra, extraTime: scheduleDuration(r.DepartedAt, sched) - oldDur,
					held: helds[id], stops: cand,
				}
				if cur := bestByRider[id]; cur == nil || m.betterPosition(pos, cur) {
					bestByRider[id] = pos
				}
			}
		}
	}

	if len(bestByRider) == 0 {
		switch {
		case !anyCapacity:
			return DispatchResult{}, ErrNoRiderRegionCapacity
		case !anyNewPromise:
			return DispatchResult{}, ErrNewOrderPromise
		default:
			return DispatchResult{}, ErrExistingViolation
		}
	}

	var best *naivePos
	for _, id := range ids {
		pos, ok := bestByRider[id]
		if !ok {
			continue
		}
		if best == nil || m.betterRider(pos, best) {
			best = pos
		}
	}

	r := m.riders[best.rider]
	r.Pending = best.stops
	rec.status = OrderAssigned
	rec.rider = best.rider
	m.orders[op.OrderID] = rec
	m.clock = op.At
	return DispatchResult{
		RiderID: best.rider, PickupIndex: best.pi, DropoffIndex: best.di,
		Arrival: best.newArrival, OldExtra: best.oldExtra, ExtraTime: best.extraTime,
	}, nil
}

// betterPosition 在同一骑手内按（在途总延后, 新单到达, pi, di）比较。
func (m *NaiveModel) betterPosition(a, b *naivePos) bool {
	if a.oldExtra != b.oldExtra {
		return a.oldExtra < b.oldExtra
	}
	if a.newArrival != b.newArrival {
		return a.newArrival < b.newArrival
	}
	if a.pi != b.pi {
		return a.pi < b.pi
	}
	return a.di < b.di
}

// betterRider 在各骑手的最优位置之间按（总耗时增量, 持有数, ID）比较。
func (m *NaiveModel) betterRider(a, b *naivePos) bool {
	if a.extraTime != b.extraTime {
		return a.extraTime < b.extraTime
	}
	if a.held != b.held {
		return a.held < b.held
	}
	return a.rider < b.rider
}

func (m *NaiveModel) complete(op NaiveOp) error {
	if op.RiderID == "" || op.OrderID == "" || (op.StopKind != StopPickup && op.StopKind != StopDropoff) {
		return ErrInvalidArgument
	}
	if err := m.rollback(op.At); err != nil {
		return err
	}
	r, ok := m.riders[op.RiderID]
	if !ok {
		return ErrRiderNotFound
	}
	if !r.Online {
		return ErrRiderOffline
	}
	rec, ok := m.orders[op.OrderID]
	if !ok {
		return ErrOrderNotFound
	}
	if rec.rider != op.RiderID {
		return ErrOrderNotAtRider
	}
	if len(r.Pending) == 0 {
		return ErrStopOutOfOrder
	}
	head := r.Pending[0]
	if head.OrderID != op.OrderID || head.Kind != op.StopKind {
		return ErrStopOutOfOrder
	}
	switch op.StopKind {
	case StopPickup:
		if rec.status != OrderAssigned {
			return ErrStopOutOfOrder
		}
	case StopDropoff:
		if rec.status != OrderPicked {
			return ErrStopOutOfOrder
		}
	}
	travel, eok := m.tt.Travel(r.Pos, head.At)
	if !eok {
		return ErrInvalidArgument
	}
	arrive := r.DepartedAt + travel
	depart := op.At
	if depart < arrive {
		depart = arrive
	}
	if op.StopKind == StopPickup && depart < head.ReadyAt {
		depart = head.ReadyAt
	}
	r.Pos = head.At
	r.DepartedAt = depart
	r.Pending = append([]Stop(nil), r.Pending[1:]...)
	if op.StopKind == StopPickup {
		rec.status = OrderPicked
	} else {
		rec.status = OrderDelivered
	}
	m.orders[op.OrderID] = rec
	m.clock = op.At
	return nil
}

func (m *NaiveModel) cancel(op NaiveOp) error {
	if op.OrderID == "" {
		return ErrInvalidArgument
	}
	if err := m.rollback(op.At); err != nil {
		return err
	}
	rec, ok := m.orders[op.OrderID]
	if !ok {
		return ErrOrderNotFound
	}
	if rec.status == OrderNew || rec.status == OrderCancelled || rec.status == OrderDelivered {
		return ErrOrderNotAssigned
	}
	if rec.status == OrderPicked {
		return ErrOrderAlreadyPicked
	}
	r := m.riders[rec.rider]
	before, eok := EstimateSchedule(m.tt, r.Pos, r.DepartedAt, r.Pending)
	if !eok {
		return ErrInvalidArgument
	}
	next := make([]Stop, 0, len(r.Pending))
	for i := range r.Pending {
		if r.Pending[i].OrderID != op.OrderID {
			next = append(next, r.Pending[i])
		}
	}
	after, eok := EstimateSchedule(m.tt, r.Pos, r.DepartedAt, next)
	if !eok {
		return ErrInvalidArgument
	}
	ai := 0
	for bi := range r.Pending {
		if r.Pending[bi].OrderID == op.OrderID {
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
	m.orders[op.OrderID] = rec
	m.clock = op.At
	return nil
}

// Clock 暴露朴素模型当前时钟。
func (m *NaiveModel) Clock() int64 { return m.clock }

// RiderCopy 返回朴素模型中骑手状态拷贝与当前推定。
func (m *NaiveModel) RiderCopy(id RiderID) (Rider, Schedule, bool) {
	r, ok := m.riders[id]
	if !ok {
		return Rider{}, nil, false
	}
	cp := *r
	cp.Pending = append([]Stop(nil), r.Pending...)
	sched, _ := EstimateSchedule(m.tt, r.Pos, r.DepartedAt, r.Pending)
	return cp, sched, true
}

// OrderState 返回朴素模型中订单状态与归属。
func (m *NaiveModel) OrderState(id OrderID) (OrderStatus, RiderID, bool) {
	rec, ok := m.orders[id]
	if !ok {
		return 0, "", false
	}
	return rec.status, rec.rider, true
}
