package ontology

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// 注册被整体拒绝时返回的可区分原因。被拒绝的操作不会改变 d 与 u。
var (
	// ErrUserExists 用户已注册。
	ErrUserExists = errors.New("user already registered")
	// ErrInvalidPeriod 步长 P 必须为正整数。
	ErrInvalidPeriod = errors.New("period P must be positive")
	// ErrNegativeWindow 容忍半径 w 不能为负。
	ErrNegativeWindow = errors.New("window radius w must be non-negative")
	// ErrDriftBelowWindow 漂移上限 D 不能小于容忍半径 w。
	ErrDriftBelowWindow = errors.New("drift bound D must be >= w")
)

// VerifyOutcome 是验证失败（或成功）的判定原因。
type VerifyOutcome int

const (
	// OutcomeOK 口令通过，且对应时间步已被消费。
	OutcomeOK VerifyOutcome = iota
	// OutcomeUserNotFound 用户未注册（最先报告）。
	OutcomeUserNotFound
	// OutcomeEmptyCode 口令为空（在用户存在之后才检查）。
	OutcomeEmptyCode
	// OutcomeCodeUsed 窗口内有匹配步，但全部不大于水位 u，口令已使用过。
	OutcomeCodeUsed
	// OutcomeCodeWrong 窗口内不存在任何匹配步，口令错误。
	OutcomeCodeWrong
)

// String 返回判定原因的可读名称。
func (o VerifyOutcome) String() string {
	switch o {
	case OutcomeOK:
		return "ok"
	case OutcomeUserNotFound:
		return "user-not-found"
	case OutcomeEmptyCode:
		return "empty-code"
	case OutcomeCodeUsed:
		return "code-used"
	case OutcomeCodeWrong:
		return "code-wrong"
	default:
		return fmt.Sprintf("outcome-%d", int(o))
	}
}

// CodeFunc 是外部提供的确定性口令函数：给定用户与时间步序号返回口令。
// 验证器只做比较，不关心其具体算法。
type CodeFunc func(user string, step int64) string

// userState 为单个用户维护的全部状态：注册参数、偏移估计 d、已用水位 u。
type userState struct {
	p int64 // 步长（秒）
	w int64 // 容忍半径（步）
	D int64 // 漂移上限（步）
	d int64 // 偏移估计，初值 0
	u int64 // 已用水位，初值 -1
}

// Verifier 是基于时间步的一次性口令验证器。零值不可用，请使用 NewVerifier。
type Verifier struct {
	mu     sync.Mutex
	users  map[string]*userState
	codes  CodeFunc
	now    func() time.Time
	logger *log.Logger
}

// NewVerifier 创建验证器。codes 为确定性口令函数，时钟默认为墙钟时间。
func NewVerifier(codes CodeFunc) *Verifier {
	return &Verifier{
		users:  make(map[string]*userState),
		codes:  codes,
		now:    time.Now,
		logger: log.Default(),
	}
}

// SetClock 替换时间源（便于测试注入固定时钟）。
func (v *Verifier) SetClock(fn func() time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.now = fn
}

// SetLogger 替换判定日志的输出目标；传 nil 关闭日志。
func (v *Verifier) SetLogger(l *log.Logger) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.logger = l
}

// Register 注册用户。按 用户已存在、P 非正、w 为负、D<w 的顺序只报第一个
// 可区分原因；任何拒绝都不会写入或修改用户状态。
func (v *Verifier) Register(user string, p, w, d int64) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if _, exists := v.users[user]; exists {
		v.logf("register input user=%q p=%d w=%d D=%d -> output rejected: %s",
			user, p, w, d, ErrUserExists)
		return ErrUserExists
	}
	if p <= 0 {
		v.logf("register input user=%q p=%d w=%d D=%d -> output rejected: %s",
			user, p, w, d, ErrInvalidPeriod)
		return ErrInvalidPeriod
	}
	if w < 0 {
		v.logf("register input user=%q p=%d w=%d D=%d -> output rejected: %s",
			user, p, w, d, ErrNegativeWindow)
		return ErrNegativeWindow
	}
	if d < w {
		v.logf("register input user=%q p=%d w=%d D=%d -> output rejected: %s",
			user, p, w, d, ErrDriftBelowWindow)
		return ErrDriftBelowWindow
	}

	v.users[user] = &userState{p: p, w: w, D: d, d: 0, u: -1}
	v.logf("register input user=%q p=%d w=%d D=%d -> output accepted: d=0 u=-1",
		user, p, w, d)
	return nil
}

// VerifyResult 描述一次验证的结果与判定依据。
type VerifyResult struct {
	// OK 为 true 当且仅当口令在未使用的候选步上匹配成功。
	OK bool
	// Reason 判定原因。
	Reason VerifyOutcome
	// CurrentStep 本次判定使用的当前步 c。
	CurrentStep int64
	// WindowLo、WindowHi 为实际搜索窗口（两个区间的交集）。
	WindowLo int64
	WindowHi int64
	// MatchedStep 为命中的时间步；失败时为 0。
	MatchedStep int64
	// Drift 为成功后更新的偏移估计 d；失败时为判定前的值。
	Drift int64
	// Watermark 为判定后的已用水位 u；失败时保持原值（单调不减）。
	Watermark int64
}

// Verify 校验口令。整个判定在锁内完成，因此可被并发调用：
// 同一用户同一口令并发提交时恰有一个通过，其余得到 OutcomeCodeUsed。
func (v *Verifier) Verify(user, code string) VerifyResult {
	v.mu.Lock()
	defer v.mu.Unlock()

	st, ok := v.users[user]
	if !ok {
		res := VerifyResult{OK: false, Reason: OutcomeUserNotFound, Watermark: -1}
		v.logf("verify input user=%q code=%q -> output ok=false reason=%s basis=user-not-registered",
			user, code, res.Reason)
		return res
	}

	c := v.now().Unix() / st.p

	// 窗口：容忍窗口 [c+d-w, c+d+w] 与漂移硬界 [c-D, c+D] 的交集。
	lo := c + st.d - st.w
	if bound := c - st.D; bound > lo {
		lo = bound
	}
	hi := c + st.d + st.w
	if bound := c + st.D; bound < hi {
		hi = bound
	}

	res := VerifyResult{
		OK:          false,
		CurrentStep: c,
		WindowLo:    lo,
		WindowHi:    hi,
		Drift:       st.d,
		Watermark:   st.u,
	}

	// 空口令在确认用户存在、窗口算出之后才检查（顺序：未注册→空→已使用→错误）。
	if code == "" {
		res.Reason = OutcomeEmptyCode
		v.logf("verify input user=%q code=%q c=%d window=[%d,%d] d=%d u=%d -> output ok=false reason=%s basis=empty-code",
			user, code, c, lo, hi, st.d, st.u, res.Reason)
		return res
	}

	var usedMatch int64 = -1 // 窗口内首个不大于 u 的匹配步（若有）
	for step := lo; step <= hi; step++ {
		if v.codes(user, step) != code {
			continue
		}
		// 候选步：窗口内序号大于水位 u 的步；取其中序号最小的匹配步。
		if step > st.u {
			oldD, oldU := st.d, st.u
			res.OK = true
			res.Reason = OutcomeOK
			res.MatchedStep = step
			st.u = step
			st.d = step - c
			res.Drift = st.d
			res.Watermark = st.u
			v.logf("verify input user=%q code=%q c=%d window=[%d,%d] d=%d u=%d -> output ok=true reason=%s basis=match-at-unused-step-%d; updated d=%d u=%d",
				user, code, c, lo, hi, oldD, oldU, res.Reason, step, st.d, st.u)
			return res
		}
		if usedMatch < 0 {
			usedMatch = step
		}
	}

	if usedMatch >= 0 {
		// 窗口内存在匹配步但没有候选匹配：匹配步必然不大于 u，口令已使用。
		res.Reason = OutcomeCodeUsed
		v.logf("verify input user=%q code=%q c=%d window=[%d,%d] d=%d u=%d -> output ok=false reason=%s basis=match-only-at-used-step-%d",
			user, code, c, lo, hi, st.d, st.u, res.Reason, usedMatch)
		return res
	}

	res.Reason = OutcomeCodeWrong
	v.logf("verify input user=%q code=%q c=%d window=[%d,%d] d=%d u=%d -> output ok=false reason=%s basis=no-matching-step-in-window",
		user, code, c, lo, hi, st.d, st.u, res.Reason)
	return res
}

func (v *Verifier) logf(format string, args ...any) {
	if v.logger != nil {
		v.logger.Printf(format, args...)
	}
}
