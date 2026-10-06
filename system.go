package ontology

import "sync"

// Config 为系统配置。所有时间单位为秒，金额单位为分。
// 退票与改签各自有独立的三档比例，下标 0/1/2 分别对应
// 远（最宽松）/ 中 / 近（最严）三档。
type Config struct {
	LongThreshold  int64 // 长阈值（>=0），距出发不小于该值归远档
	ShortThreshold int64 // 短阈值（>=0），长阈值必须严格大于短阈值
	RefundPercents [3]int
	ChangePercents [3]int
	MaxChanges     int   // 自愿改签次数上限（非自愿改签不计入）
	VoucherTTL     int64 // 代金券有效期（秒，必须 >0）
}

// Flight 为航班（目标航班也通过 RegisterFlight 注册后引用）。
type Flight struct {
	ID        string
	Fare      int64
	Departure int64
	Cancelled bool
}

// Ticket 是每张票的完整状态。所有汇总量在每次改签时增量维护，
// 因而退票/改签报价与查询均为 O(1)，不随历史改签次数增长。
type Ticket struct {
	ID           string
	Owner        string
	FlightID     string
	Fare         int64 // 当前票面价（已并入历次补付差价）
	Departure    int64 // 当前（原航班）出发时刻
	Refunded     bool
	Involuntary  bool  // 仅由航司取消产生
	Changes      int   // 自愿改签成功次数
	ChangeFees   int64 // 历次已付改签费累计（现金口径，非自愿退票时额外退回）
	CashPaid     int64 // 历次现金支付累计
	CashRefunded int64 // 历次现金退回累计
}

// Voucher 为代金券。面额剩余额递减，超额抵扣后差额留在原券上。
type Voucher struct {
	ID        string
	Owner     string
	Amount    int64
	CreatedAt int64
	ExpiresAt int64
	Used      bool // 面额用尽后置真；未过期不可仅凭“使用过”判定
}

// UsableAt 判断代金券在时刻 now 是否可用于支付。
// 恰等于到期时刻视为已过期。
func (v *Voucher) UsableAt(now int64) bool {
	return !v.Used && now < v.ExpiresAt
}

// System 是线程安全的结算系统。单一互斥锁将所有变更串行化，
// 从而使并发调用等价于某个确定的串行顺序。
type System struct {
	cfg      Config
	mu       sync.Mutex
	now      int64
	flights  map[string]*Flight
	tickets  map[string]*Ticket
	vouchers map[string]*Voucher
	vcSeq    int
}

func New(cfg Config) *System {
	s := &System{
		cfg:      cfg,
		flights:  make(map[string]*Flight),
		tickets:  make(map[string]*Ticket),
		vouchers: make(map[string]*Voucher),
	}
	return s
}

func validateConfig(c Config) error {
	if c.ShortThreshold < 0 || c.LongThreshold <= c.ShortThreshold {
		return errf(KindInvalidArgument, "invalid thresholds: long=%d short=%d", c.LongThreshold, c.ShortThreshold)
	}
	for _, p := range c.RefundPercents {
		if p < 0 || p > 100 {
			return errf(KindInvalidArgument, "invalid refund percent %d", p)
		}
	}
	for _, p := range c.ChangePercents {
		if p < 0 || p > 100 {
			return errf(KindInvalidArgument, "invalid change percent %d", p)
		}
	}
	if c.MaxChanges < 0 {
		return errf(KindInvalidArgument, "invalid max changes %d", c.MaxChanges)
	}
	if c.VoucherTTL <= 0 {
		return errf(KindInvalidArgument, "invalid voucher ttl %d", c.VoucherTTL)
	}
	return nil
}

// RegisterFlight 注册航班；参数校验失败属于“参数非法”。
func (s *System) RegisterFlight(id string, fare, departure int64) error {
	if id == "" || fare < 0 || departure < 0 {
		return errf(KindInvalidArgument, "invalid flight args")
	}
	if err := validateConfig(s.cfg); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flights[id] != nil {
		return errf(KindInvalidArgument, "flight already exists: %s", id)
	}
	s.flights[id] = &Flight{ID: id, Fare: fare, Departure: departure}
	return nil
}

// PurchaseTicket 出票。购票不推进业务时钟，也不校验时刻，
// 但记录票的初始归属航班。
func (s *System) PurchaseTicket(id, owner, flightID string) error {
	if id == "" || owner == "" || flightID == "" {
		return errf(KindInvalidArgument, "invalid purchase args")
	}
	if err := validateConfig(s.cfg); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flights[flightID]
	if f == nil {
		return errf(KindInvalidArgument, "flight not found: %s", flightID)
	}
	if f.Cancelled {
		return errf(KindInvalidArgument, "flight cancelled: %s", flightID)
	}
	if s.tickets[id] != nil {
		return errf(KindInvalidArgument, "ticket already exists: %s", id)
	}
	s.tickets[id] = &Ticket{
		ID:        id,
		Owner:     owner,
		FlightID:  flightID,
		Fare:      f.Fare,
		Departure: f.Departure,
	}
	return nil
}

// CancelFlight 航司取消航班：该航班上所有未退票（含曾改签到本航班的票）
// 标记为非自愿。被取消航班上的票不报已出发。
func (s *System) CancelFlight(flightID string, now int64) (int, error) {
	if flightID == "" || now < 0 {
		return 0, errf(KindInvalidArgument, "invalid cancel args")
	}
	if err := validateConfig(s.cfg); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return 0, errf(KindClockRewind, "now=%d before last accepted %d", now, s.now)
	}
	f := s.flights[flightID]
	if f == nil {
		return 0, errf(KindFlightNotFound, "flight not found: %s", flightID)
	}
	if f.Cancelled {
		return 0, errf(KindInvalidArgument, "flight already cancelled: %s", flightID)
	}
	f.Cancelled = true
	n := 0
	for _, t := range s.tickets {
		if !t.Refunded && t.FlightID == flightID {
			t.Involuntary = true
			n++
		}
	}
	s.now = now
	return n, nil
}

// TicketView 返回一张票当前状态的只读快照（O(1)，用于查询与对账）。
func (s *System) TicketView(id string) (Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	if !ok {
		return Ticket{}, false
	}
	return *t, true
}

// VoucherView 返回一张代金券当前状态的只读快照。
func (s *System) VoucherView(id string) (Voucher, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vouchers[id]
	if !ok {
		return Voucher{}, false
	}
	return *v, true
}

func (s *System) advanceClockLocked(now int64) error {
	if now < s.now {
		return errf(KindClockRewind, "now=%d before last accepted %d", now, s.now)
	}
	s.now = now
	return nil
}

// checkClockLocked 只检查时钟回退，不推进时钟。
func (s *System) checkClockLocked(now int64) error {
	if now < s.now {
		return errf(KindClockRewind, "now=%d before last accepted %d", now, s.now)
	}
	return nil
}
