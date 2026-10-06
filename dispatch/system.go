package dispatch

import (
	"sort"
	"sync"
)

type orderStatus int

const (
	statusCreated orderStatus = iota
	statusAssigned
	statusPicked
	statusDelivered
	statusCancelled
)

type riderState struct {
	id       string
	region   string
	pos      Point
	departAt int64
	capacity int
	online   bool
	stops    []stop
	held     int
}

type orderState struct {
	order  Order
	status orderStatus
	rider  string
}

// System 是派单系统。所有公开操作可并发调用，内部由单互斥量串行化，
// 效果等价于某个串行顺序；全局时钟只在操作被接受时推进。
type System struct {
	mu       sync.Mutex
	cfg      Config
	src      TravelTimeSource
	maxTime  int64
	riders   map[string]*riderState
	orders   map[string]*orderState
	byRegion map[string]map[string]struct{}
}

// New 构造派单系统。
func New(cfg Config, src TravelTimeSource) *System {
	return &System{
		cfg:      cfg,
		src:      src,
		riders:   make(map[string]*riderState),
		orders:   make(map[string]*orderState),
		byRegion: make(map[string]map[string]struct{}),
	}
}

func (s *System) lookup(orderID string) orderInfo {
	o := s.orders[orderID].order
	return orderInfo{pickup: o.Pickup, drop: o.Drop, readyAt: o.ReadyAt}
}

// checkClock 只做校验；时钟在推进到 accept 阶段才更新。
func (s *System) checkClock(t int64) error {
	if t < s.maxTime {
		return ErrClockRegression
	}
	return nil
}

// AddRider 注册骑手（默认在线）。
func (s *System) AddRider(r Rider, t int64) error {
	if r.ID == "" || r.Region == "" || r.Pos == "" || r.Capacity < 1 || r.DepartAt < 0 {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	if _, ok := s.riders[r.ID]; ok {
		return ErrRiderExists
	}
	s.riders[r.ID] = &riderState{
		id: r.ID, region: r.Region, pos: r.Pos, departAt: r.DepartAt,
		capacity: r.Capacity, online: true,
	}
	if s.byRegion[r.Region] == nil {
		s.byRegion[r.Region] = make(map[string]struct{})
	}
	s.byRegion[r.Region][r.ID] = struct{}{}
	s.maxTime = t
	return nil
}

// SetOnline 切换骑手在线状态；离线骑手不参与派单。
func (s *System) SetOnline(riderID string, online bool, t int64) error {
	if riderID == "" {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	r, ok := s.riders[riderID]
	if !ok {
		return ErrRiderNotFound
	}
	r.online = online
	s.maxTime = t
	return nil
}

// CreateOrder 登记一笔新订单（尚未派单）。
func (s *System) CreateOrder(o Order, t int64) error {
	if o.ID == "" || o.Region == "" || o.Pickup == "" || o.Drop == "" ||
		o.ReadyAt < 0 || o.PromiseAt < o.ReadyAt || o.PickupDwell < 0 || o.DropDwell < 0 {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	if _, ok := s.orders[o.ID]; ok {
		return ErrOrderExists
	}
	s.orders[o.ID] = &orderState{order: o, status: statusCreated}
	s.maxTime = t
	return nil
}

// DispatchOrder 把一笔已登记订单并入某位可行骑手的路线。
func (s *System) DispatchOrder(orderID string, t int64) (Assignment, error) {
	if orderID == "" {
		return Assignment{}, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return Assignment{}, err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return Assignment{}, ErrOrderNotFound
	}
	switch o.status {
	case statusAssigned, statusPicked:
		return Assignment{}, ErrOrderAlreadyAssigned
	case statusDelivered, statusCancelled:
		return Assignment{}, ErrOrderNotAssignable
	}
	ids := sortedKeys(s.byRegion[o.order.Region])
	allFull := true
	anyPromise := false
	var best riderPick
	found := false
	for _, id := range ids {
		r := s.riders[id]
		if !r.online || r.held >= r.capacity {
			continue
		}
		allFull = false
		ins, feasible, promiseOK := s.scanInsertions(r, o)
		if promiseOK {
			anyPromise = true
		}
		if !feasible {
			continue
		}
		if !found || betterPick(riderPick{r: r, ins: ins}, best) {
			best, found = riderPick{r: r, ins: ins}, true
		}
	}
	if !found {
		switch {
		case allFull:
			return Assignment{}, ErrNoRiderCapacity
		case !anyPromise:
			return Assignment{}, ErrNoPromisePosition
		default:
			return Assignment{}, ErrDetourLimit
		}
	}
	r := best.r
	pk := stop{orderID: o.order.ID, kind: StopPickup, dwell: o.order.PickupDwell, etaCap: noEtaCap}
	dl := stop{orderID: o.order.ID, kind: StopDeliver, dwell: o.order.DropDwell, etaCap: noEtaCap}
	r.stops = insertStops(r.stops, best.ins.p, best.ins.q, pk, dl)
	r.held++
	o.status = statusAssigned
	o.rider = r.id
	s.maxTime = t
	return Assignment{
		RiderID:    r.id,
		PickupPos:  best.ins.p,
		DeliverPos: best.ins.q,
		DropEta:    best.ins.dropEta,
		Increment:  best.ins.increment,
	}, nil
}

// CompleteStop 报告骑手完成序列首个停靠；报告时刻成为该停靠实际离开时刻。
func (s *System) CompleteStop(riderID, orderID string, kind StopKind, t int64) error {
	if riderID == "" || orderID == "" || (kind != StopPickup && kind != StopDeliver) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	r, ok := s.riders[riderID]
	if !ok {
		return ErrRiderNotFound
	}
	if !r.online {
		return ErrRiderOffline
	}
	if len(r.stops) == 0 || r.stops[0].orderID != orderID || r.stops[0].kind != kind {
		return ErrStopOutOfOrder
	}
	st := r.stops[0]
	o := s.orders[st.orderID]
	info := s.lookup(st.orderID)
	leave := t
	if st.kind == StopPickup && leave < info.readyAt {
		leave = info.readyAt // 早到等待至出餐就绪再离开
	}
	r.pos = st.point(info)
	r.departAt = leave
	r.stops = r.stops[1:]
	for i := range r.stops {
		r.stops[i].etaCap = noEtaCap // 实际时刻取代取消时收紧的推定上限
	}
	if st.kind == StopPickup {
		o.status = statusPicked
	} else {
		o.status = statusDelivered
		r.held--
	}
	s.maxTime = t
	return nil
}

// CancelOrder 取消订单；在途订单移除其两个停靠并重新推定，
// 保留停靠的推定到达时刻不得因此变晚（用 etaCap 钳制）。
func (s *System) CancelOrder(orderID string, t int64) error {
	if orderID == "" {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return ErrOrderNotFound
	}
	switch o.status {
	case statusPicked, statusDelivered:
		return ErrOrderAlreadyPickedUp
	case statusCancelled:
		return ErrOrderAlreadyCancelled
	case statusCreated:
		o.status = statusCancelled
		s.maxTime = t
		return nil
	}
	r := s.riders[o.rider]
	oldEta, _ := project(s.src, s.lookup, r.pos, r.departAt, r.stops)
	kept := make([]stop, 0, len(r.stops)-2)
	for i, st := range r.stops {
		if st.orderID == orderID {
			continue
		}
		if oldEta[i] < st.etaCap {
			st.etaCap = oldEta[i]
		}
		kept = append(kept, st)
	}
	r.stops = kept
	r.held--
	o.status = statusCancelled
	o.rider = ""
	s.maxTime = t
	return nil
}

// Route 返回骑手当前停靠序列及推定时刻的只读快照。
func (s *System) Route(riderID string) ([]StopView, error) {
	if riderID == "" {
		return nil, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.riders[riderID]
	if !ok {
		return nil, ErrRiderNotFound
	}
	eta, leave := project(s.src, s.lookup, r.pos, r.departAt, r.stops)
	out := make([]StopView, len(r.stops))
	for i, st := range r.stops {
		out[i] = StopView{OrderID: st.orderID, Kind: st.kind, Eta: eta[i], Leave: leave[i]}
	}
	return out, nil
}

// Held 返回骑手当前持有订单数。
func (s *System) Held(riderID string) (int, error) {
	if riderID == "" {
		return 0, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.riders[riderID]
	if !ok {
		return 0, ErrRiderNotFound
	}
	return r.held, nil
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
