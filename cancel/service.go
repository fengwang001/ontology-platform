package cancel

import (
	"container/heap"
	"sync"
)

// orderState 为单笔订单的全部可变状态。
type orderState struct {
	order       *Order
	stage       Stage
	rider       string
	cancelled   bool
	riderAborts int
	dispute     *disputeState
	result      *CancelResult
}

// Snapshot 为订单只读快照（供查询与测试复现）。
type Snapshot struct {
	ID            string
	Stage         Stage
	Rider         string
	Cancelled     bool
	RiderAborts   int
	DisputeActive bool
	Result        *CancelResult
	Amounts       Amounts
}

// Service 是取消责任判定与退款分摊系统。
// 所有方法都在同一把互斥锁内完成"校验+裁决+记账+时钟推进"，
// 因此并发调用的结果等价于某个串行顺序（线性一致），且至多落地一次取消。
type Service struct {
	cfg     Config
	mu      sync.Mutex
	lastNow int64
	orders  map[string]*orderState
	due     dueHeap
	riderAb map[string]int

	// Logf 非空时打印每步输入、输出与判定依据（用于差分测试复现）。
	Logf func(format string, args ...any)
}

// NewService 校验构造参数并创建系统。
func NewService(cfg Config) (*Service, error) {
	if cfg.DisputeWindow < 0 || cfg.LateTolerance < 0 ||
		cfg.PrepLossBP < 0 || cfg.PrepLossBP > 10000 ||
		cfg.MerchantPenalty < 0 || cfg.RiderComp < 0 {
		return nil, codeErr(ErrInvalidParam, "invalid config: %+v", cfg)
	}
	s := &Service{
		cfg:     cfg,
		orders:  make(map[string]*orderState),
		riderAb: make(map[string]int),
	}
	heap.Init(&s.due)
	return s, nil
}

func (s *Service) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// checkClock 必须在持锁状态下首先调用；被拒绝操作不推进时钟。
func (s *Service) checkClock(now int64) error {
	if now < s.lastNow {
		return codeErr(ErrClockRollback, "now=%d < last=%d", now, s.lastNow)
	}
	return nil
}

func (s *Service) requireOrder(id string) (*orderState, error) {
	st, ok := s.orders[id]
	if !ok {
		return nil, codeErr(ErrOrderNotFound, "id=%s", id)
	}
	return st, nil
}

func (s *Service) checkTerminal(st *orderState) error {
	if st.stage == StageDelivered {
		return codeErr(ErrDelivered, "order already delivered")
	}
	if st.cancelled {
		return codeErr(ErrCancelled, "order already cancelled")
	}
	return nil
}

// CreateOrder 创建订单并推进时钟。
func (s *Service) CreateOrder(now int64, o Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("CREATE_ORDER in={id=%s amt=%+v promise=%d now=%d}", o.ID, o.Amounts, o.PromiseDelivery, now)
	if err := s.checkClock(now); err != nil {
		s.logf("CREATE_ORDER out err=%v", err)
		return err
	}
	a := o.Amounts
	if o.ID == "" || a.Goods < 0 || a.Packing < 0 || a.Delivery < 0 ||
		a.Coupon < 0 || a.Coupon > a.Goods || a.Paid() < 0 {
		err := codeErr(ErrInvalidParam, "bad order amounts: %+v", a)
		s.logf("CREATE_ORDER out err=%v", err)
		return err
	}
	if _, dup := s.orders[o.ID]; dup {
		err := codeErr(ErrInvalidParam, "duplicate order id: %s", o.ID)
		s.logf("CREATE_ORDER out err=%v", err)
		return err
	}
	s.flushDue(now)
	s.lastNow = now
	cp := o
	s.orders[o.ID] = &orderState{order: &cp, stage: StagePaid}
	s.logf("CREATE_ORDER out ok stage=paid")
	return nil
}

// Accept 推进到商家已接单。
func (s *Service) Accept(now int64, id string) error {
	return s.advance(now, id, StageAccepted, "")
}

// Assign 派骑手（推进到骑手已派）。
func (s *Service) Assign(now int64, id, riderID string) error {
	if riderID == "" {
		return codeErr(ErrInvalidParam, "empty rider id")
	}
	return s.advance(now, id, StageAssigned, riderID)
}

// Pickup 骑手取货。
func (s *Service) Pickup(now int64, id string) error {
	return s.advance(now, id, StagePicked, "")
}

// Deliver 送达（终态）。
func (s *Service) Deliver(now int64, id string) error {
	return s.advance(now, id, StageDelivered, "")
}

func (s *Service) advance(now int64, id string, to Stage, rider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("ADVANCE in={id=%s to=%s rider=%s now=%d}", id, to, rider, now)
	if err := s.checkClock(now); err != nil {
		s.logf("ADVANCE out err=%v", err)
		return err
	}
	st, err := s.requireOrder(id)
	if err != nil {
		s.logf("ADVANCE out err=%v", err)
		return err
	}
	s.flushDue(now)
	if err := s.checkTerminal(st); err != nil {
		s.logf("ADVANCE out err=%v", err)
		return err
	}
	if st.dispute != nil && st.dispute.active && to == StagePicked {
		err = codeErr(ErrCancelPending, "order %s in dispute, pickup blocked", id)
		s.logf("ADVANCE out err=%v", err)
		return err
	}
	if to != st.stage+1 {
		err = codeErr(ErrStageOrder, "id=%s stage=%s -> %s", id, st.stage, to)
		s.logf("ADVANCE out err=%v", err)
		return err
	}
	s.lastNow = now
	st.stage = to
	if to == StageAssigned {
		st.rider = rider
	}
	s.logf("ADVANCE out ok stage=%s", to)
	return nil
}

// Cancel 提交取消申请。
func (s *Service) Cancel(now int64, id string, actor Actor) (*CancelResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("CANCEL in={id=%s actor=%s now=%d}", id, actorName(actor), now)
	if err := s.checkClock(now); err != nil {
		s.logf("CANCEL out err=%v", err)
		return nil, err
	}
	st, err := s.requireOrder(id)
	if err != nil {
		s.logf("CANCEL out err=%v", err)
		return nil, err
	}
	s.flushDue(now) // 可能恰好使待决订单在本次时钟落地
	if err := s.checkTerminal(st); err != nil {
		s.logf("CANCEL out err=%v", err)
		return nil, err
	}
	if st.dispute != nil && st.dispute.active {
		err = codeErr(ErrCancelPending, "id=%s dispute active", id)
		s.logf("CANCEL out err=%v", err)
		return nil, err
	}

	// 骑手取消：仅已派未取货，不落地，回到商家已接单。
	if actor == ActorRider {
		if st.stage == StagePicked {
			err = codeErr(ErrPickedUp, "rider cannot cancel after pickup")
			s.logf("CANCEL out err=%v", err)
			return nil, err
		}
		if st.stage != StageAssigned {
			err = codeErr(ErrForbidden, "rider cancel only allowed when assigned, stage=%s", st.stage)
			s.logf("CANCEL out err=%v", err)
			return nil, err
		}
		s.lastNow = now
		st.riderAborts++
		if st.rider != "" {
			s.riderAb[st.rider]++
		}
		st.rider = ""
		st.stage = StageAccepted
		s.logf("CANCEL out land=false liable=rider stage=accepted")
		return &CancelResult{OrderID: id, Land: false, Liable: PartyRider, At: now,
			Reason: "rider_cancel: no landing, order returns to accepted for reassignment"}, nil
	}

	// 用户取消：取货后必须已迟到（恰等不算迟到）。
	if actor == ActorUser && st.stage == StagePicked {
		if now <= st.order.PromiseDelivery+s.cfg.LateTolerance {
			err = codeErr(ErrNotCancellable, "user cancel after pickup but rider not late")
			s.logf("CANCEL out err=%v", err)
			return nil, err
		}
	}

	// 用户取消：商家接单后、取货前进入争议窗口。
	if actor == ActorUser && (st.stage == StageAccepted || st.stage == StageAssigned) {
		s.lastNow = now
		dl := now + s.cfg.DisputeWindow
		st.dispute = &disputeState{active: true, start: now, deadline: dl, stage: st.stage}
		heap.Push(&s.due, &dueItem{deadline: dl, orderID: id})
		s.flushDue(now) // 窗口时长为 0 时立即到期落地
		s.logf("CANCEL out dispute_opened deadline=%d", dl)
		if st.cancelled {
			return st.result, nil
		}
		return &CancelResult{OrderID: id, Land: false, Liable: PartyNone, At: now,
			Reason: "dispute opened: waiting merchant claim or window expiry"}, nil
	}

	// 其余情形立即裁决并落地（接单前用户取消 / 商家取消 / 平台取消 / 迟到用户取消）。
	d := adjudicate(s.cfg, st.stage, actor, st.order.Amounts, now,
		st.order.PromiseDelivery, false)
	res := s.land(st, d, now)
	s.lastNow = now
	s.logf("CANCEL out land=true liable=%s cash=%d restored=%d ledger=%+v reason=%s",
		res.Liable, res.Refund.CashRefund, res.Refund.CouponRestored, res.Ledger, res.Reason)
	return res, nil
}

// Claim 商家在争议窗口内声明已开始备餐；必须严格位于窗口内，右端点不允许。
func (s *Service) Claim(now int64, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("CLAIM in={id=%s now=%d}", id, now)
	if err := s.checkClock(now); err != nil {
		s.logf("CLAIM out err=%v", err)
		return false, err
	}
	st, err := s.requireOrder(id)
	if err != nil {
		s.logf("CLAIM out err=%v", err)
		return false, err
	}
	// 右端点判定优先于到期落地：恰在 deadline 声明必须报声明超时。
	if dp0 := st.dispute; dp0 != nil && dp0.active && now >= dp0.deadline {
		err = codeErr(ErrClaimTimeout, "claim at/after deadline=%d (right endpoint excluded)", dp0.deadline)
		s.logf("CLAIM out err=%v", err)
		return false, err
	}
	s.flushDue(now)
	if err := s.checkTerminal(st); err != nil {
		s.logf("CLAIM out err=%v", err)
		return false, err
	}
	dp := st.dispute
	if dp == nil || !dp.active {
		err = codeErr(ErrNoDispute, "id=%s has no active dispute", id)
		s.logf("CLAIM out err=%v", err)
		return false, err
	}
	if now < dp.start {
		err = codeErr(ErrClaimTimeout, "claim before dispute start")
		s.logf("CLAIM out err=%v", err)
		return false, err
	}
	s.lastNow = now
	dp.claimed = true
	s.logf("CLAIM out accepted=true")
	return true, nil
}

// Waive 商家主动放弃争议，立即以用户无责落地。
func (s *Service) Waive(now int64, id string) (*CancelResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("WAIVE in={id=%s now=%d}", id, now)
	if err := s.checkClock(now); err != nil {
		s.logf("WAIVE out err=%v", err)
		return nil, err
	}
	st, err := s.requireOrder(id)
	if err != nil {
		s.logf("WAIVE out err=%v", err)
		return nil, err
	}
	if dp0 := st.dispute; dp0 != nil && dp0.active && now >= dp0.deadline {
		err = codeErr(ErrClaimTimeout, "waive at/after deadline=%d: window closed", dp0.deadline)
		s.logf("WAIVE out err=%v", err)
		return nil, err
	}
	s.flushDue(now)
	if err := s.checkTerminal(st); err != nil {
		s.logf("WAIVE out err=%v", err)
		return nil, err
	}
	dp := st.dispute
	if dp == nil || !dp.active {
		err = codeErr(ErrNoDispute, "id=%s has no active dispute", id)
		s.logf("WAIVE out err=%v", err)
		return nil, err
	}
	s.lastNow = now
	d := adjudicate(s.cfg, dp.stage, ActorUser, st.order.Amounts, now,
		st.order.PromiseDelivery, false)
	res := s.land(st, d, now)
	s.logf("WAIVE out land=true liable=%s reason=%s", res.Liable, res.Reason)
	return res, nil
}

// ExpireDue 推进时钟并令所有 deadline <= now 的争议窗口到期落地。
func (s *Service) ExpireDue(now int64) ([]CancelResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("EXPIRE in={now=%d}", now)
	if err := s.checkClock(now); err != nil {
		s.logf("EXPIRE out err=%v", err)
		return nil, err
	}
	landed := s.flushDue(now)
	s.lastNow = now
	s.logf("EXPIRE out landed=%d", len(landed))
	return landed, nil
}

// flushDue 处理所有到期窗口。堆中只放入活动争议条目；
// 已被 Claim+后续取消/Waive 落地的条目惰性失效（pop 后丢弃），
// 因此到期处理只弹出待处理条目，不随已终结订单数增长。
// 声明与到期至多一个生效：Claim 后争议仍 active，到期时以 claimed 落地；
// Waive 已落地则条目失效丢弃。
func (s *Service) flushDue(now int64) []CancelResult {
	var landed []CancelResult
	for s.due.Len() > 0 && s.due[0].deadline <= now {
		it := heap.Pop(&s.due).(*dueItem)
		st, ok := s.orders[it.orderID]
		if !ok || st.cancelled {
			continue
		}
		dp := st.dispute
		if dp == nil || !dp.active || dp.deadline != it.deadline {
			continue
		}
		d := adjudicate(s.cfg, dp.stage, ActorUser, st.order.Amounts, dp.deadline,
			st.order.PromiseDelivery, dp.claimed)
		res := s.land(st, d, dp.deadline)
		s.logf("EXPIRE land id=%s claimed=%v liable=%s at=%d", it.orderID, dp.claimed, res.Liable, dp.deadline)
		landed = append(landed, *res)
	}
	return landed
}

// land 将一次裁决落地为取消终态；调用方持锁。至多执行一次。
func (s *Service) land(st *orderState, d decision, at int64) *CancelResult {
	if st.cancelled {
		return st.result
	}
	r := splitRefund(st.order.Amounts, d)
	lg := settleLedger(st.order.Amounts, d, r, s.cfg)
	st.cancelled = true
	st.dispute = nil
	st.rider = ""
	res := &CancelResult{
		OrderID: st.order.ID,
		Land:    true,
		Liable:  d.liable,
		Refund:  r,
		Ledger:  lg,
		At:      at,
		Reason:  d.reason,
	}
	st.result = res
	return res
}

// Get 返回订单只读快照。
func (s *Service) Get(id string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.orders[id]
	if !ok {
		return Snapshot{}, false
	}
	return Snapshot{
		ID:            st.order.ID,
		Stage:         st.stage,
		Rider:         st.rider,
		Cancelled:     st.cancelled,
		RiderAborts:   st.riderAborts,
		DisputeActive: st.dispute != nil && st.dispute.active,
		Result:        st.result,
		Amounts:       st.order.Amounts,
	}, true
}

// RiderAbortCount 返回某骑手累计取消次数。
func (s *Service) RiderAbortCount(riderID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.riderAb[riderID]
}

func actorName(a Actor) string {
	switch a {
	case ActorUser:
		return "user"
	case ActorMerchant:
		return "merchant"
	case ActorPlatform:
		return "platform"
	case ActorRider:
		return "rider"
	default:
		return "?"
	}
}
