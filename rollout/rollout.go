// Package rollout 实现带基线对比闸门的灰度分批发布评审器。
//
// 评审器按实例数分批放量，每批浸泡 S 后开始检视，依据新旧版本错误率
// 对比决定晋级、回退或冻结。所有操作可并发调用，效果等价于某个串行
// 顺序；相同的操作序列重放得到完全相同的结果。
package rollout

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
)

const (
	maxN          = int64(1_000_000)
	maxCounter    = int64(1_000_000_000)
	maxNow        = int64(1_000_000_000_000_000)
	maxBatches    = 8
	maxPercent    = int64(100)
	maxTol        = int64(1000)
	maxSmallCount = int64(100)
)

// State 为发布状态机状态。
type State int

const (
	StateIdle       State = iota // 未开始
	StatePublishing              // 发布中
	StateDone                    // 完成
	StateAborted                 // 中止
	StateFrozen                  // 冻结
)

func (s State) String() string {
	switch s {
	case StateIdle:
		return "未开始"
	case StatePublishing:
		return "发布中"
	case StateDone:
		return "完成"
	case StateAborted:
		return "中止"
	case StateFrozen:
		return "冻结"
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// Reason 为操作被拒绝的原因，按 参数非法 > 状态不符 > 时钟回退 的优先级只报第一个。
type Reason int

const (
	ReasonInvalidParam  Reason = iota // 参数非法
	ReasonInvalidState                // 状态不符
	ReasonClockRollback               // 时钟回退
)

func (r Reason) String() string {
	switch r {
	case ReasonInvalidParam:
		return "参数非法"
	case ReasonInvalidState:
		return "状态不符"
	case ReasonClockRollback:
		return "时钟回退"
	}
	return fmt.Sprintf("Reason(%d)", int(r))
}

// RejectError 描述一次被拒绝的操作；被拒绝的操作不改变任何状态。
type RejectError struct {
	Reason Reason
	Msg    string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("%s: %s", e.Reason, e.Msg)
}

// IsReason 报告 err 是否为 reason 类型的拒绝。
func IsReason(err error, reason Reason) bool {
	var re *RejectError
	return errors.As(err, &re) && re.Reason == reason
}

// Verdict 为一次被接受的 Observe 的判定结果。
type Verdict int

const (
	VerdictSoaking            Verdict = iota // 浸泡中（未做检视）
	VerdictInsufficientSample                // 样本不足（ps、fs 不变）
	VerdictPass                              // 通过
	VerdictFail                              // 失败
)

func (v Verdict) String() string {
	switch v {
	case VerdictSoaking:
		return "浸泡中"
	case VerdictInsufficientSample:
		return "样本不足"
	case VerdictPass:
		return "通过"
	case VerdictFail:
		return "失败"
	}
	return fmt.Sprintf("Verdict(%d)", int(v))
}

// Config 为评审器构造参数。
type Config struct {
	N    int64   // 总实例数，1..1e6
	P    []int64 // 各批累计百分比，严格递增，1..100，末项必为 100，长度 1..8
	S    int64   // 浸泡时长，0..1e9
	K    int64   // 需连续通过次数，1..100
	Tol  int64   // 容忍百分比，0..1000
	Ef   int64   // 绝对错误下限，0..1e9
	Nmin int64   // 最小样本，0..1e9
	R    int64   // 失败次数上限，1..100
	H    int64   // 回退后静默期，0..1e9
	M    int64   // 回退次数上限，1..100
}

// Status 为评审器某一时刻的完整快照。
type Status struct {
	State     State // 当前状态
	Batch     int   // 当前批号（去重后从 0 编号）
	Instances int64 // 当前新版本实例数
	St        int64 // 当前批开始时刻
	Rn        int64 // 本批新版本累计请求数
	En        int64 // 本批新版本累计错误数
	Rb        int64 // 本批基线版本累计请求数
	Eb        int64 // 本批基线版本累计错误数
	Ps        int64 // 连续通过数
	Fs        int64 // 本批失败数
	Rollbacks int64 // 累计回退次数
}

// Reviewer 为灰度分批发布评审器，可并发使用。
type Reviewer struct {
	mu        sync.Mutex
	cfg       Config
	cums      []int64 // 去重后的各批累计实例数，末项恰为 N
	state     State
	batch     int
	st        int64
	rn, en    int64
	rb, eb    int64
	ps, fs    int64
	rollbacks int64
	maxNow    int64 // 已被接受操作的最大 now，初值 0
}

// New 校验构造参数并创建评审器。参数非法时返回 *RejectError（ReasonInvalidParam）。
func New(cfg Config) (*Reviewer, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	cums := make([]int64, 0, len(cfg.P))
	for _, p := range cfg.P {
		cum := (cfg.N*p + 99) / 100 // ⌈N×p/100⌉
		if len(cums) == 0 || cums[len(cums)-1] != cum {
			cums = append(cums, cum)
		}
	}
	return &Reviewer{cfg: cfg, cums: cums, state: StateIdle}, nil
}

func validateConfig(cfg Config) error {
	bad := func(format string, args ...any) error {
		return &RejectError{Reason: ReasonInvalidParam, Msg: fmt.Sprintf(format, args...)}
	}
	if cfg.N < 1 || cfg.N > maxN {
		return bad("N=%d 越界 [1,%d]", cfg.N, maxN)
	}
	if len(cfg.P) < 1 || len(cfg.P) > maxBatches {
		return bad("批次数 m=%d 越界 [1,%d]", len(cfg.P), maxBatches)
	}
	prev := int64(0)
	for i, p := range cfg.P {
		if p < 1 || p > maxPercent {
			return bad("p_%d=%d 越界 [1,100]", i, p)
		}
		if p <= prev {
			return bad("p_%d=%d 未严格递增", i, p)
		}
		prev = p
	}
	if cfg.P[len(cfg.P)-1] != maxPercent {
		return bad("末批百分比 p_%d=%d 必须为 100", len(cfg.P)-1, cfg.P[len(cfg.P)-1])
	}
	if cfg.S < 0 || cfg.S > maxCounter {
		return bad("S=%d 越界 [0,%d]", cfg.S, maxCounter)
	}
	if cfg.K < 1 || cfg.K > maxSmallCount {
		return bad("K=%d 越界 [1,100]", cfg.K)
	}
	if cfg.Tol < 0 || cfg.Tol > maxTol {
		return bad("tol=%d 越界 [0,1000]", cfg.Tol)
	}
	if cfg.Ef < 0 || cfg.Ef > maxCounter {
		return bad("Ef=%d 越界 [0,%d]", cfg.Ef, maxCounter)
	}
	if cfg.Nmin < 0 || cfg.Nmin > maxCounter {
		return bad("Nmin=%d 越界 [0,%d]", cfg.Nmin, maxCounter)
	}
	if cfg.R < 1 || cfg.R > maxSmallCount {
		return bad("R=%d 越界 [1,100]", cfg.R)
	}
	if cfg.H < 0 || cfg.H > maxCounter {
		return bad("H=%d 越界 [0,%d]", cfg.H, maxCounter)
	}
	if cfg.M < 1 || cfg.M > maxSmallCount {
		return bad("M=%d 越界 [1,100]", cfg.M)
	}
	return nil
}

// Start 在 now 时刻开始发布：进入发布中，第 0 批开始，实例数为 cum_0。
func (r *Reviewer) Start(now int64) error {
	if err := checkNow(now); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != StateIdle {
		return &RejectError{Reason: ReasonInvalidState, Msg: fmt.Sprintf("Start 时状态为 %s", r.state)}
	}
	if err := r.checkClockLocked(now); err != nil {
		return err
	}
	r.state = StatePublishing
	r.batch = 0
	r.st = now
	r.maxNow = now
	return nil
}

// Observe 累加四个增量 (dRn, dEn, dRb, dEb) 并按规则检视，返回判定结果。
func (r *Reviewer) Observe(now, dRn, dEn, dRb, dEb int64) (Verdict, error) {
	if err := checkNow(now); err != nil {
		return VerdictSoaking, err
	}
	if err := checkDeltas(dRn, dEn, dRb, dEb); err != nil {
		return VerdictSoaking, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkCountersLocked(dRn, dEn, dRb, dEb); err != nil {
		return VerdictSoaking, err
	}
	if r.state != StatePublishing {
		return VerdictSoaking, &RejectError{Reason: ReasonInvalidState, Msg: fmt.Sprintf("Observe 时状态为 %s", r.state)}
	}
	if err := r.checkClockLocked(now); err != nil {
		return VerdictSoaking, err
	}

	r.rn += dRn
	r.en += dEn
	r.rb += dRb
	r.eb += dEb
	r.maxNow = now

	if now < r.st+r.cfg.S {
		return VerdictSoaking, nil
	}
	if r.rn < r.cfg.Nmin {
		return VerdictInsufficientSample, nil
	}
	if r.gateFailsLocked() {
		r.onFailLocked(now)
		return VerdictFail, nil
	}
	r.onPassLocked(now)
	return VerdictPass, nil
}

// Status 返回当前完整快照。
func (r *Reviewer) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statusLocked()
}

func (r *Reviewer) statusLocked() Status {
	st := Status{
		State:     r.state,
		Batch:     r.batch,
		St:        r.st,
		Rn:        r.rn,
		En:        r.en,
		Rb:        r.rb,
		Eb:        r.eb,
		Ps:        r.ps,
		Fs:        r.fs,
		Rollbacks: r.rollbacks,
	}
	if r.state == StateAborted {
		st.Instances = 0
	} else {
		st.Instances = r.cums[r.batch]
	}
	return st
}

// gateFailsLocked 实现闸门：en ≥ Ef 且 en×rb×100 > eb×rn×(100+tol) 时判失败。
// 乘积可达 1e21，使用大整数比较。rb 为 0 时左端为 0，必判通过。
func (r *Reviewer) gateFailsLocked() bool {
	if r.en < r.cfg.Ef {
		return false
	}
	left := new(big.Int).Mul(big.NewInt(r.en), big.NewInt(r.rb))
	left.Mul(left, big.NewInt(100))
	right := new(big.Int).Mul(big.NewInt(r.eb), big.NewInt(r.rn))
	right.Mul(right, big.NewInt(100+r.cfg.Tol))
	return left.Cmp(right) > 0
}

// onPassLocked 处理一次通过：ps 加一，达到 K 时晋级。
func (r *Reviewer) onPassLocked(now int64) {
	r.ps++
	if r.ps < r.cfg.K {
		return
	}
	if r.batch == len(r.cums)-1 {
		r.state = StateDone
		return
	}
	r.batch++
	r.st = now
	r.resetBatchLocked()
}

// onFailLocked 处理一次失败：fs 加一且 ps 清零，达到 R 时回退。
func (r *Reviewer) onFailLocked(now int64) {
	r.fs++
	r.ps = 0
	if r.fs < r.cfg.R {
		return
	}
	r.rollbacks++
	if r.batch == 0 {
		r.state = StateAborted // 第 0 批回退即中止，中止优先于冻结
		return
	}
	r.batch--
	r.st = now + r.cfg.H
	r.resetBatchLocked()
	if r.rollbacks >= r.cfg.M {
		r.state = StateFrozen
	}
}

func (r *Reviewer) resetBatchLocked() {
	r.rn, r.en, r.rb, r.eb = 0, 0, 0, 0
	r.ps, r.fs = 0, 0
}

func checkNow(now int64) error {
	if now < 0 || now > maxNow {
		return &RejectError{Reason: ReasonInvalidParam, Msg: fmt.Sprintf("now=%d 越界 [0,%d]", now, maxNow)}
	}
	return nil
}

func checkDeltas(dRn, dEn, dRb, dEb int64) error {
	deltas := []struct {
		name string
		v    int64
	}{{"dRn", dRn}, {"dEn", dEn}, {"dRb", dRb}, {"dEb", dEb}}
	for _, d := range deltas {
		if d.v < 0 || d.v > maxCounter {
			return &RejectError{Reason: ReasonInvalidParam, Msg: fmt.Sprintf("增量 %s=%d 越界 [0,%d]", d.name, d.v, maxCounter)}
		}
	}
	if dEn > dRn {
		return &RejectError{Reason: ReasonInvalidParam, Msg: fmt.Sprintf("增量错误数 dEn=%d 大于请求数 dRn=%d", dEn, dRn)}
	}
	if dEb > dRb {
		return &RejectError{Reason: ReasonInvalidParam, Msg: fmt.Sprintf("增量错误数 dEb=%d 大于请求数 dRb=%d", dEb, dRb)}
	}
	return nil
}

func (r *Reviewer) checkCountersLocked(dRn, dEn, dRb, dEb int64) error {
	counters := []struct {
		name string
		v    int64
	}{{"rn", r.rn + dRn}, {"en", r.en + dEn}, {"rb", r.rb + dRb}, {"eb", r.eb + dEb}}
	for _, c := range counters {
		if c.v > maxCounter {
			return &RejectError{Reason: ReasonInvalidParam, Msg: fmt.Sprintf("累计值 %s=%d 超过上限 %d", c.name, c.v, maxCounter)}
		}
	}
	return nil
}

func (r *Reviewer) checkClockLocked(now int64) error {
	if now < r.maxNow {
		return &RejectError{Reason: ReasonClockRollback, Msg: fmt.Sprintf("now=%d 小于已接受的最大时刻 %d", now, r.maxNow)}
	}
	return nil
}
