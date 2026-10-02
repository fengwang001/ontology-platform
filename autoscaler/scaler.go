// Package autoscaler 实现带预热在途容量、冷却内追加差额与缩容阻止的
// 步进扩缩容控制器。控制器按指标所处区间选择调整比例，结合在途实例
// 与冷却状态决定本次实际增减的实例数，所有状态迁移均可精确复现。
package autoscaler

import (
	"errors"
	"sync"
)

// 参数与状态取值的绝对上界。
const (
	maxLimit   = int64(1_000_000)             // Mn/Mx/ms 上界
	threshMax  = int64(1_000_000_000)         // H/Lw/W/Cout/Cin/metric 上界
	maxNow     = int64(1_000_000_000_000_000) // now 上界 10^15
	maxStepPct = int64(1000)                  // 档表 pct 上界
)

// 拒绝原因，调用方可用 errors.Is 区分。
var (
	// ErrInvalidConfig 构造参数不满足约束，整体拒绝。
	ErrInvalidConfig = errors.New("autoscaler: invalid config")
	// ErrInvalidParam Evaluate 参数非法（now<0、now>10^15、metric<0、metric>10^9）。
	ErrInvalidParam = errors.New("autoscaler: invalid evaluate parameter")
	// ErrClockRegression 时钟回退（now 小于已接受的最大 now）。
	ErrClockRegression = errors.New("autoscaler: clock regression")
)

// Step 为档表中的一档：当 v（扩容为 metric-H，缩容为 Lw-metric）满足
// lo<=v<下一档 lo 时选中本档，最后一档无上界。
type Step struct {
	Lo  int64
	Pct int64
}

// Config 为控制器构造参数。
type Config struct {
	Mn   int64 // 容量下限，1..10^6
	Mx   int64 // 容量上限，Mn..10^6
	C0   int64 // 初始就绪容量，Mn..10^6
	H    int64 // 扩容阈值，0..10^9
	Lw   int64 // 缩容阈值，0..10^9 且 Lw<H
	Up   []Step
	Down []Step
	Ms   int64 // 最小扩容步长，1..10^6
	W    int64 // 预热时长，1..10^9
	Cout int64 // 扩容冷却，0..10^9
	Cin  int64 // 缩容冷却，0..10^9
}

// Action 为一次 Evaluate 的动作种类。
type Action int

const (
	ActionNone     Action = iota // 无动作
	ActionScaleOut               // 扩容（追加在途批次）
	ActionScaleIn                // 缩容（立即减少就绪容量）
)

func (a Action) String() string {
	switch a {
	case ActionScaleOut:
		return "scale-out"
	case ActionScaleIn:
		return "scale-in"
	default:
		return "none"
	}
}

// Batch 为一个在途批次：ReadyAt 时刻就绪，数量为 Count。
type Batch struct {
	ReadyAt int64
	Count   int64
}

// Result 为一次被接受的 Evaluate 的结果。
type Result struct {
	Action   Action // 动作种类
	Delta    int64  // 本次增减数量（扩容为追加的在途数量，缩容为减少的就绪容量）
	Cap      int64  // 处理后的就绪容量
	Inflight int64  // 处理后的在途总数
}

// Snapshot 为控制器状态的一致性快照，用于查询与测试对照。
type Snapshot struct {
	Cap        int64
	Batches    []Batch
	B0         int64
	LastOut    int64
	HasLastOut bool
	LastIn     int64
	HasLastIn  bool
	MaxNow     int64
}

// Inflight 返回快照中的在途总数。
func (s Snapshot) Inflight() int64 {
	var total int64
	for _, b := range s.Batches {
		total += b.Count
	}
	return total
}

// Eff 返回有效容量：cap 加全部在途数量。
func (s Snapshot) Eff() int64 {
	return s.Cap + s.Inflight()
}

// Scaler 为步进扩缩容控制器，所有方法可并发调用，
// 结果等价于某个串行顺序。
type Scaler struct {
	mu        sync.Mutex
	cfg       Config
	cap       int64
	batches   []Batch
	b0        int64
	lastOut   int64
	hasOut    bool
	lastIn    int64
	hasIn     bool
	maxNow    int64
	seenAtNow map[int64]struct{} // 当前 maxNow 已评估过的 metric，用于幂等
}

// NewScaler 校验配置并构造控制器；任一约束不满足即整体拒绝。
func NewScaler(cfg Config) (*Scaler, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	up := make([]Step, len(cfg.Up))
	copy(up, cfg.Up)
	down := make([]Step, len(cfg.Down))
	copy(down, cfg.Down)
	cfg.Up, cfg.Down = up, down
	return &Scaler{
		cfg:       cfg,
		cap:       cfg.C0,
		seenAtNow: make(map[int64]struct{}),
	}, nil
}

func validate(cfg Config) error {
	if cfg.Mn < 1 || cfg.Mn > maxLimit {
		return ErrInvalidConfig
	}
	if cfg.Mx < cfg.Mn || cfg.Mx > maxLimit {
		return ErrInvalidConfig
	}
	if cfg.C0 < cfg.Mn || cfg.C0 > cfg.Mx {
		return ErrInvalidConfig
	}
	if cfg.Lw < 0 || cfg.H > threshMax || cfg.Lw >= cfg.H {
		return ErrInvalidConfig
	}
	if !validSteps(cfg.Up) || !validSteps(cfg.Down) {
		return ErrInvalidConfig
	}
	if cfg.Ms < 1 || cfg.Ms > maxLimit {
		return ErrInvalidConfig
	}
	if cfg.W < 1 || cfg.W > threshMax {
		return ErrInvalidConfig
	}
	if cfg.Cout < 0 || cfg.Cout > threshMax || cfg.Cin < 0 || cfg.Cin > threshMax {
		return ErrInvalidConfig
	}
	return nil
}

func validSteps(steps []Step) bool {
	if len(steps) == 0 || steps[0].Lo != 0 {
		return false
	}
	for i, st := range steps {
		if st.Pct < 1 || st.Pct > maxStepPct {
			return false
		}
		if i > 0 && st.Lo <= steps[i-1].Lo {
			return false
		}
	}
	return true
}

// Snapshot 返回当前状态的一致性快照。
func (s *Scaler) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	batches := make([]Batch, len(s.batches))
	copy(batches, s.batches)
	return Snapshot{
		Cap:        s.cap,
		Batches:    batches,
		B0:         s.b0,
		LastOut:    s.lastOut,
		HasLastOut: s.hasOut,
		LastIn:     s.lastIn,
		HasLastIn:  s.hasIn,
		MaxNow:     s.maxNow,
	}
}

// Evaluate 处理一次评估，按次序执行：并入就绪批次、按阈值区间决策
// 扩容/缩容/无动作。被拒绝时不改变任何状态并返回可区分的原因，
// 拒绝原因按参数非法、时钟回退的顺序只报第一个。
func (s *Scaler) Evaluate(now, metric int64) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || now > maxNow || metric < 0 || metric > threshMax {
		return Result{}, ErrInvalidParam
	}
	if now < s.maxNow {
		return Result{}, ErrClockRegression
	}
	return s.evaluate(now, metric), nil
}

func (s *Scaler) evaluate(now, metric int64) Result {
	// 第一步：并入就绪时刻不大于 now 的在途批次（恰等即就绪）。
	kept := s.batches[:0]
	for _, b := range s.batches {
		if b.ReadyAt <= now {
			s.cap += b.Count
		} else {
			kept = append(kept, b)
		}
	}
	s.batches = kept

	// 接受本次评估：推进最大 now；同一 now 的重复 metric 自第二次起无动作。
	if now > s.maxNow {
		s.maxNow = now
		clear(s.seenAtNow)
	}
	if _, dup := s.seenAtNow[metric]; dup {
		return s.result(ActionNone, 0)
	}
	s.seenAtNow[metric] = struct{}{}

	// 第二步：metric 不小于 H 时尝试扩容。
	if metric >= s.cfg.H {
		return s.scaleOut(now, metric)
	}
	// 第三步：metric 严格小于 Lw 时尝试缩容。
	if metric < s.cfg.Lw {
		return s.scaleIn(now, metric)
	}
	// 第四步：metric 位于 [Lw, H) 时无动作。
	return s.result(ActionNone, 0)
}

func (s *Scaler) scaleOut(now, metric int64) Result {
	pct := selectPct(s.cfg.Up, metric-s.cfg.H)
	eff := s.cap + s.inflight()
	inCooldown := s.hasOut && now < s.lastOut+s.cfg.Cout
	// 非冷却（含恰等冷却结束）按当前有效容量计算；冷却内按 B0 追加差额。
	base := eff
	if inCooldown {
		base = s.b0
	}
	delta := ceilDiv(base*pct, 100)
	if delta < s.cfg.Ms {
		delta = s.cfg.Ms
	}
	target := base + delta
	if target > s.cfg.Mx {
		target = s.cfg.Mx
	}
	if target <= eff {
		// 上限钳制或差额为零时不动作，且不更新 B0/lastOut。
		return s.result(ActionNone, 0)
	}
	added := target - eff
	s.batches = append(s.batches, Batch{ReadyAt: now + s.cfg.W, Count: added})
	if !inCooldown {
		s.b0 = eff
		s.lastOut = now
		s.hasOut = true
	}
	return s.result(ActionScaleOut, added)
}

func (s *Scaler) scaleIn(now, metric int64) Result {
	// 存在任何在途批次则阻止缩容。
	if len(s.batches) > 0 {
		return s.result(ActionNone, 0)
	}
	// 缩容冷却独立于扩容冷却。
	if s.hasIn && now < s.lastIn+s.cfg.Cin {
		return s.result(ActionNone, 0)
	}
	pct := selectPct(s.cfg.Down, s.cfg.Lw-metric)
	delta := s.cap * pct / 100 // 向下取整
	if delta < 1 {
		delta = 1
	}
	target := s.cap - delta
	if target < s.cfg.Mn {
		target = s.cfg.Mn
	}
	if target >= s.cap {
		return s.result(ActionNone, 0)
	}
	removed := s.cap - target
	s.cap = target
	s.lastIn = now
	s.hasIn = true
	return s.result(ActionScaleIn, removed)
}

func (s *Scaler) result(action Action, delta int64) Result {
	return Result{Action: action, Delta: delta, Cap: s.cap, Inflight: s.inflight()}
}

// selectPct 按 lo<=v<下一档 lo 选档，最后一档无上界。
func selectPct(steps []Step, v int64) int64 {
	pct := steps[0].Pct
	for _, st := range steps[1:] {
		if v < st.Lo {
			break
		}
		pct = st.Pct
	}
	return pct
}

func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}

func (s *Scaler) inflight() int64 {
	var total int64
	for _, b := range s.batches {
		total += b.Count
	}
	return total
}
