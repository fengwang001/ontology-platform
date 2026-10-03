// Package cbs 实现常带宽服务器（Constant Bandwidth Server）的带宽账本与零松弛回收器。
//
// 每台服务器含预算 Q 与周期 P，占用带宽 Q/P。服务器的状态是当前时钟 now 的
// 纯函数：有待处理工作量 w>0 时为就绪；w=0 且曾被唤醒时按零松弛判定式
// q·P >= (d-now)·Q 判定为已释放，否则为空闲占用；从未唤醒者为已释放。
// 就绪与空闲占用占用带宽，已释放不占。所有带宽比较均使用精确有理数，
// 零松弛判定使用 128 位乘积，绝不使用浮点。
package cbs

import (
	"errors"
	"fmt"
	"math/big"
	"math/bits"
	"sync"
)

const (
	// MaxServers 是账本可容纳的服务器上限。
	MaxServers = 32
	// MaxQP 是预算 Q 与周期 P 的上限（1 <= Q <= P <= MaxQP）。
	MaxQP = 1_000_000
	// MaxClock 是系统时钟的上限。
	MaxClock = 1_000_000_000_000_000
	// MaxWork 是单次 Wake 工作量的上限。
	MaxWork = 1_000_000_000
	// MaxBacklog 是单台服务器累积待处理工作量 w 的上限。
	MaxBacklog = 1_000_000_000_000_000
)

// 拒绝原因。校验顺序固定为：参数非法、编号不存在、时钟回退、带宽不足、
// 不可运行、非最早截止、占用中；同一次调用只报告第一个命中的原因。
var (
	ErrInvalidParam          = errors.New("cbs: 参数非法")
	ErrNotFound              = errors.New("cbs: 编号不存在")
	ErrClockRegression       = errors.New("cbs: 时钟回退")
	ErrInsufficientBandwidth = errors.New("cbs: 带宽不足")
	ErrNotRunnable           = errors.New("cbs: 不可运行")
	ErrNotEarliestDeadline   = errors.New("cbs: 非最早截止")
	ErrBusy                  = errors.New("cbs: 占用中")
	ErrDuplicateID           = errors.New("cbs: 编号重复")
	ErrCapacityFull          = errors.New("cbs: 容量已满")
)

// State 是服务器状态，为当前时钟的纯函数，不落盘。
type State int

const (
	// StateReleased 已释放，不占用带宽。
	StateReleased State = iota
	// StateReady 就绪（w>0），占用带宽。
	StateReady
	// StateIdleOccupying 空闲占用（w=0 且零松弛未到），占用带宽。
	StateIdleOccupying
)

func (s State) String() string {
	switch s {
	case StateReady:
		return "Ready"
	case StateIdleOccupying:
		return "IdleOccupying"
	default:
		return "Released"
	}
}

// Snapshot 是 State 查询返回的只读快照。
type Snapshot struct {
	ID        string
	State     State
	Q         uint64 // 预算
	P         uint64 // 周期
	Remaining uint64 // 剩余预算 q
	Deadline  uint64 // 截止期 d
	Backlog   uint64 // 待处理工作量 w
}

type server struct {
	id    string
	Q     uint64
	P     uint64
	q     uint64
	d     uint64
	w     uint64
	woken bool
}

// Ledger 是带宽账本。所有方法均可并发调用，效果等价于某个串行顺序。
type Ledger struct {
	mu      sync.Mutex
	now     uint64
	servers map[string]*server
}

// NewLedger 创建空账本，系统时钟为 0。
func NewLedger() *Ledger {
	return &Ledger{servers: make(map[string]*server)}
}

// Now 返回当前系统时钟。
func (l *Ledger) Now() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.now
}

// zeroLaxityReached 判定零松弛是否到达：q·P >= (d-now)·Q。
// 左侧 q·P <= 1e12 恒可用 64 位表示；右侧 (d-now)·Q 可达 1e21，
// 必须使用 128 位乘积比较。
func zeroLaxityReached(q, P, d, now, Q uint64) bool {
	if now >= d {
		return true
	}
	lhs := q * P
	hi, lo := bits.Mul64(d-now, Q)
	if hi != 0 {
		return false
	}
	return lhs >= lo
}

func stateOf(s *server, now uint64) State {
	if s.w > 0 {
		return StateReady
	}
	if !s.woken {
		return StateReleased
	}
	if zeroLaxityReached(s.q, s.P, s.d, now, s.Q) {
		return StateReleased
	}
	return StateIdleOccupying
}

// occupiedLocked 返回 now 时刻占用带宽之和（精确有理数，既约）。
func (l *Ledger) occupiedLocked(now uint64) *big.Rat {
	sum := new(big.Rat)
	for _, s := range l.servers {
		if stateOf(s, now) == StateReleased {
			continue
		}
		sum.Add(sum, new(big.Rat).SetFrac64(int64(s.Q), int64(s.P)))
	}
	return sum
}

// earliestReadyLocked 返回就绪者中截止期最小者，并列取编号字节序小者。
func (l *Ledger) earliestReadyLocked(now uint64) *server {
	var best *server
	for _, s := range l.servers {
		if stateOf(s, now) != StateReady {
			continue
		}
		if best == nil || s.d < best.d || (s.d == best.d && s.id < best.id) {
			best = s
		}
	}
	return best
}

// Add 注册服务器。只可能报参数非法、编号重复、容量已满。
func (l *Ledger) Add(id string, Q, P uint64) error {
	if id == "" || Q < 1 || Q > P || P > MaxQP {
		return ErrInvalidParam
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.servers[id]; ok {
		return ErrDuplicateID
	}
	if len(l.servers) >= MaxServers {
		return ErrCapacityFull
	}
	l.servers[id] = &server{id: id, Q: Q, P: P, q: Q}
	return nil
}

// Wake 唤醒服务器并注入工作量。
//
// 就绪者只累加 w；空闲占用者保持 q、d 不变，w 置为工作量；已释放者须先
// 接纳——其余占用带宽加自己不超过 1，才置 q=Q、d=now+P、w=工作量，否则
// 拒绝且保留旧 q、d。被拒绝时不改变任何状态与系统时钟。
func (l *Ledger) Wake(id string, now, work uint64) error {
	if id == "" || work < 1 || work > MaxWork || now > MaxClock {
		return ErrInvalidParam
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.servers[id]
	if !ok {
		return ErrNotFound
	}
	if now < l.now {
		return ErrClockRegression
	}
	switch stateOf(s, now) {
	case StateReady:
		if s.w+work > MaxBacklog {
			return fmt.Errorf("%w: 累积工作量超过 1e15", ErrInvalidParam)
		}
		s.w += work
	case StateIdleOccupying:
		s.w = work
	default: // StateReleased：先接纳
		total := l.occupiedLocked(now)
		total.Add(total, new(big.Rat).SetFrac64(int64(s.Q), int64(s.P)))
		if total.Cmp(big.NewRat(1, 1)) > 0 {
			return fmt.Errorf("%w: 占用 %s + %d/%d 超过 1",
				ErrInsufficientBandwidth, l.occupiedLocked(now).RatString(), s.Q, s.P)
		}
		s.q = s.Q
		s.d = now + s.P
		s.w = work
		s.woken = true
	}
	l.now = now
	return nil
}

// Run 自 now 起让该服务器连续运行 delta 个单位。它必须是就绪者中截止期
// 最小者（并列取编号字节序小者），且 1 <= delta <= w。每消耗 1 单位 q 减 1，
// q 归 0 时立即补满为 Q 且 d 加 P；结束后时钟为 now+delta，w 减 delta。
func (l *Ledger) Run(id string, now, delta uint64) error {
	if id == "" || delta < 1 || now > MaxClock {
		return ErrInvalidParam
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.servers[id]
	if !ok {
		return ErrNotFound
	}
	if now < l.now {
		return ErrClockRegression
	}
	if stateOf(s, now) != StateReady || delta > s.w {
		return ErrNotRunnable
	}
	if earliest := l.earliestReadyLocked(now); earliest != s {
		return fmt.Errorf("%w: 最早截止者为 %s", ErrNotEarliestDeadline, earliest.id)
	}
	if delta < s.q {
		s.q -= delta
	} else {
		rem := delta - s.q
		refills := 1 + rem/s.Q
		s.d += refills * s.P
		if r := rem % s.Q; r == 0 {
			s.q = s.Q
		} else {
			s.q = s.Q - r
		}
	}
	s.w -= delta
	l.now = now + delta
	return nil
}

// Remove 删除服务器，仅已释放者可删，否则报占用中。
func (l *Ledger) Remove(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.servers[id]
	if !ok {
		return ErrNotFound
	}
	if stateOf(s, l.now) != StateReleased {
		return ErrBusy
	}
	delete(l.servers, id)
	return nil
}

// State 返回服务器快照，状态按当前时钟判定。
func (l *Ledger) State(id string) (Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.servers[id]
	if !ok {
		return Snapshot{}, ErrNotFound
	}
	return Snapshot{
		ID:        s.id,
		State:     stateOf(s, l.now),
		Q:         s.Q,
		P:         s.P,
		Remaining: s.q,
		Deadline:  s.d,
		Backlog:   s.w,
	}, nil
}

// Total 返回当前占用带宽之和的既约分数。
func (l *Ledger) Total() *big.Rat {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.occupiedLocked(l.now)
}

// Next 返回就绪者中截止期最小者（并列取编号字节序小者）。
func (l *Ledger) Next() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.earliestReadyLocked(l.now); s != nil {
		return s.id, true
	}
	return "", false
}
