package delivery

import (
	"fmt"
	"sync"
)

// Package delivery 实现配送异常上报与无法送达处置系统。
//
// 模块划分：
//   - types.go：值对象、状态枚举、可程序化区分的错误码。
//   - system.go：单调时钟、订单生命周期、按单笔订单的惰性到期转化与查询。
//   - exceptions.go：三类异常状态机（联系不上、地址有误、用户拒收）。
//   - settlement.go：无法送达后的退回/就地处理、商家确认窗口与结算补偿。
//   - naive.go：独立的事件溯源朴素对照模型，仅供差分测试。
//
// 所有时刻为整数秒且全局单调。并发安全由单一互斥锁保证：所有结果等价于
// 某个串行交错顺序；同一时刻的竞争由实际获得锁的顺序决定唯一胜者。

type exception struct {
	view    ExceptionView
	records []ContactRecord
	origX   int64
	origY   int64
}

type order struct {
	view OrderView
}

// System 是并发安全的配送异常上报与处置系统。
// 单一互斥锁串行化所有状态变更，使并发结果等价于某个串行顺序。
type System struct {
	mu       sync.Mutex
	params   Params
	lastTime int64
	orders   map[string]*order
	excs     map[string]*exception
	nextID   int64
}

// NewSystem 校验并构造系统。
func NewSystem(p Params) (*System, error) {
	if p.MinWaitSeconds < 0 || p.MinContacts < 0 || p.MinContactInterval < 0 ||
		p.CorrectionWindow <= 0 || p.MaxCorrectionDist < 0 ||
		p.EvidenceValidSeconds < 0 || p.RiderCompensation < 0 || p.MerchantConfirmWin <= 0 {
		return nil, &OpError{Code: ErrInvalidParam, Msg: "invalid constructor params"}
	}
	return &System{
		params: p,
		orders: map[string]*order{},
		excs:   map[string]*exception{},
	}, nil
}

func validDisp(d Disposition) bool { return d == DispReturn || d == DispLocal }

// checkClock 仅检查时钟回退，不推进时钟（时钟只在操作被接受时推进）。
func (s *System) checkClock(at int64) error {
	if at < s.lastTime {
		return &OpError{Code: ErrClockRollback, Msg: fmt.Sprintf("clock rollback: %d < %d", at, s.lastTime)}
	}
	return nil
}

// commit 在拒绝检查全部通过后推进已接受操作的最大时刻。
func (s *System) commit(at int64) { s.lastTime = at }

func (s *System) genID(prefix string) string {
	s.nextID++
	return fmt.Sprintf("%s%d", prefix, s.nextID)
}

// settleAt 以时刻 at 对单笔订单执行惰性到期转化。
// 只访问该订单自身字段，开销 O(1)，与平台异常总数无关。
func (s *System) settleAt(o *order, at int64) {
	if o.view.Status == OSException {
		if ex := s.excs[o.view.ActiveException]; ex != nil &&
			ex.view.Type == ETWrongAddress && ex.view.Status == ESActive &&
			at >= ex.view.Deadline {
			ex.view.Status = ESUndeliverable
			s.applyUndeliverable(o, ex, ex.view.Deadline, ESClosedExpired)
		}
	}
	if o.view.Status == OSReturning && o.view.ConfirmDeadline >= 0 && at >= o.view.ConfirmDeadline {
		o.view.Status = OSReturnUnconfirmed
		o.view.Responsibility = PartyMerchant
	}
}

// settleOrderAt 供订单级操作使用：只触发该订单自身的地址异常到期；
// 退回确认窗口仅在查询或商家确认操作处惰性判定。
func (s *System) settleOrderAt(o *order, at int64) {
	if o.view.Status == OSException {
		if ex := s.excs[o.view.ActiveException]; ex != nil &&
			ex.view.Type == ETWrongAddress && ex.view.Status == ESActive &&
			at >= ex.view.Deadline {
			ex.view.Status = ESUndeliverable
			s.applyUndeliverable(o, ex, ex.view.Deadline, ESClosedExpired)
		}
	}
}

func (s *System) PlaceOrder(id string, disp Disposition, x, y, at int64) error {
	if id == "" || !validDisp(disp) || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid place order args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	if _, ok := s.orders[id]; ok {
		return &OpError{Code: ErrInvalidParam, Msg: "duplicate order id"}
	}
	s.commit(at)
	s.orders[id] = &order{view: OrderView{
		ID:              id,
		Status:          OSPlaced,
		Disposition:     disp,
		AddressX:        x,
		AddressY:        y,
		ReturnedAt:      -1,
		ConfirmDeadline: -1,
	}}
	return nil
}

func (s *System) PickUp(orderID string, at int64) error {
	if orderID == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid pickup args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return &OpError{Code: ErrOrderNotFound, Msg: "order not found: " + orderID}
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	s.settleAt(o, at)
	switch o.view.Status {
	case OSPlaced:
		s.commit(at)
		o.view.Status = OSPicked
		return nil
	case OSPicked:
		s.commit(at)
		return nil
	default:
		if o.view.Status.Terminal() {
			return &OpError{Code: ErrOrderTerminal, Msg: "order terminal"}
		}
		return &OpError{Code: ErrWrongState, Msg: "cannot pickup in current state"}
	}
}

func (s *System) ConfirmDelivery(orderID string, at int64) error {
	if orderID == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid delivery args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return &OpError{Code: ErrOrderNotFound, Msg: "order not found: " + orderID}
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	s.settleAt(o, at)
	switch o.view.Status {
	case OSPicked:
		s.commit(at)
		o.view.Status = OSDelivered
		return nil
	case OSDelivered:
		return &OpError{Code: ErrAlreadyDelivered, Msg: "already delivered"}
	default:
		if o.view.Status.Terminal() {
			return &OpError{Code: ErrOrderTerminal, Msg: "order terminal"}
		}
		return &OpError{Code: ErrWrongState, Msg: "cannot deliver in current state"}
	}
}

func (s *System) GetOrder(id string, at int64) (OrderView, error) {
	if id == "" || at < 0 {
		return OrderView{}, &OpError{Code: ErrInvalidParam, Msg: "invalid query args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return OrderView{}, &OpError{Code: ErrOrderNotFound, Msg: "order not found: " + id}
	}
	s.settleAt(o, at)
	return o.view, nil
}

func (s *System) GetException(id string, at int64) (ExceptionView, error) {
	if id == "" || at < 0 {
		return ExceptionView{}, &OpError{Code: ErrInvalidParam, Msg: "invalid query args"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ex, ok := s.excs[id]
	if !ok {
		return ExceptionView{}, &OpError{Code: ErrExceptionNotFound, Msg: "exception not found: " + id}
	}
	if o := s.orders[ex.view.OrderID]; o != nil {
		s.settleAt(o, at)
	}
	return ex.view, nil
}
