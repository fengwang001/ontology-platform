// Package idpool 提供带隔离期与短命抖动加长的编号池。
package idpool

import (
	"errors"
	"sync"
)

// 可区分的参数错误。
var (
	ErrInvalidN      = errors.New("idpool: N must be positive")
	ErrInvalidQ      = errors.New("idpool: Q must be positive")
	ErrInvalidQmax   = errors.New("idpool: Qmax must be >= Q")
	ErrInvalidR      = errors.New("idpool: R must be positive")
	ErrClockRollback = errors.New("idpool: time must not go backwards")
	ErrIDOutOfRange  = errors.New("idpool: id out of range")
	ErrIDFree        = errors.New("idpool: id is free")
	ErrIDQuarantined = errors.New("idpool: id is quarantined")
)

// AllocError 表示无空闲编号导致的分配失败。
type AllocError struct {
	// Exhausted 为 true 时池中编号全部使用中；
	// 为 false 时至少有一个编号处于隔离中。
	Exhausted bool
	// NextFreeAt 最早解除隔离的时刻（仅隔离中存在时有效）。
	NextFreeAt int64
	// NextFreeID 该时刻解除隔离的编号中最小者（仅隔离中存在时有效）。
	NextFreeID int
}

func (e *AllocError) Error() string { return "idpool: no free id" }

type slotState uint8

const (
	stateFree slotState = iota
	stateInUse
	stateQuarantined
)

type slot struct {
	state   slotState
	allocAt int64 // 分配时刻（stateInUse）
	freeAt  int64 // 隔离解除时刻（stateQuarantined）
	lastQ   int64 // 最近一次释放所用的隔离期；初始为 Q
}

// Pool 是编号 1..N 的隔离编号池，零值不可用，请用 New 创建。
// 所有时刻均为整数毫秒，由调用方保证时钟单调；池会检测时钟回拨。
type Pool struct {
	mu      sync.Mutex
	n       int
	q       int64
	qmax    int64
	r       int64
	lastNow int64
	slots   []slot
}

// New 创建编号池。
// n 为编号上界（编号 1..n）；q 为基础隔离期，qmax 为隔离上限，
// r 为短命阈值（存活时间 < r 视为短命；恰好等于 r 算正常寿命）。
func New(n int, q, qmax, r int64) (*Pool, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	if q <= 0 {
		return nil, ErrInvalidQ
	}
	if qmax < q {
		return nil, ErrInvalidQmax
	}
	if r <= 0 {
		return nil, ErrInvalidR
	}
	slots := make([]slot, n+1)
	for i := 1; i <= n; i++ {
		slots[i].lastQ = q
	}
	return &Pool{
		n:     n,
		q:     q,
		qmax:  qmax,
		r:     r,
		slots: slots,
	}, nil
}

func (p *Pool) checkTime(nowMs int64) error {
	if nowMs < p.lastNow {
		return ErrClockRollback
	}
	return nil
}

// mature 将所有隔离已到期（freeAt <= now）的编号转为空闲。
// 调用方需持有 mu。
func (p *Pool) mature(nowMs int64) {
	for i := 1; i <= p.n; i++ {
		s := &p.slots[i]
		if s.state == stateQuarantined && s.freeAt <= nowMs {
			s.state = stateFree
			s.freeAt = 0
		}
	}
}

// earliestQuarantine 返回隔离中编号的最早解除时刻，
// 并列时取最小编号；无隔离编号时 ok 为 false。
// 调用方需持有 mu。
func (p *Pool) earliestQuarantine() (at int64, id int, ok bool) {
	for i := 1; i <= p.n; i++ {
		s := &p.slots[i]
		if s.state != stateQuarantined {
			continue
		}
		if !ok || s.freeAt < at || (s.freeAt == at && i < id) {
			at, id, ok = s.freeAt, i, true
		}
	}
	return at, id, ok
}

// Allocate 在时刻 nowMs 分配当前空闲的最小编号。
// 若没有空闲编号：
//   - 存在隔离中的编号：返回 *AllocError（Exhausted=false），
//     并报告其中最早解除的时刻与编号（并列取小编号）；
//   - 否则池已被全部占用：返回 *AllocError（Exhausted=true）。
//
// 绝不会提前借出隔离中的编号。
func (p *Pool) Allocate(nowMs int64) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.checkTime(nowMs); err != nil {
		return 0, err
	}
	p.lastNow = nowMs

	p.mature(nowMs)

	for i := 1; i <= p.n; i++ {
		if p.slots[i].state == stateFree {
			p.slots[i].state = stateInUse
			p.slots[i].allocAt = nowMs
			return i, nil
		}
	}

	at, id, ok := p.earliestQuarantine()
	err := &AllocError{Exhausted: !ok}
	if ok {
		err.NextFreeAt, err.NextFreeID = at, id
	}
	return 0, err
}

// Release 在时刻 nowMs 释放使用中的编号 id。
// 存活时间 = nowMs - 分配时刻：
//   - 存活时间 < r（短命）：本次隔离期 = min(2*该编号上次隔离期, qmax)；
//   - 存活时间 >= r（正常寿命，恰好等于 r 也算）：本次隔离期 = q。
//
// 编号在 nowMs + 隔离期 起变为空闲（恰到该时刻即可再分配）。
// 校验顺序：时钟回拨优先，随后按越界、空闲、隔离中只报第一个；
// 被拒绝的操作不改变任何编号状态与隔离记录。
func (p *Pool) Release(id int, nowMs int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.checkTime(nowMs); err != nil {
		return err
	}
	p.lastNow = nowMs

	if id < 1 || id > p.n {
		return ErrIDOutOfRange
	}

	s := &p.slots[id]

	// 隔离已到期的编号在本时刻即视为空闲。
	effective := s.state
	if effective == stateQuarantined && s.freeAt <= nowMs {
		effective = stateFree
	}
	switch effective {
	case stateFree:
		return ErrIDFree
	case stateQuarantined:
		return ErrIDQuarantined
	}

	lived := nowMs - s.allocAt
	var qq int64
	if lived < p.r {
		qq = 2 * s.lastQ
		if qq > p.qmax {
			qq = p.qmax
		}
	} else {
		qq = p.q
	}
	s.lastQ = qq
	s.state = stateQuarantined
	s.freeAt = nowMs + qq
	s.allocAt = 0
	return nil
}

// Stats 为某一时刻的池快照。
type Stats struct {
	InUse         int
	Quarantined   int
	Free          int
	NextFreeAt    int64
	NextFreeID    int
	HasQuarantine bool
}

// Query 返回时刻 nowMs 的池状态。
// Query 不会提前借出编号，仅推进内部对到期隔离的认知。
func (p *Pool) Query(nowMs int64) (Stats, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.checkTime(nowMs); err != nil {
		return Stats{}, err
	}
	p.lastNow = nowMs

	p.mature(nowMs)

	var st Stats
	for i := 1; i <= p.n; i++ {
		switch p.slots[i].state {
		case stateInUse:
			st.InUse++
		case stateQuarantined:
			st.Quarantined++
		default:
			st.Free++
		}
	}
	if at, id, ok := p.earliestQuarantine(); ok {
		st.HasQuarantine = true
		st.NextFreeAt, st.NextFreeID = at, id
	}
	return st, nil
}
