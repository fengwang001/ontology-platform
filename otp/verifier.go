// Package otp 实现基于时间步（TOTP 风格）的一次性口令验证器。
//
// 验证器容忍设备时钟偏差并校正漂移：每个用户维护偏移估计 d 与
// 已用水位 u，凡序号不大于 u 的时间步口令永远不能再次通过。
package otp

import (
	"errors"
	"sync"
	"time"
)

// Func 是外部提供的确定性口令函数，按（用户，时间步序号）算出口令。
// 验证器只负责比较，不关心口令的具体生成算法。
type Func func(user string, step int64) string

// Result 是单次口令验证的结论。
type Result int

const (
	// OK 验证通过。
	OK Result = iota
	// NotRegistered 用户未注册。
	NotRegistered
	// EmptyPassword 口令为空。
	EmptyPassword
	// Used 口令对应的时间步已使用（序号不大于已用水位 u）。
	Used
	// Wrong 口令错误（窗口内无任何匹配步）。
	Wrong
)

func (r Result) String() string {
	switch r {
	case OK:
		return "OK"
	case NotRegistered:
		return "NotRegistered"
	case EmptyPassword:
		return "EmptyPassword"
	case Used:
		return "Used"
	case Wrong:
		return "Wrong"
	default:
		return "Unknown"
	}
}

// 注册时可能返回的错误，彼此可区分。
var (
	ErrUserExists    = errors.New("otp: 用户已存在")
	ErrInvalidPeriod = errors.New("otp: 步长 P 必须为正整数")
	ErrInvalidRadius = errors.New("otp: 容忍半径 w 不能为负")
	ErrInvalidDrift  = errors.New("otp: 漂移上限 D 不能小于容忍半径 w")
)

// userState 是单个用户的验证状态。
type userState struct {
	period int64 // 步长 P（秒）
	w      int64 // 容忍半径
	drift  int64 // 漂移上限 D
	d      int64 // 偏移估计，初值 0
	u      int64 // 已用水位，初值 -1
}

// entry 是单个用户的并发单元，不同用户持有不同的锁，互不影响。
type entry struct {
	mu    sync.Mutex
	state userState
}

// Verifier 是一次性口令验证器，可并发调用。
type Verifier struct {
	otp Func

	mu    sync.Mutex // 仅保护 users 表本身
	users map[string]*entry
}

// New 创建一个以 otp 为口令来源的验证器。
func New(otp Func) *Verifier {
	return &Verifier{otp: otp, users: make(map[string]*entry)}
}

// Register 注册用户。步长 period 必须为正，0<=w<=drift。
// 任何拒绝都整体生效，不会留下半个用户，也不会改变已有用户状态。
func (v *Verifier) Register(user string, period, w, drift int64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.users[user]; ok {
		return ErrUserExists
	}
	if period <= 0 {
		return ErrInvalidPeriod
	}
	if w < 0 {
		return ErrInvalidRadius
	}
	if drift < w {
		return ErrInvalidDrift
	}
	v.users[user] = &entry{state: userState{
		period: period,
		w:      w,
		drift:  drift,
		d:      0,
		u:      -1,
	}}
	return nil
}

// Verify 校验用户在 now 时刻提交的口令。
//
// 失败原因按 未注册 -> 口令为空 -> 已使用 -> 口令错误 的顺序只报第一个。
// 验证失败不改变该用户的 d 与 u。
func (v *Verifier) Verify(user, password string, now time.Time) Result {
	v.mu.Lock()
	e, ok := v.users[user]
	v.mu.Unlock()
	if !ok {
		return NotRegistered
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if password == "" {
		return EmptyPassword
	}

	s := &e.state
	c := floorDiv(now.Unix(), s.period)

	// 窗口为 [c+d-w, c+d+w] 与 [c-D, c+D] 的交集。
	lo := max(c+s.d-s.w, c-s.drift)
	hi := min(c+s.d+s.w, c+s.drift)

	// 候选：窗口内序号大于 u 的步，取序号最小的匹配步。
	for t := max(lo, s.u+1); t <= hi; t++ {
		if v.otp(user, t) == password {
			s.u = t
			s.d = t - c
			return OK
		}
	}

	// 无候选匹配：窗口内若仍存在匹配步，其序号必然不大于 u，即口令已使用。
	for t := lo; t <= hi && t <= s.u; t++ {
		if v.otp(user, t) == password {
			return Used
		}
	}
	return Wrong
}

// State 返回用户当前的偏移估计 d 与已用水位 u，供测试与运维观测。
func (v *Verifier) State(user string) (d, u int64, ok bool) {
	v.mu.Lock()
	e, found := v.users[user]
	v.mu.Unlock()
	if !found {
		return 0, 0, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.d, e.state.u, true
}

// floorDiv 向下取整除法（Go 的 / 是向零取整）。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
