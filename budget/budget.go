// Package budget 实现按周期重置的用量预算账本。
//
// 每个主体注册时给定周期预算 Q、周期长度 P 与预留过期期限 X。
// 周期号为时刻除以 P 向下取整，各周期独立核算。
// 预留先占额度，结算按实际用量记入预留发生时的周期，释放取消占用。
package budget

import (
	"errors"
	"fmt"
	"sync"
)

// 可被 errors.Is 区分的拒绝原因。
var (
	ErrSubjectNotRegistered = errors.New("subject not registered")
	ErrNonPositiveParam     = errors.New("parameter must be positive")
	ErrNonPositiveAmount    = errors.New("reservation amount must be positive")
	ErrReservationNotFound  = errors.New("reservation not found")
	ErrAlreadySettled       = errors.New("reservation already settled")
	ErrAlreadyReleased      = errors.New("reservation already released")
	ErrNegativeUsage        = errors.New("usage must not be negative")
)

// InsufficientQuotaError 表示额度不足，Remaining 为该周期剩余可用量（透支时为 0）。
type InsufficientQuotaError struct {
	Remaining int64
}

func (e *InsufficientQuotaError) Error() string {
	return fmt.Sprintf("insufficient quota: remaining %d", e.Remaining)
}

type reservationState int

const (
	stateInflight reservationState = iota
	stateSettled
	stateReleased
)

type reservation struct {
	id       string
	subject  string
	period   int64
	amount   int64
	expireAt int64
	state    reservationState
}

type subject struct {
	quota      int64
	periodLen  int64
	expireSpan int64
	used       map[int64]int64
	res        map[string]*reservation
}

// Ledger 是并发安全的用量预算账本。
type Ledger struct {
	mu       sync.Mutex
	seq      int
	subjects map[string]*subject
}

// NewLedger 创建空账本。
func NewLedger() *Ledger {
	return &Ledger{subjects: make(map[string]*subject)}
}

// Register 注册主体，Q、P、X 必须为正。
func (l *Ledger) Register(name string, q, p, x int64) error {
	if q <= 0 {
		return fmt.Errorf("%w: Q=%d", ErrNonPositiveParam, q)
	}
	if p <= 0 {
		return fmt.Errorf("%w: P=%d", ErrNonPositiveParam, p)
	}
	if x <= 0 {
		return fmt.Errorf("%w: X=%d", ErrNonPositiveParam, x)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.subjects[name] = &subject{
		quota:      q,
		periodLen:  p,
		expireSpan: x,
		used:       make(map[int64]int64),
		res:        make(map[string]*reservation),
	}
	return nil
}

// inflightLocked 计算周期 period 在时刻 t 的在途预留总额（未结算、未释放且未过期）。
func (s *subject) inflightLocked(period, t int64) int64 {
	var sum int64
	for _, r := range s.res {
		if r.period == period && r.state == stateInflight && t < r.expireAt {
			sum += r.amount
		}
	}
	return sum
}

// Reserve 在时刻 t 为主体预留额度 r，成功返回全局顺序预留号。
//
// 拒绝原因按优先级只报第一个：主体未注册、r 非正、额度不足。
// 成功当且仅当该周期已用量加在途预留再加 r 不超过 Q。
func (l *Ledger) Reserve(name string, t, r int64) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.subjects[name]
	if !ok {
		return "", ErrSubjectNotRegistered
	}
	if r <= 0 {
		return "", ErrNonPositiveAmount
	}
	period := t / s.periodLen
	remaining := s.quota - s.used[period] - s.inflightLocked(period, t)
	if remaining < 0 {
		remaining = 0
	}
	if r > remaining {
		return "", &InsufficientQuotaError{Remaining: remaining}
	}
	l.seq++
	id := fmt.Sprintf("v%d", l.seq)
	s.res[id] = &reservation{
		id:       id,
		subject:  name,
		period:   period,
		amount:   r,
		expireAt: t + s.expireSpan,
		state:    stateInflight,
	}
	return id, nil
}

// Settle 按实际用量 u 结算预留，u 记入预留发生时的周期。
//
// 在途或已过期的预留都可结算；u 超过预留额的部分照常记入，
// 可能使该周期已用量超过 Q。拒绝原因按优先级：预留号不存在、
// 已结算、已释放、u 为负。
func (l *Ledger) Settle(id string, u int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, s := l.findLocked(id)
	if r == nil {
		return ErrReservationNotFound
	}
	if r.state == stateSettled {
		return ErrAlreadySettled
	}
	if r.state == stateReleased {
		return ErrAlreadyReleased
	}
	if u < 0 {
		return ErrNegativeUsage
	}
	r.state = stateSettled
	s.used[r.period] += u
	return nil
}

// Release 显式释放在途预留，使其不再占额度。
//
// 释放后不可再结算。拒绝原因按优先级：预留号不存在、已结算、已释放。
func (l *Ledger) Release(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, _ := l.findLocked(id)
	if r == nil {
		return ErrReservationNotFound
	}
	if r.state == stateSettled {
		return ErrAlreadySettled
	}
	if r.state == stateReleased {
		return ErrAlreadyReleased
	}
	r.state = stateReleased
	return nil
}

// Query 查询主体某周期在时刻 t 的已用量与在途预留量。
//
// 在途量只统计时刻 t 尚未过期（t < 预留时刻+X）的在途预留。
func (l *Ledger) Query(name string, period, t int64) (used, inflight int64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.subjects[name]
	if !ok {
		return 0, 0, ErrSubjectNotRegistered
	}
	return s.used[period], s.inflightLocked(period, t), nil
}

func (l *Ledger) findLocked(id string) (*reservation, *subject) {
	for _, s := range l.subjects {
		if r, ok := s.res[id]; ok {
			return r, s
		}
	}
	return nil, nil
}
