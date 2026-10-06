package shophours

import (
	"sync"
	"time"
)

// Config 是 System 的构造参数。
type Config struct {
	BaseTime                int64 // 周划分基准时刻
	WeekSec                 int64 // 一周秒数
	Intervals               []Interval
	PreCloseLead            int64 // 打烊前截止提前量（秒）
	MaxClosureDuration      int64 // 临时歇业最长时长（秒）
	MinClosureGap           int64 // 临时歇业之间最短间隔（秒）
	MaxReservationAheadDays int64 // 预约单最大提前天数
}

// System 是商家营业时段与临时歇业管理系统。所有方法可并发调用，
// 内部以单一互斥锁串行化，结果等价于某个合法串行顺序。
type System struct {
	mu       sync.Mutex
	cfg      Config
	clock    int64
	tables   *scheduleBook
	closures *closureBook
	orders   *orderBook
	history  []*closure
}

// NewSystem 以构造参数创建系统，初始表立即用于第 0 周。
func NewSystem(cfg Config) (*System, error) {
	if cfg.WeekSec <= 0 {
		return nil, bizErr(ErrInvalidParam, "WeekSec must be positive")
	}
	if cfg.PreCloseLead < 0 || cfg.MaxClosureDuration < 0 || cfg.MinClosureGap < 0 ||
		cfg.MaxReservationAheadDays < 0 {
		return nil, bizErr(ErrInvalidParam, "duration parameters must be non-negative")
	}
	if err := validateIntervals(cfg.Intervals, cfg.WeekSec); err != nil {
		return nil, err
	}
	return &System{
		cfg:      cfg,
		clock:    noTime,
		tables:   newScheduleBook(cfg.BaseTime, cfg.WeekSec, cfg.Intervals),
		closures: newClosureBook(cfg.MaxClosureDuration, cfg.MinClosureGap),
		orders:   newOrderBook(),
	}, nil
}

// tick 是所有写操作的统一入口：校验时钟、推进惰性状态，并在强制停业首次
// 生效时取消全部已接未开工订单（平台责任）。被拒绝的操作在调用 tick 前
// 只做纯参数校验，因此不会推进时钟或任何状态。
func (s *System) tick(now int64) error {
	if s.clock != noTime && now < s.clock {
		return bizErr(ErrClockRollback, "operation time earlier than last accepted time")
	}
	s.clock = now
	s.tables.advance(now)
	activated, fStart := s.closures.advance(now)
	if activated {
		s.orders.cancelOpenInstant(fStart, ResponsiblePlatform, ErrForcedSuspension)
	}
	return nil
}

// SubmitTable 提交新周时段表；下一个周边界生效，覆盖既有待生效表。
// 强制停业期间同样允许提交。
func (s *System) SubmitTable(now int64, ivs []Interval) error {
	if err := validateIntervals(ivs, s.cfg.WeekSec); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return err
	}
	return s.tables.submit(now, ivs)
}

// StartTemporaryClosure 发起临时歇业。cancelPending=true 时连带取消
// 已接未开工即时单（商家责任）。
func (s *System) StartTemporaryClosure(now, start, duration int64, cancelPending bool) error {
	if start < now || duration <= 0 || duration > s.cfg.MaxClosureDuration {
		return bizErr(ErrInvalidParam, "closure start/duration out of allowed range")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return err
	}
	if s.closures.forceOn(now) {
		return bizErr(ErrForcedSuspension, "merchant is under forced suspension")
	}
	if !s.closures.gapOK(start) {
		return bizErr(ErrClosureGap, "interval since previous closure too short")
	}
	end := start + duration
	if s.closures.overlaps(start, end) {
		return bizErr(ErrClosureOverlap, "closure overlaps an existing closure")
	}
	if len(s.orders.openInst) > 0 && !cancelPending {
		return bizErr(ErrPendingOrders, "there are accepted but not started instant orders")
	}
	c := &closure{start: start, end: end, actualEnd: end}
	s.closures.add(c)
	s.history = append(s.history, c)
	if len(s.orders.openInst) > 0 {
		s.orders.cancelOpenInstant(now, ResponsibleMerchant, ErrTemporaryClosure)
	}
	return nil
}

// EndTemporaryClosure 提前结束当前正在进行的歇业。
func (s *System) EndTemporaryClosure(now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return err
	}
	c := s.closures.covering(now)
	if c == nil {
		return bizErr(ErrState, "no active temporary closure to end")
	}
	if now >= c.end {
		return bizErr(ErrState, "temporary closure already ended")
	}
	c.actualEnd = now
	c.endedEarly = true
	s.closures.prune(now)
	return nil
}

// ForceSuspend 登记强制停业；start 可为当前或未来时刻，无截止直至解除。
func (s *System) ForceSuspend(now, start int64) error {
	if start < 0 {
		return bizErr(ErrInvalidParam, "force start must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return err
	}
	if s.closures.forceStart != noTime {
		return bizErr(ErrState, "forced suspension already in effect")
	}
	s.closures.forceStart = start
	if activated, fStart := s.closures.advance(now); activated {
		// 停业起点即刻到达：取消所有已接未开工订单（平台责任）。
		s.orders.cancelOpenInstant(fStart, ResponsiblePlatform, ErrForcedSuspension)
	}
	return nil
}

// LiftForceSuspension 解除强制停业；不恢复任何已取消订单。
func (s *System) LiftForceSuspension(now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return err
	}
	if s.closures.forceStart == noTime {
		return bizErr(ErrState, "no forced suspension to lift")
	}
	if !s.closures.forceActive {
		return bizErr(ErrState, "forced suspension not yet active")
	}
	s.closures.forceStart = noTime
	s.closures.forceActive = false
	return nil
}

// AcceptInstant 判定即时单准入。返回订单 ID。
func (s *System) AcceptInstant(now, prepDuration int64) (int64, error) {
	if prepDuration < 0 {
		return 0, bizErr(ErrInvalidParam, "prep duration must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return 0, err
	}
	if s.closures.forceOn(now) {
		return 0, bizErr(ErrForcedSuspension, "merchant is under forced suspension")
	}
	if s.closures.covering(now) != nil {
		return 0, bizErr(ErrTemporaryClosure, "merchant is temporarily closed")
	}
	end, ok := s.tables.current.containingEnd(now, s.cfg.BaseTime, s.cfg.WeekSec)
	if !ok {
		return 0, bizErr(ErrNotOpen, "not within business hours")
	}
	if end-now < prepDuration+s.cfg.PreCloseLead {
		return 0, bizErr(ErrNearClosing, "remaining business time insufficient")
	}
	rec := s.orders.add(KindInstant, now, now+prepDuration)
	return rec.id, nil
}

// AcceptReservation 判定预约单准入。返回订单 ID。
func (s *System) AcceptReservation(now, targetAt, prepDuration int64) (int64, error) {
	if prepDuration < 0 || targetAt < now {
		return 0, bizErr(ErrInvalidParam, "target/prep out of range")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return 0, err
	}
	if s.closures.forceOn(now) {
		return 0, bizErr(ErrForcedSuspension, "merchant is under forced suspension")
	}
	maxAhead := s.cfg.MaxReservationAheadDays * int64(24*time.Hour/time.Second)
	if targetAt-now > maxAhead {
		return 0, bizErr(ErrReservationTooFar, "target pickup beyond reservation horizon")
	}
	weekStart := s.cfg.BaseTime + floorDiv(targetAt-s.cfg.BaseTime, s.cfg.WeekSec)*s.cfg.WeekSec
	tbl := s.tables.forWeek(weekStart)
	begin := targetAt - prepDuration
	end, ok := tbl.containingEnd(begin, s.cfg.BaseTime, s.cfg.WeekSec)
	if !ok || targetAt > end {
		return 0, bizErr(ErrNotOpen, "prep window not within business hours")
	}
	if s.closures.contains(targetAt) {
		return 0, bizErr(ErrTemporaryClosure, "target time falls in a temporary closure")
	}
	rec := s.orders.add(KindReservation, now, targetAt)
	return rec.id, nil
}

// StartOrder 报告订单开工。
func (s *System) StartOrder(now, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return err
	}
	rec, ok := s.orders.byID[id]
	if !ok {
		return bizErr(ErrNotFound, "order not found")
	}
	if rec.status == OrderCancelled {
		return bizErr(ErrState, "order already cancelled")
	}
	if rec.completed {
		return bizErr(ErrState, "order already completed")
	}
	if rec.started {
		return bizErr(ErrState, "order already started")
	}
	if now < rec.acceptedAt {
		return bizErr(ErrState, "cannot start before acceptance")
	}
	rec.started = true
	rec.status = OrderStarted
	if rec.kind == KindInstant {
		delete(s.orders.openInst, id)
	}
	return nil
}

// CompleteOrder 报告订单完成。
func (s *System) CompleteOrder(now, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return err
	}
	rec, ok := s.orders.byID[id]
	if !ok {
		return bizErr(ErrNotFound, "order not found")
	}
	if rec.status == OrderCancelled {
		return bizErr(ErrState, "order already cancelled")
	}
	if rec.completed {
		return bizErr(ErrState, "order already completed")
	}
	if !rec.started {
		return bizErr(ErrState, "order not started")
	}
	rec.completed = true
	rec.status = OrderCompleted
	return nil
}

// GetOrder 查询订单快照（只读，不推进时钟）。
func (s *System) GetOrder(id int64) (Order, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.orders.byID[id]
	if !ok {
		return Order{}, false
	}
	return s.orders.snapshot(rec), true
}

// ListClosures 返回全部临时歇业记录快照（含提前结束信息）。
func (s *System) ListClosures() []ClosureInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ClosureInfo, 0, len(s.history))
	for _, c := range s.history {
		out = append(out, ClosureInfo{
			Start:      c.start,
			Duration:   c.end - c.start,
			ActualEnd:  c.actualEnd,
			EndedEarly: c.endedEarly,
		})
	}
	return out
}
