// Package budget 实现按周期重置的用量预算账本。
package budget

import (
	"fmt"
	"sync"
)

// Reason 是可区分的拒绝原因。
type Reason string

const (
	ReasonSubjectNotRegistered     Reason = "SUBJECT_NOT_REGISTERED"
	ReasonSubjectAlreadyRegistered Reason = "SUBJECT_ALREADY_REGISTERED"
	ReasonInvalidPeriodConfig      Reason = "INVALID_PERIOD_CONFIG"
	ReasonNonPositiveAmount        Reason = "NON_POSITIVE_AMOUNT"
	ReasonInsufficientQuota        Reason = "INSUFFICIENT_QUOTA"
	ReasonReservationNotFound      Reason = "RESERVATION_NOT_FOUND"
	ReasonAlreadySettled           Reason = "ALREADY_SETTLED"
	ReasonAlreadyReleased          Reason = "ALREADY_RELEASED"
	ReasonNegativeUsage            Reason = "NEGATIVE_USAGE"
)

// Error 描述一次被拒绝的操作；被拒绝的操作不改变任何账目。
type Error struct {
	Reason Reason
	// Remaining 仅在 Reason 为 INSUFFICIENT_QUOTA 时有效，
	// 表示该周期剩余可预留额度（透支时为 0）。
	Remaining int64
	Detail    string
}

func (e *Error) Error() string {
	if e.Reason == ReasonInsufficientQuota {
		return fmt.Sprintf("%s: remaining=%d %s", e.Reason, e.Remaining, e.Detail)
	}
	return fmt.Sprintf("%s: %s", e.Reason, e.Detail)
}

type reservationState int

const (
	stateInflight reservationState = iota
	stateSettled
	stateReleased
)

type reservation struct {
	id        string
	subject   string
	period    int64
	amount    int64
	expiresAt int64
	state     reservationState
}

type subjectConfig struct {
	quota  int64
	period int64
	expiry int64
}

// Ledger 是并发安全的周期预算账本。
type Ledger struct {
	mu           sync.Mutex
	subjects     map[string]subjectConfig
	reservations map[string]*reservation
	byPeriod     map[string]map[int64][]*reservation
	used         map[string]map[int64]int64
	counter      int64
}

// NewLedger 创建空账本。
func NewLedger() *Ledger {
	return &Ledger{
		subjects:     make(map[string]subjectConfig),
		reservations: make(map[string]*reservation),
		byPeriod:     make(map[string]map[int64][]*reservation),
		used:         make(map[string]map[int64]int64),
	}
}

// Register 注册主体；Q、P、X 任一非正则拒绝。
func (l *Ledger) Register(subject string, quota, periodLen, expiry int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if quota <= 0 || periodLen <= 0 || expiry <= 0 {
		return &Error{Reason: ReasonInvalidPeriodConfig,
			Detail: fmt.Sprintf("subject=%q Q=%d P=%d X=%d 必须均为正整数", subject, quota, periodLen, expiry)}
	}
	if _, ok := l.subjects[subject]; ok {
		return &Error{Reason: ReasonSubjectAlreadyRegistered,
			Detail: fmt.Sprintf("subject=%q 已注册", subject)}
	}
	l.subjects[subject] = subjectConfig{quota: quota, period: periodLen, expiry: expiry}
	return nil
}

// Reserve 在 now 时刻为主体预留额度 r，成功返回全局顺序预留号（v1、v2……）。
// 拒绝原因按 主体未注册 -> r 非正 -> 额度不足 的顺序只报第一个。
func (l *Ledger) Reserve(subject string, r, now int64) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cfg, ok := l.subjects[subject]
	if !ok {
		return "", &Error{Reason: ReasonSubjectNotRegistered,
			Detail: fmt.Sprintf("subject=%q 未注册", subject)}
	}
	if r <= 0 {
		return "", &Error{Reason: ReasonNonPositiveAmount,
			Detail: fmt.Sprintf("subject=%q r=%d 必须为正整数", subject, r)}
	}
	period := floorDiv(now, cfg.period)
	used := l.used[subject][period]
	inflight := l.inflightLocked(subject, period, now)
	remaining := cfg.quota - used - inflight
	if remaining < r {
		report := remaining
		if report < 0 {
			report = 0
		}
		return "", &Error{Reason: ReasonInsufficientQuota, Remaining: report,
			Detail: fmt.Sprintf("subject=%q period=%d used=%d inflight=%d r=%d Q=%d",
				subject, period, used, inflight, r, cfg.quota)}
	}
	l.counter++
	res := &reservation{
		id:        fmt.Sprintf("v%d", l.counter),
		subject:   subject,
		period:    period,
		amount:    r,
		expiresAt: now + cfg.expiry,
		state:     stateInflight,
	}
	l.reservations[res.id] = res
	if l.byPeriod[subject] == nil {
		l.byPeriod[subject] = make(map[int64][]*reservation)
	}
	l.byPeriod[subject][period] = append(l.byPeriod[subject][period], res)
	if l.used[subject] == nil {
		l.used[subject] = make(map[int64]int64)
	}
	return res.id, nil
}

// Settle 结算预留：把实际用量 u 记入预留所属周期的已用量。
// 在途或已过期的预留都可结算；u 超过 r 的部分照常记入。
// 拒绝原因按 预留号不存在 -> 已结算 -> 已释放 -> u 为负 的顺序只报第一个。
func (l *Ledger) Settle(id string, u int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	res, ok := l.reservations[id]
	if !ok {
		return &Error{Reason: ReasonReservationNotFound,
			Detail: fmt.Sprintf("reservation=%q 不存在", id)}
	}
	if res.state == stateSettled {
		return &Error{Reason: ReasonAlreadySettled,
			Detail: fmt.Sprintf("reservation=%q 已结算", id)}
	}
	if res.state == stateReleased {
		return &Error{Reason: ReasonAlreadyReleased,
			Detail: fmt.Sprintf("reservation=%q 已释放", id)}
	}
	if u < 0 {
		return &Error{Reason: ReasonNegativeUsage,
			Detail: fmt.Sprintf("reservation=%q u=%d 不能为负", id, u)}
	}
	res.state = stateSettled
	l.used[res.subject][res.period] += u
	return nil
}

// Release 显式释放预留，使其不再占额度，此后不可再结算。
// 拒绝原因按 预留号不存在 -> 已结算 -> 已释放 的顺序只报第一个。
func (l *Ledger) Release(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	res, ok := l.reservations[id]
	if !ok {
		return &Error{Reason: ReasonReservationNotFound,
			Detail: fmt.Sprintf("reservation=%q 不存在", id)}
	}
	if res.state == stateSettled {
		return &Error{Reason: ReasonAlreadySettled,
			Detail: fmt.Sprintf("reservation=%q 已结算", id)}
	}
	if res.state == stateReleased {
		return &Error{Reason: ReasonAlreadyReleased,
			Detail: fmt.Sprintf("reservation=%q 已释放", id)}
	}
	res.state = stateReleased
	return nil
}

// Query 查询主体某周期在 now 时刻的已用量与在途预留量。
func (l *Ledger) Query(subject string, period, now int64) (used, inflight int64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.subjects[subject]; !ok {
		return 0, 0, &Error{Reason: ReasonSubjectNotRegistered,
			Detail: fmt.Sprintf("subject=%q 未注册", subject)}
	}
	return l.used[subject][period], l.inflightLocked(subject, period, now), nil
}

// inflightLocked 计算某周期在 now 时刻的在途预留总额：
// 状态为在途且尚未到期（now < expiresAt，恰到点即过期）。
func (l *Ledger) inflightLocked(subject string, period, now int64) int64 {
	var sum int64
	for _, res := range l.byPeriod[subject][period] {
		if res.state == stateInflight && now < res.expiresAt {
			sum += res.amount
		}
	}
	return sum
}

// floorDiv 向下取整除法，用于计算周期号。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
