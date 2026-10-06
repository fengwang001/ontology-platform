package delivery

import "fmt"

// 朴素对照模型：追加式事件日志是唯一事实来源。
// 每次操作均从全部已接受事件逐条重放状态，刻意不做索引与缓存，
// 与快速实现形成算法独立的事件溯源对照（O(N^2) 重放）。

type naiveOp int

const (
	nopPlace naiveOp = iota
	nopPickup
	nopDeliver
	nopReport
	nopContact
	nopJudge
	nopRespond
	nopCorrect
	nopReturn
	nopConfirm
)

type naiveEvent struct {
	op    naiveOp
	at    int64
	order string
	et    ExceptionType
	evID  string
	evAt  int64
	x, y  int64
	excID string
	disp  Disposition
}

type naiveExc struct {
	view    ExceptionView
	records []ContactRecord
	origX   int64
	origY   int64
}

type naiveState struct {
	orders map[string]*order
	excs   map[string]*naiveExc
}

// NaiveSystem 是独立朴素实现，仅供差分测试。
type NaiveSystem struct {
	params   Params
	lastTime int64
	log      []naiveEvent
	nextID   int64
}

func NewNaive(p Params) *NaiveSystem {
	return &NaiveSystem{params: p}
}

// replay 从事件日志重建截至时刻 at（含惰性到期转化）的完整状态。
func (n *NaiveSystem) replay(at int64) *naiveState {
	st := &naiveState{orders: map[string]*order{}, excs: map[string]*naiveExc{}}
	for _, ev := range n.log {
		// 按事件时刻只做地址异常到期转化；退回确认窗口仅在统一结算时判定，
		// 因为退回途中订单在其事件时刻尚未到期。
		if ev.op != nopPlace {
			if o := st.orders[ev.order]; o != nil {
				n.settleOrder(st, o, ev.at)
			}
		}
		n.apply(st, ev)
	}
	for _, o := range st.orders {
		n.settle(st, o, at)
	}
	return st
}

// replayOrders 仅重放地址异常到期，不判定退回确认窗口（供骑手送回操作使用）。
func (n *NaiveSystem) replayOrders(at int64) *naiveState {
	st := &naiveState{orders: map[string]*order{}, excs: map[string]*naiveExc{}}
	for _, ev := range n.log {
		if ev.op != nopPlace {
			if o := st.orders[ev.order]; o != nil {
				n.settleOrder(st, o, ev.at)
			}
		}
		n.apply(st, ev)
	}
	// 仅事件重放，不做任何惰性到期判定（调用方按自身语义另行处理）。
	return st
}

func (n *NaiveSystem) add(ev naiveEvent) {
	n.log = append(n.log, ev)
	n.lastTime = ev.at
}

// checkClock 只检查不推进；推进时钟等价于事件被追加（add）。

func (n *NaiveSystem) settle(st *naiveState, o *order, at int64) {
	if o.view.Status == OSException {
		if ex := st.excs[o.view.ActiveException]; ex != nil &&
			ex.view.Type == ETWrongAddress && ex.view.Status == ESActive &&
			at >= ex.view.Deadline {
			ex.view.Status = ESUndeliverable
			n.applyUndeliverable(st, o, ex, ex.view.Deadline, ESClosedExpired)
		}
	}
	if o.view.Status == OSReturning && o.view.ConfirmDeadline >= 0 && at >= o.view.ConfirmDeadline {
		o.view.Status = OSReturnUnconfirmed
		o.view.Responsibility = PartyMerchant
	}
}

func (n *NaiveSystem) settleOrder(st *naiveState, o *order, at int64) {
	if o.view.Status == OSException {
		if ex := st.excs[o.view.ActiveException]; ex != nil &&
			ex.view.Type == ETWrongAddress && ex.view.Status == ESActive &&
			at >= ex.view.Deadline {
			ex.view.Status = ESUndeliverable
			n.applyUndeliverable(st, o, ex, ex.view.Deadline, ESClosedExpired)
		}
	}
}

func (n *NaiveSystem) applyUndeliverable(st *naiveState, o *order, ex *naiveExc, at int64, cs ExceptionStatus) {
	ex.view.Status = cs
	o.view.ActiveException = ""
	o.view.Responsibility = PartyUser
	if !o.view.CompPaid {
		o.view.CompPaid = true
		o.view.CompAmount = n.params.RiderCompensation
	}
	if o.view.Disposition == DispLocal {
		o.view.Status = OSHandled
	} else {
		o.view.Status = OSReturning
	}
}

func (n *NaiveSystem) apply(st *naiveState, ev naiveEvent) {
	switch ev.op {
	case nopPlace:
		st.orders[ev.order] = &order{view: OrderView{
			ID: ev.order, Status: OSPlaced, Disposition: ev.disp,
			AddressX: ev.x, AddressY: ev.y, ReturnedAt: -1, ConfirmDeadline: -1,
		}}
	case nopPickup:
		if st.orders[ev.order].view.Status == OSPlaced {
			st.orders[ev.order].view.Status = OSPicked
		}
	case nopDeliver:
		if st.orders[ev.order].view.Status == OSPicked {
			st.orders[ev.order].view.Status = OSDelivered
		}
	case nopReport:
		o := st.orders[ev.order]
		ex := &naiveExc{
			view: ExceptionView{
				ID: ev.excID, OrderID: ev.order, Type: ev.et,
				Status: ESActive, StartTime: ev.at, LastContactAt: -1, Deadline: -1,
				EvidenceID: ev.evID, EvidenceAt: ev.evAt,
			},
			origX: o.view.AddressX, origY: o.view.AddressY,
		}
		if ev.et == ETWrongAddress {
			ex.view.Deadline = ev.at + n.params.CorrectionWindow
		}
		st.excs[ev.excID] = ex
		o.view.ActiveException = ev.excID
		o.view.Status = OSException
		if ev.et == ETRejection {
			n.applyUndeliverable(st, o, ex, ev.at, ESUndeliverable)
		}
	case nopContact:
		ex := st.excs[ev.excID]
		ex.records = append(ex.records, ContactRecord{Time: ev.at})
		ex.view.ContactCount++
		ex.view.LastContactAt = ev.at
	case nopJudge:
		n.applyUndeliverable(st, st.orders[ev.order], st.excs[ev.excID], ev.at, ESUndeliverable)
	case nopRespond:
		ex := st.excs[ev.excID]
		o := st.orders[ev.order]
		ex.view.Status = ESClosedUser
		o.view.ActiveException = ""
		o.view.Status = OSPicked
	case nopCorrect:
		ex := st.excs[ev.excID]
		o := st.orders[ev.order]
		o.view.AddressX = ev.x
		o.view.AddressY = ev.y
		o.view.AddressChanges++
		ex.view.Status = ESClosedUser
		o.view.ActiveException = ""
		o.view.Status = OSPicked
	case nopReturn:
		o := st.orders[ev.order]
		if o.view.Status == OSReturning && o.view.ReturnedAt == -1 {
			o.view.ReturnedAt = ev.at
			o.view.ConfirmDeadline = ev.at + n.params.MerchantConfirmWin
		}
	case nopConfirm:
		st.orders[ev.order].view.Status = OSReturned
	}
}

func (n *NaiveSystem) rollback(at int64) error {
	if at < n.lastTime {
		return &OpError{Code: ErrClockRollback, Msg: "clock rollback"}
	}
	return nil
}

func (n *NaiveSystem) active(st *naiveState, id string) (*order, *naiveExc, error) {
	ex, ok := st.excs[id]
	if !ok {
		return nil, nil, &OpError{Code: ErrExceptionNotFound, Msg: "nf"}
	}
	o := st.orders[ex.view.OrderID]
	if o == nil {
		return nil, nil, &OpError{Code: ErrOrderNotFound, Msg: "nf"}
	}
	return o, ex, nil
}

func (n *NaiveSystem) requireActive(o *order, ex *naiveExc, id string) error {
	if o.view.ActiveException != id || ex.view.Status != ESActive {
		return &OpError{Code: ErrExceptionClosed, Msg: "closed"}
	}
	return nil
}

func (n *NaiveSystem) PlaceOrder(id string, disp Disposition, x, y, at int64) error {
	if id == "" || !validDisp(disp) || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	if _, ok := n.replay(at).orders[id]; ok {
		return &OpError{Code: ErrInvalidParam, Msg: "dup"}
	}
	if err := n.rollback(at); err != nil {
		return err
	}
	n.add(naiveEvent{op: nopPlace, at: at, order: id, disp: disp, x: x, y: y})
	return nil
}

func (n *NaiveSystem) PickUp(id string, at int64) error {
	if id == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	o := n.replay(at).orders[id]
	if o == nil {
		return &OpError{Code: ErrOrderNotFound, Msg: "nf"}
	}
	if err := n.rollback(at); err != nil {
		return err
	}
	if o.view.Status == OSPlaced {
		n.add(naiveEvent{op: nopPickup, at: at, order: id})
		return nil
	}
	if o.view.Status == OSPicked {
		return nil
	}
	if o.view.Status.Terminal() {
		return &OpError{Code: ErrOrderTerminal, Msg: "terminal"}
	}
	return &OpError{Code: ErrWrongState, Msg: "state"}
}

func (n *NaiveSystem) ConfirmDelivery(id string, at int64) error {
	if id == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	o := n.replay(at).orders[id]
	if o == nil {
		return &OpError{Code: ErrOrderNotFound, Msg: "nf"}
	}
	if err := n.rollback(at); err != nil {
		return err
	}
	switch {
	case o.view.Status == OSPicked:
		n.add(naiveEvent{op: nopDeliver, at: at, order: id})
		return nil
	case o.view.Status == OSDelivered:
		return &OpError{Code: ErrAlreadyDelivered, Msg: "delivered"}
	case o.view.Status.Terminal():
		return &OpError{Code: ErrOrderTerminal, Msg: "terminal"}
	default:
		return &OpError{Code: ErrWrongState, Msg: "state"}
	}
}

func (n *NaiveSystem) ReportException(orderID string, at int64, et ExceptionType, evID string, evAt int64) (string, error) {
	if orderID == "" || at < 0 || !validExceptionType(et) {
		return "", &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	if et == ETRejection && evID == "" {
		return "", &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	st := n.replay(at)
	o, ok := st.orders[orderID]
	if !ok {
		return "", &OpError{Code: ErrOrderNotFound, Msg: "nf"}
	}
	if err := n.rollback(at); err != nil {
		return "", err
	}
	if o.view.Status != OSPicked {
		switch {
		case o.view.Status == OSDelivered:
			return "", &OpError{Code: ErrAlreadyDelivered, Msg: "delivered"}
		case o.view.Status.Terminal():
			return "", &OpError{Code: ErrOrderTerminal, Msg: "terminal"}
		case o.view.Status == OSException:
			return "", &OpError{Code: ErrActiveException, Msg: "active"}
		case o.view.Status == OSPlaced:
			return "", &OpError{Code: ErrNotPickedUp, Msg: "not picked"}
		default:
			return "", &OpError{Code: ErrWrongState, Msg: "state"}
		}
	}
	if et == ETRejection && evAt < at-n.params.EvidenceValidSeconds {
		return "", &OpError{Code: ErrInvalidEvidence, Msg: "evidence"}
	}
	n.nextID++
	id := fmt.Sprintf("e%d", n.nextID)
	ev := naiveEvent{op: nopReport, at: at, order: orderID, et: et, evID: evID, evAt: evAt, excID: id}
	n.add(ev)
	return id, nil
}

func (n *NaiveSystem) RecordContact(id string, at int64) error {
	if id == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	st := n.replay(at)
	o, ex, err := n.active(st, id)
	if err != nil {
		return err
	}
	if err := n.rollback(at); err != nil {
		return err
	}
	n.settle(st, o, at)
	if ex.view.Type != ETUnreachable {
		return &OpError{Code: ErrTypeMismatch, Msg: "type"}
	}
	if err := n.requireActive(o, ex, id); err != nil {
		return err
	}
	if ex.view.LastContactAt >= 0 && at-ex.view.LastContactAt < n.params.MinContactInterval {
		return &OpError{Code: ErrContactTooSoon, Msg: "soon"}
	}
	n.add(naiveEvent{op: nopContact, at: at, order: ex.view.OrderID, excID: id})
	return nil
}

func (n *NaiveSystem) JudgeUndeliverable(id string, at int64) error {
	if id == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	st := n.replay(at)
	o, ex, err := n.active(st, id)
	if err != nil {
		return err
	}
	if err := n.rollback(at); err != nil {
		return err
	}
	if ex.view.Type != ETUnreachable {
		return &OpError{Code: ErrTypeMismatch, Msg: "type"}
	}
	if err := n.requireActive(o, ex, id); err != nil {
		return err
	}
	if at-ex.view.StartTime < n.params.MinWaitSeconds {
		return &OpError{Code: ErrConditionWait, Msg: "wait"}
	}
	if ex.view.ContactCount < n.params.MinContacts {
		return &OpError{Code: ErrConditionContact, Msg: "contacts"}
	}
	n.add(naiveEvent{op: nopJudge, at: at, order: ex.view.OrderID, excID: id})
	return nil
}

func (n *NaiveSystem) UserRespond(id string, at int64) error {
	if id == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	st := n.replay(at)
	o, ex, err := n.active(st, id)
	if err != nil {
		return err
	}
	if err := n.rollback(at); err != nil {
		return err
	}
	if ex.view.Type != ETUnreachable {
		return &OpError{Code: ErrTypeMismatch, Msg: "type"}
	}
	if err := n.requireActive(o, ex, id); err != nil {
		return err
	}
	n.add(naiveEvent{op: nopRespond, at: at, order: ex.view.OrderID, excID: id})
	return nil
}

func (n *NaiveSystem) SubmitCorrection(id string, x, y, at int64) error {
	if id == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	st := n.replay(at - 1) // 用窗口右端点前一刻的状态，避免本次到期先行转化
	o, ex, err := n.active(st, id)
	if err != nil {
		return err
	}
	if err := n.rollback(at); err != nil {
		return err
	}
	if ex.view.Type != ETWrongAddress {
		return &OpError{Code: ErrTypeMismatch, Msg: "type"}
	}
	if err := n.requireActive(o, ex, id); err != nil {
		return err
	}
	if at >= ex.view.Deadline {
		return &OpError{Code: ErrCorrectionWindow, Msg: "window"}
	}
	if absInt64(x-ex.origX)+absInt64(y-ex.origY) > n.params.MaxCorrectionDist {
		return &OpError{Code: ErrDistanceExceeded, Msg: "dist"}
	}
	n.add(naiveEvent{op: nopCorrect, at: at, order: ex.view.OrderID, excID: id, x: x, y: y})
	return nil
}

func (n *NaiveSystem) RiderReturn(id string, at int64) error {
	if id == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	st := n.replayOrders(at)
	o, ok := st.orders[id]
	if !ok {
		return &OpError{Code: ErrOrderNotFound, Msg: "nf"}
	}
	if err := n.rollback(at); err != nil {
		return err
	}
	n.settleOrder(st, o, at) // 只处理地址异常到期；不提前判定确认窗口
	if o.view.Status == OSReturning && o.view.ConfirmDeadline >= 0 && at >= o.view.ConfirmDeadline {
		o.view.Status = OSReturnUnconfirmed
		o.view.Responsibility = PartyMerchant
	}
	switch o.view.Status {
	case OSReturnUnconfirmed:
		return &OpError{Code: ErrConfirmWindow, Msg: "expired"}
	case OSReturning:
		if o.view.ReturnedAt >= 0 {
			return &OpError{Code: ErrWrongState, Msg: "returned"}
		}
		n.add(naiveEvent{op: nopReturn, at: at, order: id})
		return nil
	case OSReturned:
		return &OpError{Code: ErrOrderTerminal, Msg: "terminal"}
	default:
		if o.view.Status.Terminal() {
			return &OpError{Code: ErrOrderTerminal, Msg: "terminal"}
		}
		return &OpError{Code: ErrWrongState, Msg: "state"}
	}
}

func (n *NaiveSystem) MerchantConfirm(id string, at int64) error {
	if id == "" || at < 0 {
		return &OpError{Code: ErrInvalidParam, Msg: "invalid"}
	}
	st := n.replay(at)
	o, ok := st.orders[id]
	if !ok {
		return &OpError{Code: ErrOrderNotFound, Msg: "nf"}
	}
	if err := n.rollback(at); err != nil {
		return err
	}
	n.settle(st, o, at)
	if o.view.Status == OSReturnUnconfirmed {
		return &OpError{Code: ErrConfirmWindow, Msg: "expired"}
	}
	if o.view.Status != OSReturning {
		if o.view.Status.Terminal() {
			return &OpError{Code: ErrOrderTerminal, Msg: "terminal"}
		}
		return &OpError{Code: ErrWrongState, Msg: "state"}
	}
	if o.view.ReturnedAt < 0 {
		return &OpError{Code: ErrWrongState, Msg: "not returned"}
	}
	if at >= o.view.ConfirmDeadline {
		return &OpError{Code: ErrConfirmWindow, Msg: "edge"}
	}
	n.add(naiveEvent{op: nopConfirm, at: at, order: id})
	return nil
}

// Snapshot 返回截至时刻 at 的完整状态，供差分测试逐项比对。
func (n *NaiveSystem) Snapshot(at int64) (map[string]OrderView, map[string]ExceptionView) {
	st := n.replay(at)
	ov := map[string]OrderView{}
	for id, o := range st.orders {
		ov[id] = o.view
	}
	ev := map[string]ExceptionView{}
	for id, ex := range st.excs {
		ev[id] = ex.view
	}
	return ov, ev
}
