// Package ontology 提供 guard：计数滑窗熔断状态机与舱壁排队的统一编排入口。
package ontology

import (
	"errors"
	"fmt"
	"sync"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/ring"
)

// AcquireOutcome 为 Acquire 的结局类别。
type AcquireOutcome int

const (
	Granted AcquireOutcome = iota
	Queued
	RejectedOpen
	RejectedHalfOpenFull
	RejectedFull
)

// IDStatus 为已发编号 id 的最终/当前结局。
type IDStatus int

const (
	StatusQueued IDStatus = iota + 1
	StatusActive
	StatusReleased
	StatusTimedOut
	StatusCancelled
)

// 错误哨兵：按规定顺序只报第一个错误。
var (
	ErrInvalidArgument = errors.New("guard: invalid argument")
	ErrInvalidTime     = errors.New("guard: invalid time")
	ErrClockRollback   = errors.New("guard: clock rollback")
	ErrPermitNotActive = errors.New("guard: id is not an active permit")
	ErrUnknownID       = errors.New("guard: unknown id")
)

// Params 为守卫的全部构造参数（毫秒均为 int64）。
type Params struct {
	N, M, F, SR int
	S, O, Wt    int64
	H, C, Q     int
}

// AcquireResult 为 Acquire 的返回；拒绝时 ID 为 0。
type AcquireResult struct {
	Outcome AcquireOutcome
	ID      int64
}

// Snapshot 为可观测快照。
type Snapshot struct {
	State  breaker.State
	Epoch  int64
	Count  int
	Failed int
	Slow   int
	Active int
	Queued int
}

type idState int

const (
	stQueued idState = iota + 1
	stActive
	stReleased
	stTimedOut
	stCancelled
)

type idRecord struct {
	state idState
	epoch int64 // 成为在役（Granted，含排队转授予）时刻的纪元
}

// Guard 为下游调用守卫。单把互斥锁串行化全部操作，
// 因而并发结果等价于某个与墙上时间一致的串行顺序。
type Guard struct {
	mu      sync.Mutex
	brk     *breaker.Breaker
	bh      *bulkhead.Bulkhead
	wt      int64
	slowAt  int64
	maxNow  int64
	nextID  int64
	records map[int64]*idRecord
}

// New 按参数构造守卫；参数越界返回 ErrInvalidArgument（包裹具体原因）。
func New(p Params) (*Guard, error) {
	if !(1 <= p.M && p.M <= p.N && p.N <= 1000) {
		return nil, fmt.Errorf("%w: require 1<=M<=N<=1000, got N=%d M=%d", ErrInvalidArgument, p.N, p.M)
	}
	if !(1 <= p.F && p.F <= 100) {
		return nil, fmt.Errorf("%w: F must be 1..100, got %d", ErrInvalidArgument, p.F)
	}
	if !(1 <= p.SR && p.SR <= 100) {
		return nil, fmt.Errorf("%w: SR must be 1..100, got %d", ErrInvalidArgument, p.SR)
	}
	for name, v := range map[string]int64{"S": p.S, "O": p.O, "Wt": p.Wt} {
		if !(1 <= v && v <= 1_000_000_000) {
			return nil, fmt.Errorf("%w: %s must be 1..1e9 ms, got %d", ErrInvalidArgument, name, v)
		}
	}
	if !(1 <= p.H && p.H <= 100) {
		return nil, fmt.Errorf("%w: H must be 1..100, got %d", ErrInvalidArgument, p.H)
	}
	if !(1 <= p.C && p.C <= 1000) {
		return nil, fmt.Errorf("%w: C must be 1..1000, got %d", ErrInvalidArgument, p.C)
	}
	if !(0 <= p.Q && p.Q <= 1000) {
		return nil, fmt.Errorf("%w: Q must be 0..1000, got %d", ErrInvalidArgument, p.Q)
	}
	return &Guard{
		brk: breaker.New(breaker.Config{
			N: p.N, M: p.M, F: p.F, SR: p.SR, H: p.H, S: p.S, O: p.O,
		}),
		bh:      bulkhead.New(p.C, p.Q),
		wt:      p.Wt,
		slowAt:  p.S,
		nextID:  1,
		records: make(map[int64]*idRecord),
	}, nil
}

// validateTime 校验时间合法性（0..1e15）与单调不回退，但不改变任何状态。
func (g *Guard) validateTime(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return fmt.Errorf("%w: now=%d out of [0,1e15]", ErrInvalidTime, now)
	}
	if now < g.maxNow {
		return fmt.Errorf("%w: now=%d < max=%d", ErrClockRollback, now, g.maxNow)
	}
	return nil
}

// commitTime 在校验通过后推进最大 now。
func (g *Guard) commitTime(now int64) {
	g.maxNow = now
}

// settle 先按入队序结算超时等待者，再做 Open→HalfOpen 定时迁移。
func (g *Guard) settle(now int64) {
	g.bh.SettleTimeouts(now, g.wt, func(id int64) {
		g.records[id].state = stTimedOut
	})
	g.brk.Settle(now)
}

func (g *Guard) issueID() int64 {
	id := g.nextID
	g.nextID++
	return id
}

// Acquire 在时刻 now 申请下游调用：先结算，再按熔断状态与许可决定放行/排队/拒绝。
// 拒绝是正常结果而非错误；结算效果保留并推进最大 now。
func (g *Guard) Acquire(now int64) (AcquireResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.validateTime(now); err != nil {
		return AcquireResult{}, err
	}
	g.commitTime(now)
	g.settle(now)

	switch g.brk.State() {
	case breaker.Open:
		return AcquireResult{Outcome: RejectedOpen}, nil
	case breaker.HalfOpen:
		if g.brk.ProbeIssued() >= g.brk.ProbeLimit() {
			return AcquireResult{Outcome: RejectedHalfOpenFull}, nil
		}
		if g.bh.Free() > 0 {
			id := g.issueID()
			g.records[id] = &idRecord{state: stActive, epoch: g.brk.Epoch()}
			g.bh.Acquire()
			g.brk.IssueProbe()
			return AcquireResult{Outcome: Granted, ID: id}, nil
		}
		// HalfOpen 不入队；探测名额有余但无空闲许可按 Full 拒绝。
		return AcquireResult{Outcome: RejectedFull}, nil
	default: // Closed
		if g.bh.Free() > 0 {
			id := g.issueID()
			g.records[id] = &idRecord{state: stActive, epoch: g.brk.Epoch()}
			g.bh.Acquire()
			return AcquireResult{Outcome: Granted, ID: id}, nil
		}
		if g.bh.QueueLen() < g.bh.QueueCap() {
			id := g.issueID()
			g.records[id] = &idRecord{state: stQueued}
			g.bh.Enqueue(id, now)
			return AcquireResult{Outcome: Queued, ID: id}, nil
		}
		return AcquireResult{Outcome: RejectedFull}, nil
	}
}

// Release 归还 id。错误顺序：参数非法（id<=0、dur<0）、时间非法、时钟回退、
// id 非在役。错误不改变任何状态。
func (g *Guard) Release(id int64, ok bool, dur, now int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id <= 0 {
		return fmt.Errorf("%w: id=%d", ErrInvalidArgument, id)
	}
	if dur < 0 {
		return fmt.Errorf("%w: dur=%d", ErrInvalidArgument, dur)
	}
	if err := g.validateTime(now); err != nil {
		return err
	}
	rec, known := g.records[id]
	if !known || rec.state != stActive {
		return fmt.Errorf("%w: id=%d", ErrPermitNotActive, id)
	}

	g.commitTime(now)
	g.settle(now)
	permEpoch := rec.epoch
	rec.state = stReleased
	g.bh.Release()

	// 仅发放纪元等于当前纪元的结果计入统计；旧纪元许可只归还。
	if permEpoch == g.brk.Epoch() {
		switch g.brk.State() {
		case breaker.Closed:
			slow := dur >= g.slowAt
			g.brk.Ring().Add(ring.Entry{Failed: !ok, Slow: slow})
			if g.brk.ShouldTrip() {
				g.trip(now)
			}
		case breaker.HalfOpen:
			if !ok || dur >= g.slowAt {
				g.trip(now)
			} else if g.brk.IncProbeSuccess() {
				// 已转 Closed
			}
		}
	}

	// 仍为 Closed 时，空闲许可按入队序授予队首等待者。
	if g.brk.State() == breaker.Closed {
		g.bh.TryFill(now,
			func() bool { return g.brk.State() == breaker.Closed },
			func(qid int64) {
				g.records[qid].state = stActive
				g.records[qid].epoch = g.brk.Epoch()
			})
	}
	return nil
}

// trip 开路：迁移状态机并按入队序撤销全部排队者。
func (g *Guard) trip(now int64) {
	g.brk.Trip(now)
	g.bh.CancelAll(func(id int64) {
		g.records[id].state = stCancelled
	})
}

// Status 在时刻 now 查询 id 结局；id 从未发出返回 ErrUnknownID。
func (g *Guard) Status(id int64, now int64) (IDStatus, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id <= 0 {
		return 0, fmt.Errorf("%w: id=%d", ErrInvalidArgument, id)
	}
	if err := g.validateTime(now); err != nil {
		return 0, err
	}
	rec, known := g.records[id]
	if !known {
		return 0, fmt.Errorf("%w: id=%d", ErrUnknownID, id)
	}
	g.commitTime(now)
	g.settle(now)
	switch rec.state {
	case stQueued:
		return StatusQueued, nil
	case stActive:
		return StatusActive, nil
	case stReleased:
		return StatusReleased, nil
	case stTimedOut:
		return StatusTimedOut, nil
	default:
		return StatusCancelled, nil
	}
}

// Snapshot 在结算后返回状态、纪元、环计数与舱壁账目。
func (g *Guard) Snapshot(now int64) (Snapshot, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.validateTime(now); err != nil {
		return Snapshot{}, err
	}
	g.commitTime(now)
	g.settle(now)
	win := g.brk.Ring()
	return Snapshot{
		State:  g.brk.State(),
		Epoch:  g.brk.Epoch(),
		Count:  win.Count(),
		Failed: win.Failed(),
		Slow:   win.Slow(),
		Active: g.bh.Active(),
		Queued: g.bh.QueueLen(),
	}, nil
}
