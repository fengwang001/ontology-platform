package shophours

import "time"

// naiveSys 是独立于 System 的朴素对照实现：所有判定均线性扫描/逐点计算，
// 不使用数组下标索引与惰性指针，只用于差分随机测试。
type naiveSys struct {
	cfg        Config
	clock      int64
	current    []Interval
	pending    []Interval
	pendBound  int64
	hasPending bool
	closures   []naiveClosure
	forceStart int64
	forceOn    bool
	orders     map[int64]*naiveOrder
	nextID     int64
}

type naiveClosure struct {
	start, end, actualEnd int64
	endedEarly            bool
}

type naiveOrder struct {
	id           int64
	kind         OrderKind
	acceptedAt   int64
	promisedAt   int64
	status       OrderStatus
	cancelledBy  ResponsibleParty
	cancelledAt  int64
	cancelReason ErrorCode
	started      bool
	completed    bool
}

func newNaive(cfg Config) *naiveSys {
	return &naiveSys{
		cfg:        cfg,
		clock:      noTime,
		current:    append([]Interval(nil), cfg.Intervals...),
		forceStart: noTime,
		orders:     map[int64]*naiveOrder{},
		nextID:     1,
	}
}

func nWeekStart(cfg Config, t int64) int64 {
	return cfg.BaseTime + floorDiv(t-cfg.BaseTime, cfg.WeekSec)*cfg.WeekSec
}

// nIntervalEnd 逐区间扫描 t 所在营业区间的绝对右端点。
func nIntervalEnd(cfg Config, ivs []Interval, t int64) (int64, bool) {
	off := mod(t-cfg.BaseTime, cfg.WeekSec)
	for _, iv := range ivs {
		var right int64
		if iv.Start < iv.End {
			if iv.Start <= off && off < iv.End {
				right = iv.End
			}
		} else {
			if off >= iv.Start {
				right = iv.End + cfg.WeekSec
			} else if off < iv.End {
				right = iv.End
			}
		}
		if right != 0 {
			return t + (right - off), true
		}
	}
	return 0, false
}

// nValidate 逐秒标注 owner 检查重叠，并检查相邻营业秒（含回绕）是否分属不同区间。
func nValidate(ivs []Interval, w int64) error {
	if w <= 0 {
		return bizErr(ErrInvalidParam, "w")
	}
	for _, iv := range ivs {
		if iv.Start < 0 || iv.Start > w || iv.End < 0 || iv.End > w || iv.Start == iv.End {
			return bizErr(ErrInvalidParam, "bad iv")
		}
	}
	owner := make([]int, w)
	for i := range owner {
		owner[i] = -1
	}
	for idx, iv := range ivs {
		for t := int64(0); t < w; t++ {
			inside := false
			if iv.Start < iv.End {
				inside = iv.Start <= t && t < iv.End
			} else {
				inside = t >= iv.Start || t < iv.End
			}
			if inside {
				if owner[t] != -1 {
					return bizErr(ErrInvalidParam, "overlap")
				}
				owner[t] = idx
			}
		}
	}
	for t := int64(0); t < w; t++ {
		a, b := owner[t], owner[(t+1)%w]
		if a != -1 && b != -1 && a != b {
			return bizErr(ErrInvalidParam, "touch")
		}
	}
	return nil
}

func (n *naiveSys) cancelOpen(now int64, by ResponsibleParty, reason ErrorCode) {
	for _, o := range n.orders {
		if o.kind == KindInstant && o.status == OrderAccepted {
			o.status = OrderCancelled
			o.cancelledBy = by
			o.cancelledAt = now
			o.cancelReason = reason
		}
	}
}

func (n *naiveSys) tick(now int64) error {
	if n.clock != noTime && now < n.clock {
		return bizErr(ErrClockRollback, "rollback")
	}
	n.clock = now
	if n.hasPending && nWeekStart(n.cfg, now) >= n.pendBound {
		n.current = n.pending
		n.pending = nil
		n.hasPending = false
	}
	if !n.forceOn && n.forceStart != noTime && n.forceStart <= now {
		n.forceOn = true
		n.cancelOpen(n.forceStart, ResponsiblePlatform, ErrForcedSuspension)
	}
	return nil
}

func (n *naiveSys) ivsFor(target int64) []Interval {
	if n.hasPending && nWeekStart(n.cfg, target) >= n.pendBound {
		return n.pending
	}
	return n.current
}

func (n *naiveSys) lastEnd(now int64) int64 {
	le := noTime
	for _, c := range n.closures {
		if c.actualEnd <= now && c.actualEnd > le {
			le = c.actualEnd
		}
	}
	return le
}

func (n *naiveSys) submit(now int64, ivs []Interval) error {
	if err := nValidate(ivs, n.cfg.WeekSec); err != nil {
		return err
	}
	if err := n.tick(now); err != nil {
		return err
	}
	n.pending = append([]Interval(nil), ivs...)
	n.pendBound = n.cfg.BaseTime + (floorDiv(now-n.cfg.BaseTime, n.cfg.WeekSec)+1)*n.cfg.WeekSec
	n.hasPending = true
	return nil
}

func (n *naiveSys) startClosure(now, start, dur int64, cancel bool) error {
	if start < now || dur <= 0 || dur > n.cfg.MaxClosureDuration {
		return bizErr(ErrInvalidParam, "bad closure")
	}
	if err := n.tick(now); err != nil {
		return err
	}
	if n.forceOn {
		return bizErr(ErrForcedSuspension, "force")
	}
	if le := n.lastEnd(now); le != noTime && start-le < n.cfg.MinClosureGap {
		return bizErr(ErrClosureGap, "gap")
	}
	end := start + dur
	for i := range n.closures {
		c := &n.closures[i]
		if c.actualEnd > now && start < c.end && c.start < end {
			return bizErr(ErrClosureOverlap, "overlap")
		}
	}
	pending := 0
	for _, o := range n.orders {
		if o.kind == KindInstant && o.status == OrderAccepted {
			pending++
		}
	}
	if pending > 0 && !cancel {
		return bizErr(ErrPendingOrders, "pending")
	}
	n.closures = append(n.closures, naiveClosure{start: start, end: end, actualEnd: end})
	if pending > 0 {
		n.cancelOpen(now, ResponsibleMerchant, ErrTemporaryClosure)
	}
	return nil
}

func (n *naiveSys) endClosure(now int64) error {
	if err := n.tick(now); err != nil {
		return err
	}
	idx := -1
	for i := range n.closures {
		c := &n.closures[i]
		if c.start <= now && now < c.actualEnd {
			idx = i
		}
	}
	if idx == -1 {
		return bizErr(ErrState, "no active closure")
	}
	if now >= n.closures[idx].end {
		return bizErr(ErrState, "ended")
	}
	n.closures[idx].actualEnd = now
	n.closures[idx].endedEarly = true
	return nil
}

func (n *naiveSys) force(now, start int64) error {
	if start < 0 {
		return bizErr(ErrInvalidParam, "bad force")
	}
	if err := n.tick(now); err != nil {
		return err
	}
	if n.forceStart != noTime {
		return bizErr(ErrState, "already force")
	}
	n.forceStart = start
	if start <= now {
		n.forceOn = true
		n.cancelOpen(start, ResponsiblePlatform, ErrForcedSuspension)
	}
	return nil
}

func (n *naiveSys) lift(now int64) error {
	if err := n.tick(now); err != nil {
		return err
	}
	if n.forceStart == noTime {
		return bizErr(ErrState, "no force")
	}
	if !n.forceOn {
		return bizErr(ErrState, "not active")
	}
	n.forceStart = noTime
	n.forceOn = false
	return nil
}

func (n *naiveSys) acceptInstant(now, prep int64) (int64, error) {
	if prep < 0 {
		return 0, bizErr(ErrInvalidParam, "prep")
	}
	if err := n.tick(now); err != nil {
		return 0, err
	}
	if n.forceOn {
		return 0, bizErr(ErrForcedSuspension, "force")
	}
	for _, c := range n.closures {
		if c.start <= now && now < c.actualEnd {
			return 0, bizErr(ErrTemporaryClosure, "closure")
		}
	}
	end, ok := nIntervalEnd(n.cfg, n.current, now)
	if !ok {
		return 0, bizErr(ErrNotOpen, "closed")
	}
	if end-now < prep+n.cfg.PreCloseLead {
		return 0, bizErr(ErrNearClosing, "near")
	}
	id := n.nextID
	n.nextID++
	n.orders[id] = &naiveOrder{id: id, kind: KindInstant, acceptedAt: now, promisedAt: now + prep, status: OrderAccepted}
	return id, nil
}

func (n *naiveSys) acceptReservation(now, target, prep int64) (int64, error) {
	if prep < 0 || target < now {
		return 0, bizErr(ErrInvalidParam, "target")
	}
	if err := n.tick(now); err != nil {
		return 0, err
	}
	if n.forceOn {
		return 0, bizErr(ErrForcedSuspension, "force")
	}
	maxAhead := n.cfg.MaxReservationAheadDays * int64(24*time.Hour/time.Second)
	if target-now > maxAhead {
		return 0, bizErr(ErrReservationTooFar, "far")
	}
	ivs := n.ivsFor(target)
	begin := target - prep
	end, ok := nIntervalEnd(n.cfg, ivs, begin)
	if !ok || target > end {
		return 0, bizErr(ErrNotOpen, "closed")
	}
	for _, c := range n.closures {
		if c.start <= target && target < c.end {
			return 0, bizErr(ErrTemporaryClosure, "closure target")
		}
	}
	id := n.nextID
	n.nextID++
	n.orders[id] = &naiveOrder{id: id, kind: KindReservation, acceptedAt: now, promisedAt: target, status: OrderAccepted}
	return id, nil
}

func (n *naiveSys) startOrder(now, id int64) error {
	if err := n.tick(now); err != nil {
		return err
	}
	o, ok := n.orders[id]
	if !ok {
		return bizErr(ErrNotFound, "no order")
	}
	switch {
	case o.status == OrderCancelled:
		return bizErr(ErrState, "cancelled")
	case o.completed:
		return bizErr(ErrState, "done")
	case o.started:
		return bizErr(ErrState, "started")
	case now < o.acceptedAt:
		return bizErr(ErrState, "before accept")
	}
	o.started = true
	o.status = OrderStarted
	return nil
}

func (n *naiveSys) completeOrder(now, id int64) error {
	if err := n.tick(now); err != nil {
		return err
	}
	o, ok := n.orders[id]
	if !ok {
		return bizErr(ErrNotFound, "no order")
	}
	switch {
	case o.status == OrderCancelled:
		return bizErr(ErrState, "cancelled")
	case o.completed:
		return bizErr(ErrState, "done")
	case !o.started:
		return bizErr(ErrState, "not started")
	}
	o.completed = true
	o.status = OrderCompleted
	return nil
}
