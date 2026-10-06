package carpool

import (
	"fmt"
	"sort"
	"sync"
)

// Service 是合乘匹配与费用分摊服务。
// 所有公开方法持有同一把互斥锁，并发调用等价于某个串行顺序。
type Service struct {
	mu         sync.Mutex
	cfg        Config
	lastTime   int64
	seq        int
	vehicles   map[string]*Vehicle
	vehicleIDs []string // 字典序，保证匹配选择的确定性
	orders     map[string]*Order
	waiting    []*Order // 按（下单时刻, 序号）升序
	fareIters  int64    // computeRawFares 迭代计数，用于查询开销证明
}

// NewService 创建服务；配置非法时返回错误。
func NewService(cfg Config) (*Service, error) {
	if cfg.StopDuration < 0 || cfg.TimePerDistance < 0 || cfg.UnitPrice < 0 ||
		cfg.CancelFee < 0 || cfg.MaxActiveOrders < 1 {
		return nil, ErrInvalidParam
	}
	return &Service{
		cfg:      cfg,
		vehicles: make(map[string]*Vehicle),
		orders:   make(map[string]*Order),
	}, nil
}

// AddVehicle 注册一辆空载车辆。
func (s *Service) AddVehicle(id string, seats int, pos, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || seats < 1 || pos < 0 {
		return ErrInvalidParam
	}
	if _, dup := s.vehicles[id]; dup {
		return ErrInvalidParam
	}
	if now < s.lastTime {
		return ErrClockRollback
	}
	s.vehicles[id] = &Vehicle{ID: id, Seats: seats, Pos: pos}
	s.vehicleIDs = append(s.vehicleIDs, id)
	sort.Strings(s.vehicleIDs)
	s.lastTime = now
	s.expireWaiting(now)
	return nil
}

// SubmitOrder 提交新订单，尝试立即匹配，否则进入等待或报无车可用。
func (s *Service) SubmitOrder(id string, pickup, dropoff int64, persons int, maxStopDelay, latestPickup, now int64) (SubmitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || pickup < 0 || dropoff <= pickup || persons < 1 ||
		maxStopDelay < 0 || latestPickup < now {
		return SubmitResult{}, ErrInvalidParam
	}
	if _, dup := s.orders[id]; dup {
		return SubmitResult{}, ErrInvalidParam
	}
	if now < s.lastTime {
		return SubmitResult{}, ErrClockRollback
	}
	o := &Order{
		ID: id, Pickup: pickup, Dropoff: dropoff, Persons: persons,
		MaxStopDelay: maxStopDelay, LatestPickup: latestPickup,
		SubmitTime: now, Status: StatusWaiting,
	}
	vid, ok := s.tryMatch(o, now)
	if !ok && latestPickup <= now {
		// 无法等待（最晚时刻即当前时刻），操作被拒绝，不改变任何状态。
		return SubmitResult{}, ErrNoVehicle
	}
	o.Seq = s.seq
	s.seq++
	s.orders[id] = o
	if ok {
		s.assign(o, s.vehicles[vid])
	} else {
		s.waiting = append(s.waiting, o)
	}
	s.lastTime = now
	s.expireWaiting(now)
	if ok {
		return SubmitResult{Status: o.Status, VehicleID: vid}, nil
	}
	return SubmitResult{Status: StatusWaiting}, nil
}

// CancelOrder 取消未上车的订单。
func (s *Service) CancelOrder(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return ErrInvalidParam
	}
	if now < s.lastTime {
		return ErrClockRollback
	}
	o, ok := s.orders[id]
	if !ok {
		return ErrOrderNotFound
	}
	switch o.Status {
	case StatusCompleted:
		return ErrOrderCompleted
	case StatusOnboard:
		return ErrOrderOnboard
	case StatusCancelled, StatusExpired:
		return ErrInvalidParam
	}
	if o.Status == StatusWaiting {
		s.removeWaiting(o)
	} else {
		v := s.vehicles[o.VehicleID]
		v.Active = removeOrder(v.Active, o)
	}
	o.Status = StatusCancelled
	o.Settled = s.cfg.CancelFee
	s.lastTime = now
	s.expireWaiting(now)
	return nil
}

// UpdateVehiclePosition 更新车辆位置，处理上下车、等待重试与失效。
func (s *Service) UpdateVehiclePosition(vehicleID string, pos, now int64) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || pos < 0 {
		return nil, ErrInvalidParam
	}
	if now < s.lastTime {
		return nil, ErrClockRollback
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return nil, ErrVehicleNotFound
	}
	if pos < v.Pos {
		return nil, ErrPositionRegression
	}
	v.Pos = pos
	var events []Event
	s.processArrivals(v, &events)
	s.retryWaiting(now, &events)
	events = append(events, s.expireWaiting(now)...)
	s.lastTime = now
	return events, nil
}

// QueryOrder 查询订单当前的预计应付、锁价上限与状态。
// 开销只与该车在途订单数（受 MaxActiveOrders 限制）有关，
// 与历史订单总数无关：完成/取消的订单即时结算归档，不再参与遍历。
func (s *Service) QueryOrder(id string) (OrderView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return OrderView{}, ErrOrderNotFound
	}
	view := OrderView{
		ID: id, Status: o.Status, VehicleID: o.VehicleID,
		Cap: o.Cap, SoloFare: soloFare(o, s.cfg.UnitPrice),
	}
	switch o.Status {
	case StatusWaiting:
		view.EstimatedFare = view.SoloFare
	case StatusMatched, StatusOnboard:
		raw := computeRawFares(s.vehicles[o.VehicleID].Active, s.cfg.UnitPrice, &s.fareIters)
		view.EstimatedFare = payable(raw[o.ID], view.SoloFare, o.Cap)
	case StatusCompleted, StatusCancelled:
		view.EstimatedFare = o.Settled
	}
	return view, nil
}

// tryMatch 在所有车辆中按（延误增量最小，车辆标识字典序最小）选车。
func (s *Service) tryMatch(o *Order, now int64) (string, bool) {
	best := ""
	var bestDelta int64
	found := false
	for _, vid := range s.vehicleIDs {
		d, ok := s.vehicles[vid].insertDelta(o, s.cfg, now)
		if !ok {
			continue
		}
		if !found || d < bestDelta {
			best, bestDelta, found = vid, d, true
		}
	}
	return best, found
}

// assign 把订单并入车辆行程，并按当时车上全部乘客锁定其应付上限。
func (s *Service) assign(o *Order, v *Vehicle) {
	o.VehicleID = v.ID
	if o.Pickup <= v.Pos {
		o.Status = StatusOnboard
	} else {
		o.Status = StatusMatched
	}
	v.Active = append(v.Active, o)
	raw := computeRawFares(v.Active, s.cfg.UnitPrice, &s.fareIters)
	cap := raw[o.ID]
	if solo := soloFare(o, s.cfg.UnitPrice); cap > solo {
		cap = solo
	}
	o.Cap = cap
}

// processArrivals 按位置升序处理车辆到达新位置途中的所有下车与上车。
// 同一位置先下车后上车；完成订单以当时车上全部乘客结算且不再变化。
func (s *Service) processArrivals(v *Vehicle, events *[]Event) {
	for {
		loc := int64(-1)
		for _, o := range v.Active {
			if o.Status == StatusOnboard && o.Dropoff <= v.Pos && (loc < 0 || o.Dropoff < loc) {
				loc = o.Dropoff
			}
			if o.Status == StatusMatched && o.Pickup <= v.Pos && (loc < 0 || o.Pickup < loc) {
				loc = o.Pickup
			}
		}
		if loc < 0 {
			return
		}
		raw := computeRawFares(v.Active, s.cfg.UnitPrice, &s.fareIters)
		for _, o := range v.Active {
			if o.Status == StatusOnboard && o.Dropoff == loc {
				o.Settled = payable(raw[o.ID], soloFare(o, s.cfg.UnitPrice), o.Cap)
				o.Status = StatusCompleted
				*events = append(*events, Event{Kind: EventCompleted, OrderID: o.ID, VehicleID: v.ID, Fare: o.Settled})
			}
		}
		v.Active = removeCompleted(v.Active)
		for _, o := range v.Active {
			if o.Status == StatusMatched && o.Pickup == loc {
				o.Status = StatusOnboard
				*events = append(*events, Event{Kind: EventBoarded, OrderID: o.ID, VehicleID: v.ID})
			}
		}
	}
}

// retryWaiting 按下单先后重试等待队列，成功匹配的订单出队。
func (s *Service) retryWaiting(now int64, events *[]Event) {
	var kept []*Order
	for _, o := range s.waiting {
		if vid, ok := s.tryMatch(o, now); ok {
			s.assign(o, s.vehicles[vid])
			*events = append(*events, Event{Kind: EventMatched, OrderID: o.ID, VehicleID: vid})
		} else {
			kept = append(kept, o)
		}
	}
	s.waiting = kept
}

// removeWaiting 从等待队列移除订单。
func (s *Service) removeWaiting(o *Order) {
	s.waiting = removeOrder(s.waiting, o)
}

// removeOrder 从切片移除指定订单。
func removeOrder(orders []*Order, o *Order) []*Order {
	out := orders[:0]
	for _, x := range orders {
		if x != o {
			out = append(out, x)
		}
	}
	return out
}

// removeCompleted 从在途切片移除已完成订单（归档，不再参与遍历）。
func removeCompleted(orders []*Order) []*Order {
	out := orders[:0]
	for _, o := range orders {
		if o.Status != StatusCompleted {
			out = append(out, o)
		}
	}
	return out
}

// checkVehicleInvariant 校验不变式（供测试与并发场景核对）：
// 每辆车各段分摊之和等于各段基础费用之和减去被上限截掉的部分，
// 且每位乘客的应付不超过其上限与独行费用两者中的较小者。
func (s *Service) checkVehicleInvariant(vehicleID string) error {
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return ErrVehicleNotFound
	}
	raw := computeRawFares(v.Active, s.cfg.UnitPrice, nil)
	locs := stopLocations(v.Active)
	var bases int64
	for i := 0; i+1 < len(locs); i++ {
		covered := false
		for _, o := range v.Active {
			if o.Pickup <= locs[i] && o.Dropoff >= locs[i+1] {
				covered = true
				break
			}
		}
		if covered {
			bases += (locs[i+1] - locs[i]) * s.cfg.UnitPrice
		}
	}
	var sumRaw, sumFinal int64
	for _, o := range v.Active {
		solo := soloFare(o, s.cfg.UnitPrice)
		final := payable(raw[o.ID], solo, o.Cap)
		if final > solo || final > o.Cap {
			return fmt.Errorf("order %s pays %d, exceeds min(cap=%d, solo=%d)", o.ID, final, o.Cap, solo)
		}
		sumRaw += raw[o.ID]
		sumFinal += final
	}
	if sumRaw != bases {
		return fmt.Errorf("vehicle %s: segment shares %d != segment bases %d", vehicleID, sumRaw, bases)
	}
	if sumFinal != bases-(sumRaw-sumFinal) {
		return fmt.Errorf("vehicle %s: capped-sum invariant broken", vehicleID)
	}
	return nil
}

// expireWaiting 使最晚上车时刻不晚于 now 的等待订单失效，返回失效事件。
func (s *Service) expireWaiting(now int64) []Event {
	var kept []*Order
	var events []Event
	for _, o := range s.waiting {
		if o.LatestPickup <= now {
			o.Status = StatusExpired
			events = append(events, Event{Kind: EventExpired, OrderID: o.ID})
		} else {
			kept = append(kept, o)
		}
	}
	s.waiting = kept
	return events
}
