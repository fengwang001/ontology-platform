// Package naive 是年休假额度服务的独立朴素参考实现，用于模型对照测试。
//
// 与主实现刻意采用完全不同的结构：
//   - 事件溯源：只追加被接受的操作，任何结果都从事件日志整体重放；
//   - 逐年触达：年度发放与结转用从入职年起的一年一年的显式循环补齐；
//   - 线性扫描：重叠判定、状态查找都直接遍历全部假单；
//   - 无任何索引、快照或缓存。
//
// 正确性优先于效率，语义与 leave.Service 完全一致。
package naive

import (
	"fmt"

	"ontology/leave"
)

const daysPerYear = 365

const (
	kRequest = iota
	kApprove
	kReject
	kWithdraw
	kCancel
	kEarlyEnd
)

type event struct {
	now      int
	kind     int
	id       int64
	from, to int
	newEnd   int
	charges  []leave.Charge
}

// Emp 保存一名员工的登记信息与全部已接受事件。
type Emp struct {
	hireDay int
	regNow  int
	events  []event
}

// Service 是朴素参考服务（非并发安全，仅供单线程对照）。
type Service struct {
	cfg     leave.Config
	nonWork [daysPerYear]bool
	clock   int
	emps    map[string]*Emp
	nextID  int64
}

func errf(cat leave.Category, format string, args ...any) *leave.Error {
	return &leave.Error{Cat: cat, Msg: fmt.Sprintf(format, args...)}
}

// NewService 创建朴素参考服务。
func NewService(cfg leave.Config) *Service {
	s := &Service{cfg: cfg, clock: -1, emps: make(map[string]*Emp)}
	for _, d := range cfg.NonWorkdays {
		s.nonWork[d] = true
	}
	return s
}

func yearOf(day int) int { return day / daysPerYear }
func doyOf(day int) int  { return day % daysPerYear }

// quota 与主实现相同的确定性发放函数（独立编写）。
func (s *Service) quota(e *Emp, y int) int {
	hireYear := yearOf(e.hireDay)
	if y < hireYear {
		return 0
	}
	tier := 0
	tenure := 0
	if y > hireYear {
		tenure = (daysPerYear*y - e.hireDay) / daysPerYear
	}
	for _, b := range s.cfg.TenureBounds {
		if tenure >= b {
			tier++
		}
	}
	base := s.cfg.AnnualQuotas[tier]
	if y == hireYear {
		return base * (daysPerYear - doyOf(e.hireDay)) / daysPerYear
	}
	return base
}

// rstate 是一次重放得到的中间状态。
type rstate struct {
	carryYear  int
	carriedOut map[int]int
	usedCur    map[int]int
	usedCar    map[int]int
	pendCur    map[int]int
	pendCar    map[int]int
	voidedCar  map[int]int
	leaves     map[int64]*rleave
}

type rleave struct {
	id       int64
	from, to int
	status   leave.Status
	charges  []leave.Charge
}

func terminated(st leave.Status) bool {
	return st == leave.StatusRejected || st == leave.StatusWithdrawn || st == leave.StatusCancelled
}

// materialize 逐年补齐年度结转（逐年触达的显式形式）。
func (s *Service) materialize(e *Emp, st *rstate, Y int) {
	for y := st.carryYear + 1; y <= Y; y++ {
		unused := s.quota(e, y-1) - st.usedCur[y-1] - st.pendCur[y-1]
		if unused < 0 {
			unused = 0
		}
		if unused > s.cfg.CarryCap {
			unused = s.cfg.CarryCap
		}
		st.carriedOut[y-1] = unused
	}
	if Y > st.carryYear {
		st.carryYear = Y
	}
}

// replay 重放所有 now <= t 的事件，并把结转补齐到 t 所在年度。
func (s *Service) replay(e *Emp, t int) *rstate {
	st := &rstate{
		carryYear:  yearOf(e.hireDay),
		carriedOut: make(map[int]int),
		usedCur:    make(map[int]int),
		usedCar:    make(map[int]int),
		pendCur:    make(map[int]int),
		pendCar:    make(map[int]int),
		voidedCar:  make(map[int]int),
		leaves:     make(map[int64]*rleave),
	}
	for _, ev := range e.events {
		if ev.now > t {
			break
		}
		s.materialize(e, st, yearOf(ev.now))
		s.apply(st, ev)
	}
	s.materialize(e, st, yearOf(t))
	return st
}

func (s *Service) apply(st *rstate, ev event) {
	switch ev.kind {
	case kRequest:
		l := &rleave{id: ev.id, from: ev.from, to: ev.to, status: leave.StatusPending, charges: ev.charges}
		for _, c := range ev.charges {
			if c.Source == leave.Carried {
				st.pendCar[c.Year]++
			} else {
				st.pendCur[c.Year]++
			}
		}
		st.leaves[ev.id] = l
	case kApprove:
		l := st.leaves[ev.id]
		for _, c := range l.charges {
			if c.Source == leave.Carried {
				st.pendCar[c.Year]--
				st.usedCar[c.Year]++
			} else {
				st.pendCur[c.Year]--
				st.usedCur[c.Year]++
			}
		}
		l.status = leave.StatusApproved
	case kReject, kWithdraw:
		l := st.leaves[ev.id]
		s.release(st, l.charges, ev.now, true)
		if ev.kind == kReject {
			l.status = leave.StatusRejected
		} else {
			l.status = leave.StatusWithdrawn
		}
	case kCancel:
		l := st.leaves[ev.id]
		s.release(st, l.charges, ev.now, false)
		l.status = leave.StatusCancelled
	case kEarlyEnd:
		l := st.leaves[ev.id]
		var kept, released []leave.Charge
		for _, c := range l.charges {
			if c.Day > ev.newEnd {
				released = append(released, c)
			} else {
				kept = append(kept, c)
			}
		}
		s.release(st, released, ev.now, false)
		l.charges = kept
		l.to = ev.newEnd
	}
}

func (s *Service) release(st *rstate, charges []leave.Charge, now int, pending bool) {
	for _, c := range charges {
		if c.Source == leave.Carried {
			if pending {
				st.pendCar[c.Year]--
			} else {
				st.usedCar[c.Year]--
			}
			if daysPerYear*c.Year+s.cfg.CarryDeadline < now {
				st.voidedCar[c.Year]++
			}
		} else {
			if pending {
				st.pendCur[c.Year]--
			} else {
				st.usedCur[c.Year]--
			}
		}
	}
}

// Register 登记员工。
func (s *Service) Register(now int, empID string, hireDay int) error {
	if now < 0 || empID == "" || hireDay < 0 {
		return errf(leave.CatInvalidParam, "bad register args")
	}
	if now < s.clock {
		return errf(leave.CatClockRegression, "now=%d < clock=%d", now, s.clock)
	}
	if _, ok := s.emps[empID]; ok {
		return errf(leave.CatInvalidState, "employee %q exists", empID)
	}
	s.emps[empID] = &Emp{hireDay: hireDay, regNow: now}
	s.clock = now
	return nil
}

// RequestLeave 提交请假申请。
func (s *Service) RequestLeave(now int, empID string, from, to int) (int64, []leave.Charge, error) {
	if now < 0 || empID == "" || from < 0 || to < from || from < now || to-from > 200*daysPerYear {
		return 0, nil, errf(leave.CatInvalidParam, "bad request args")
	}
	if now < s.clock {
		return 0, nil, errf(leave.CatClockRegression, "now=%d < clock=%d", now, s.clock)
	}
	e, ok := s.emps[empID]
	if !ok {
		return 0, nil, errf(leave.CatEmployeeNotFound, "employee %q", empID)
	}
	if from < e.hireDay {
		return 0, nil, errf(leave.CatInvalidParam, "from before hire day")
	}
	st := s.replay(e, now)
	// 线性扫描重叠
	for _, l := range st.leaves {
		if terminated(l.status) {
			continue
		}
		if l.from <= to && from <= l.to {
			return 0, nil, errf(leave.CatOverlap, "[%d,%d] overlaps leave %d", from, to, l.id)
		}
	}
	// 逐日试扣
	nowYear := yearOf(now)
	carUsed := make(map[int]int)
	curUsed := make(map[int]int)
	var charges []leave.Charge
	for d := from; d <= to; d++ {
		if s.nonWork[doyOf(d)] {
			continue
		}
		y := yearOf(d)
		carAvail := 0
		if y <= nowYear {
			carAvail = st.carriedOut[y-1] - st.usedCar[y] - st.pendCar[y] - carUsed[y]
		}
		curAvail := s.quota(e, y) - st.usedCur[y] - st.pendCur[y] - st.carriedOut[y] - curUsed[y]
		switch {
		case doyOf(d) <= s.cfg.CarryDeadline && carAvail > 0:
			carUsed[y]++
			charges = append(charges, leave.Charge{Day: d, Year: y, Source: leave.Carried})
		case curAvail > 0:
			curUsed[y]++
			charges = append(charges, leave.Charge{Day: d, Year: y, Source: leave.Current})
		default:
			return 0, nil, errf(leave.CatInsufficientQuota, "day %d", d)
		}
	}
	s.nextID++
	id := s.nextID
	e.events = append(e.events, event{now: now, kind: kRequest, id: id, from: from, to: to, charges: charges})
	s.clock = now
	return id, charges, nil
}

// withLeave 是携带假单操作的公共骨架。
func (s *Service) withLeave(now int, empID string, leaveID int64,
	check func(l *rleave) error, makeEvent func() event) error {
	if now < 0 || empID == "" || leaveID <= 0 {
		return errf(leave.CatInvalidParam, "bad args")
	}
	if now < s.clock {
		return errf(leave.CatClockRegression, "now=%d < clock=%d", now, s.clock)
	}
	e, ok := s.emps[empID]
	if !ok {
		return errf(leave.CatEmployeeNotFound, "employee %q", empID)
	}
	st := s.replay(e, now)
	l, ok := st.leaves[leaveID]
	if !ok {
		return errf(leave.CatInvalidState, "leave %d not found", leaveID)
	}
	if err := check(l); err != nil {
		return err
	}
	e.events = append(e.events, makeEvent())
	s.clock = now
	return nil
}

// Approve 批准假单。
func (s *Service) Approve(now int, empID string, leaveID int64) error {
	return s.withLeave(now, empID, leaveID, func(l *rleave) error {
		if l.status != leave.StatusPending {
			return errf(leave.CatInvalidState, "not pending")
		}
		return nil
	}, func() event {
		return event{now: now, kind: kApprove, id: leaveID}
	})
}

// Reject 驳回假单。
func (s *Service) Reject(now int, empID string, leaveID int64) error {
	return s.withLeave(now, empID, leaveID, func(l *rleave) error {
		if l.status != leave.StatusPending {
			return errf(leave.CatInvalidState, "not pending")
		}
		return nil
	}, func() event {
		return event{now: now, kind: kReject, id: leaveID}
	})
}

// Withdraw 撤回假单。
func (s *Service) Withdraw(now int, empID string, leaveID int64) error {
	return s.withLeave(now, empID, leaveID, func(l *rleave) error {
		if l.status != leave.StatusPending {
			return errf(leave.CatInvalidState, "not pending")
		}
		return nil
	}, func() event {
		return event{now: now, kind: kWithdraw, id: leaveID}
	})
}

// CancelLeave 整单销假。
func (s *Service) CancelLeave(now int, empID string, leaveID int64) error {
	return s.withLeave(now, empID, leaveID, func(l *rleave) error {
		if l.status != leave.StatusApproved {
			return errf(leave.CatInvalidState, "not approved")
		}
		if now >= l.from {
			return errf(leave.CatInvalidState, "already started")
		}
		return nil
	}, func() event {
		return event{now: now, kind: kCancel, id: leaveID}
	})
}

// EarlyEnd 提前结束。
func (s *Service) EarlyEnd(now int, empID string, leaveID int64, newEnd int) error {
	if newEnd < 0 {
		return errf(leave.CatInvalidParam, "negative newEnd")
	}
	return s.withLeave(now, empID, leaveID, func(l *rleave) error {
		if l.status != leave.StatusApproved {
			return errf(leave.CatInvalidState, "not approved")
		}
		if l.from > now {
			return errf(leave.CatInvalidState, "not started")
		}
		if newEnd < l.from || newEnd >= l.to {
			return errf(leave.CatInvalidParam, "newEnd out of range")
		}
		return nil
	}, func() event {
		return event{now: now, kind: kEarlyEnd, id: leaveID, newEnd: newEnd}
	})
}

// Balance 查询时刻 now 所在年度的余额。
func (s *Service) Balance(now int, empID string) (leave.Balance, error) {
	if now < 0 || empID == "" {
		return leave.Balance{}, errf(leave.CatInvalidParam, "bad args")
	}
	e, ok := s.emps[empID]
	if !ok || now < e.regNow {
		return leave.Balance{}, errf(leave.CatEmployeeNotFound, "employee %q", empID)
	}
	st := s.replay(e, now)
	y := yearOf(now)
	granted := s.quota(e, y)
	carriedIn := st.carriedOut[y-1]
	return leave.Balance{
		Year:             y,
		CurrentGranted:   granted,
		CurrentPending:   st.pendCur[y],
		CurrentUsed:      st.usedCur[y],
		CurrentAvailable: granted - st.usedCur[y] - st.pendCur[y] - st.carriedOut[y],
		CarriedGranted:   carriedIn,
		CarriedPending:   st.pendCar[y],
		CarriedUsed:      st.usedCar[y],
		CarriedAvailable: carriedIn - st.usedCar[y] - st.pendCar[y] - st.voidedCar[y],
	}, nil
}
