package rollout

import (
	"math/big"
	"sync"
)

// window 是当前阶段、当前评估窗口的请求计数。
// 阶段切换、回滚、重置（以及失败后留级）时清空。
type window struct {
	canaryTotal  int64
	canaryFailed int64
	stableTotal  int64
	stableFailed int64
}

// Splitter 是带指标闸门与自动回滚的灰度流量切分器。
// 单一互斥锁保护全部可变状态，因此并发调用的结果
// 等价于某个串行顺序；锁内无阻塞操作。
// 路由（map + 链表 O(1)）与观测（计数器 O(1)）的开销
// 只取决于粘性记录上限 P，不随历史请求总数增长。
type Splitter struct {
	mu sync.Mutex

	cfg Config

	phase       Phase
	idx         int
	enteredAtMs int64
	failStreak  int
	win         window
	sticky      *stickyStore

	// 时钟由调用方注入；记录见过的最大时间戳，拒绝回退。
	clockSet bool
	lastMs   int64
}

// New 依据配置创建切分器，初始状态为未开始、比例为零。
func New(cfg Config) (*Splitter, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := &Splitter{cfg: cfg, phase: PhaseNotStarted}
	s.sticky = newStickyStore(cfg.MaxSticky, cfg.StickyMs)
	return s, nil
}

// Position 返回标识在 [0,10000) 上的恒定归属位置，供测试独立对照。
// 纯函数，不改变任何状态，因此不受时钟与阶段限制。
func (s *Splitter) Position(id string) int {
	return position(id)
}

// checkClock 必须在持锁状态下调用：时钟严格不允许回退。
func (s *Splitter) checkClock(nowMs int64) error {
	if s.clockSet && nowMs < s.lastMs {
		return ErrClockRewound
	}
	return nil
}

// advanceClock 接受一次合法调用所携带的时间戳。
func (s *Splitter) advanceClock(nowMs int64) {
	if !s.clockSet {
		s.clockSet = true
		s.lastMs = nowMs
		return
	}
	if nowMs > s.lastMs {
		s.lastMs = nowMs
	}
}

func (s *Splitter) currentRatio() int {
	switch s.phase {
	case PhaseRunning:
		return s.cfg.Ratios[s.idx]
	case PhaseCompleted:
		return s.cfg.Ratios[len(s.cfg.Ratios)-1]
	default:
		return 0
	}
}

// Start 仅未开始可用，进入第 0 阶段，驻留起点为 nowMs。
func (s *Splitter) Start(nowMs int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowMs); err != nil {
		return err
	}
	if s.phase != PhaseNotStarted {
		return ErrIllegalState
	}
	s.advanceClock(nowMs)
	s.phase = PhaseRunning
	s.idx = 0
	s.enteredAtMs = nowMs
	s.failStreak = 0
	s.win = window{}
	return nil
}

// Reset 任何状态可用，清空除配置外的全部状态（含粘性与时钟基线），
// 回到未开始。
func (s *Splitter) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = PhaseNotStarted
	s.idx = 0
	s.enteredAtMs = 0
	s.failStreak = 0
	s.win = window{}
	s.sticky = newStickyStore(s.cfg.MaxSticky, s.cfg.StickyMs)
	s.clockSet = false
	s.lastMs = 0
}

// Downgrade 仅进行中且阶段序号大于 0 可用。
// 窗口、失败累计清空，驻留起点重算，粘性记录保持不变。
func (s *Splitter) Downgrade(nowMs int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowMs); err != nil {
		return err
	}
	if s.phase != PhaseRunning || s.idx == 0 {
		return ErrIllegalState
	}
	s.advanceClock(nowMs)
	s.idx--
	s.enteredAtMs = nowMs
	s.failStreak = 0
	s.win = window{}
	return nil
}

// Route 给定标识与当前时间，返回应走版本与判定来源。
// 优先级：回滚态强制稳定 > 未过期粘性 > 按比例归属。
func (s *Splitter) Route(id string, nowMs int64) (RouteDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowMs); err != nil {
		return RouteDecision{}, err
	}
	s.advanceClock(nowMs)

	if s.phase == PhaseRolledBack {
		// 已回滚：不读也不更新粘性记录。
		return RouteDecision{Version: VersionStable, Source: SourceForceStable}, nil
	}

	if v, ok := s.sticky.lookup(id, nowMs); ok {
		s.sticky.touch(id, v, nowMs)
		return RouteDecision{Version: v, Source: SourceSticky}, nil
	}

	ratio := s.currentRatio()
	v := VersionStable
	if position(id) < ratio {
		v = VersionCanary
	}
	s.sticky.touch(id, v, nowMs)
	return RouteDecision{Version: v, Source: SourceProportion}, nil
}

// Observe 记录一次请求结果；仅计入当前进行中的评估窗口。
// 非进行中状态下静默忽略且不报错。
func (s *Splitter) Observe(v Version, success bool, nowMs int64) error {
	if v != VersionStable && v != VersionCanary {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowMs); err != nil {
		return err
	}
	s.advanceClock(nowMs)
	if s.phase != PhaseRunning {
		return nil
	}
	if v == VersionCanary {
		s.win.canaryTotal++
		if !success {
			s.win.canaryFailed++
		}
	} else {
		s.win.stableTotal++
		if !success {
			s.win.stableFailed++
		}
	}
	return nil
}

// Evaluate 周期性触发指标闸门。
// 非进行中或驻留未满返回 DwellNotMet；灰度请求不足 G 返回 Insufficient。
// 错误率比较全程整数运算（math/big），不存在浮点边界误判。
func (s *Splitter) Evaluate(nowMs int64) (EvalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(nowMs); err != nil {
		return EvalDwellNotMet, err
	}
	s.advanceClock(nowMs)

	if s.phase != PhaseRunning || nowMs-s.enteredAtMs < s.cfg.DwellMs {
		return EvalDwellNotMet, nil
	}
	if s.win.canaryTotal < int64(s.cfg.MinCanary) {
		return EvalInsufficient, nil
	}

	if !s.errorRateWithinTolerance() {
		s.failStreak++
		if s.failStreak >= s.cfg.MaxFailures {
			s.enterRollback()
			return EvalFailed, nil
		}
		// 留级：清空窗口，驻留起点重新计算。
		s.win = window{}
		s.enteredAtMs = nowMs
		return EvalFailed, nil
	}

	s.failStreak = 0
	s.win = window{}
	if s.idx == len(s.cfg.Ratios)-1 {
		s.phase = PhaseCompleted
		s.enteredAtMs = 0
		return EvalPassed, nil
	}
	s.idx++
	s.enteredAtMs = nowMs
	return EvalPassed, nil
}

// errorRateWithinTolerance 判定
// 灰度错误率 <= 稳定错误率 + T/10000。
// 稳定无请求时其错误率视为 0。全程整数精确比较：
//
//	sn > 0: gc/gn <= sc/sn + t/10000
//	        <=> gc*10000*sn <= sc*10000*gn + t*gn*sn
//	sn == 0: gc*10000 <= t*gn
func (s *Splitter) errorRateWithinTolerance() bool {
	gn, gc := big.NewInt(s.win.canaryTotal), big.NewInt(s.win.canaryFailed)
	sn, sc := big.NewInt(s.win.stableTotal), big.NewInt(s.win.stableFailed)
	base := big.NewInt(10000)
	tol := big.NewInt(int64(s.cfg.ToleranceBP))

	left := new(big.Int).Mul(gc, base) // gc*10000
	if s.win.stableTotal == 0 {
		right := new(big.Int).Mul(tol, gn)
		return left.Cmp(right) <= 0
	}
	left.Mul(left, sn) // gc*10000*sn

	right := new(big.Int).Mul(sc, base)
	right.Mul(right, gn) // sc*10000*gn
	extra := new(big.Int).Mul(tol, gn)
	extra.Mul(extra, sn) // t*gn*sn
	right.Add(right, extra)
	return left.Cmp(right) <= 0
}

func (s *Splitter) enterRollback() {
	s.phase = PhaseRolledBack
	s.idx = 0
	s.enteredAtMs = 0
	s.failStreak = 0
	s.win = window{}
	s.sticky.clear() // 进入回滚态时清空全部粘性记录
}

// Snapshot 返回只读状态快照。
func (s *Splitter) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		Phase:       s.phase,
		Ratio:       s.currentRatio(),
		FailStreak:  s.failStreak,
		StickyCount: s.sticky.len(),
		LastClockMs: s.lastMs,
	}
	if s.phase == PhaseRunning {
		snap.PhaseIndex = s.idx
		snap.EnteredAtMs = s.enteredAtMs
	}
	return snap
}
