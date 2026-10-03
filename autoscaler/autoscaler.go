// Package autoscaler 实现带预热在途容量、冷却内追加差额与缩容阻止的
// 步进扩缩容控制器。控制器按指标所处区间选择调整比例，结合在途实例
// 与冷却状态决定每次实际增减的实例数，所有状态变化可精确复现。
package autoscaler

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

const (
	maxCapacityBound  = int64(1_000_000)
	maxThresholdBound = int64(1_000_000_000)
	maxNowBound       = int64(1_000_000_000_000_000)
)

var (
	// ErrInvalidConfig 表示构造参数整体非法。
	ErrInvalidConfig = errors.New("autoscaler: invalid config")
	// ErrInvalidParam 表示 Evaluate 的 now 或 metric 越界。
	ErrInvalidParam = errors.New("autoscaler: invalid parameter")
	// ErrClockBackward 表示 Evaluate 的 now 小于已接受的最大 now。
	ErrClockBackward = errors.New("autoscaler: clock backward")
)

// Tier 为档表中的一档：当 v（或 u）落在 [Lo, 下一档 Lo) 时取 Pct。
type Tier struct {
	Lo  int64
	Pct int64
}

// Config 为控制器构造参数。
type Config struct {
	Min     int64  // Mn：容量下限，[1, 1e6]
	Max     int64  // Mx：容量上限，[Min, 1e6]
	Initial int64  // c0：初始就绪容量，[Min, Max]
	High    int64  // H：扩容阈值，[0, 1e9]
	Low     int64  // Lw：缩容阈值，[0, 1e9)，严格小于 High
	Up      []Tier // 扩容档表：至少一档，Lo 从 0 起严格递增，Pct ∈ [1, 1000]
	Down    []Tier // 缩容档表：格式同上
	MinStep int64  // ms：最小扩容步长，[1, 1e6]
	Warmup  int64  // W：预热时长，[1, 1e9]
	CoolOut int64  // Cout：扩容冷却，[0, 1e9]
	CoolIn  int64  // Cin：缩容冷却，[0, 1e9]
}

// Batch 为在途批次：ReadyAt 时刻就绪 Count 个实例（恰等即就绪）。
type Batch struct {
	ReadyAt int64
	Count   int64
}

// ActionKind 为一次 Evaluate 的动作种类。
type ActionKind int

const (
	ActionNone ActionKind = iota
	ActionScaleOut
	ActionScaleIn
)

func (a ActionKind) String() string {
	switch a {
	case ActionScaleOut:
		return "scale-out"
	case ActionScaleIn:
		return "scale-in"
	default:
		return "none"
	}
}

// Result 为一次被接受的 Evaluate 的结果。
type Result struct {
	Action   ActionKind // 动作种类
	Amount   int64      // 本次增减的实例数（无动作时为 0）
	Cap      int64      // 处理后的就绪容量
	Inflight int64      // 处理后的在途总数
}

// State 为控制器某一时刻的完整快照。
type State struct {
	Cap           int64   // 就绪容量 cap
	Batches       []Batch // 在途批次列表（按就绪时刻递增）
	InflightTotal int64   // 在途总数
	Effective     int64   // 有效容量 eff = cap + 在途总数
	B0            int64   // 最近一次非冷却扩容前的有效容量
	LastOut       int64   // 最近一次非冷却扩容时刻，-1 表示无
	LastIn        int64   // 最近一次缩容时刻，-1 表示无
	MaxNow        int64   // 已接受的最大 now
}

// Controller 为步进扩缩容控制器，所有方法可并发调用，
// 结果等价于某个串行顺序。
type Controller struct {
	mu      sync.Mutex
	cfg     Config
	ready   int64
	batches []Batch
	b0      int64
	lastOut int64
	lastIn  int64
	maxNow  int64
	evalNow int64
	seen    map[int64]struct{}
}

// New 校验配置并构造控制器；配置非法时整体拒绝并返回 ErrInvalidConfig。
func New(cfg Config) (*Controller, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	cfg.Up = append([]Tier(nil), cfg.Up...)
	cfg.Down = append([]Tier(nil), cfg.Down...)
	return &Controller{
		cfg:     cfg,
		ready:   cfg.Initial,
		lastOut: -1,
		lastIn:  -1,
		evalNow: -1,
		seen:    make(map[int64]struct{}),
	}, nil
}

func validate(cfg Config) error {
	if cfg.Min < 1 || cfg.Min > maxCapacityBound {
		return fmt.Errorf("%w: Min=%d out of [1, 1e6]", ErrInvalidConfig, cfg.Min)
	}
	if cfg.Max < cfg.Min || cfg.Max > maxCapacityBound {
		return fmt.Errorf("%w: Max=%d out of [Min, 1e6]", ErrInvalidConfig, cfg.Max)
	}
	if cfg.Initial < cfg.Min || cfg.Initial > cfg.Max {
		return fmt.Errorf("%w: Initial=%d out of [Min, Max]", ErrInvalidConfig, cfg.Initial)
	}
	if cfg.High < 0 || cfg.High > maxThresholdBound ||
		cfg.Low < 0 || cfg.Low > maxThresholdBound || cfg.Low >= cfg.High {
		return fmt.Errorf("%w: thresholds Low=%d High=%d invalid", ErrInvalidConfig, cfg.Low, cfg.High)
	}
	if err := validateTiers("Up", cfg.Up); err != nil {
		return err
	}
	if err := validateTiers("Down", cfg.Down); err != nil {
		return err
	}
	if cfg.MinStep < 1 || cfg.MinStep > maxCapacityBound {
		return fmt.Errorf("%w: MinStep=%d out of [1, 1e6]", ErrInvalidConfig, cfg.MinStep)
	}
	if cfg.Warmup < 1 || cfg.Warmup > maxThresholdBound {
		return fmt.Errorf("%w: Warmup=%d out of [1, 1e9]", ErrInvalidConfig, cfg.Warmup)
	}
	if cfg.CoolOut < 0 || cfg.CoolOut > maxThresholdBound {
		return fmt.Errorf("%w: CoolOut=%d out of [0, 1e9]", ErrInvalidConfig, cfg.CoolOut)
	}
	if cfg.CoolIn < 0 || cfg.CoolIn > maxThresholdBound {
		return fmt.Errorf("%w: CoolIn=%d out of [0, 1e9]", ErrInvalidConfig, cfg.CoolIn)
	}
	return nil
}

func validateTiers(name string, tiers []Tier) error {
	if len(tiers) == 0 {
		return fmt.Errorf("%w: %s tiers empty", ErrInvalidConfig, name)
	}
	if tiers[0].Lo != 0 {
		return fmt.Errorf("%w: %s tiers must start at lo=0, got %d", ErrInvalidConfig, name, tiers[0].Lo)
	}
	for i, t := range tiers {
		if t.Pct < 1 || t.Pct > 1000 {
			return fmt.Errorf("%w: %s tier %d pct=%d out of [1, 1000]", ErrInvalidConfig, name, i, t.Pct)
		}
		if i > 0 && t.Lo <= tiers[i-1].Lo {
			return fmt.Errorf("%w: %s tier %d lo=%d not strictly increasing", ErrInvalidConfig, name, i, t.Lo)
		}
	}
	return nil
}

// Evaluate 处理一次评估。被拒绝时不改变任何状态；
// 被接受时（包括无动作）并入就绪批次并推进最大 now。
func (c *Controller) Evaluate(now, metric int64) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if now < 0 || now > maxNowBound || metric < 0 || metric > maxThresholdBound {
		return Result{}, ErrInvalidParam
	}
	if now < c.maxNow {
		return Result{}, ErrClockBackward
	}

	// 第一步：并入就绪时刻不大于 now 的在途批次（恰等即就绪）。
	kept := c.batches[:0]
	for _, b := range c.batches {
		if b.ReadyAt <= now {
			c.ready += b.Count
		} else {
			kept = append(kept, b)
		}
	}
	c.batches = kept
	c.maxNow = now

	// 同一 metric 在同一 now 重复评估：第二次起无动作。
	if now != c.evalNow {
		c.evalNow = now
		c.seen = make(map[int64]struct{})
	}
	if _, dup := c.seen[metric]; dup {
		return c.result(ActionNone, 0), nil
	}
	c.seen[metric] = struct{}{}

	inflight := sumBatches(c.batches)

	// 第二步：metric >= H，扩容方向。
	if metric >= c.cfg.High {
		pct := selectTier(c.cfg.Up, metric-c.cfg.High)
		eff := c.ready + inflight
		inCooldown := c.lastOut >= 0 && now < c.lastOut+c.cfg.CoolOut
		base := eff
		if inCooldown {
			base = c.b0
		}
		delta := ceilPct(base, pct)
		if delta < c.cfg.MinStep {
			delta = c.cfg.MinStep
		}
		target := base + delta
		if target > c.cfg.Max {
			target = c.cfg.Max
		}
		if target > eff {
			add := target - eff
			c.batches = append(c.batches, Batch{ReadyAt: now + c.cfg.Warmup, Count: add})
			if !inCooldown {
				c.b0 = eff
				c.lastOut = now
			}
			return c.result(ActionScaleOut, add), nil
		}
		return c.result(ActionNone, 0), nil
	}

	// 第三步：metric < Lw，缩容方向。
	if metric < c.cfg.Low {
		pct := selectTier(c.cfg.Down, c.cfg.Low-metric)
		if inflight > 0 {
			return c.result(ActionNone, 0), nil
		}
		if c.lastIn >= 0 && now < c.lastIn+c.cfg.CoolIn {
			return c.result(ActionNone, 0), nil
		}
		delta := c.ready * pct / 100
		if delta < 1 {
			delta = 1
		}
		target := c.ready - delta
		if target < c.cfg.Min {
			target = c.cfg.Min
		}
		if target < c.ready {
			amount := c.ready - target
			c.ready = target
			c.lastIn = now
			return c.result(ActionScaleIn, amount), nil
		}
		return c.result(ActionNone, 0), nil
	}

	// 第四步：metric 位于 [Lw, H)，无动作。
	return c.result(ActionNone, 0), nil
}

// State 返回当前完整状态快照。
func (c *Controller) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	inflight := sumBatches(c.batches)
	return State{
		Cap:           c.ready,
		Batches:       append([]Batch(nil), c.batches...),
		InflightTotal: inflight,
		Effective:     c.ready + inflight,
		B0:            c.b0,
		LastOut:       c.lastOut,
		LastIn:        c.lastIn,
		MaxNow:        c.maxNow,
	}
}

func (c *Controller) result(action ActionKind, amount int64) Result {
	return Result{
		Action:   action,
		Amount:   amount,
		Cap:      c.ready,
		Inflight: sumBatches(c.batches),
	}
}

// selectTier 按 lo<=v<下一档 lo 选档（最后一档无上界），返回 pct。
func selectTier(tiers []Tier, v int64) int64 {
	i := sort.Search(len(tiers), func(i int) bool { return tiers[i].Lo > v }) - 1
	return tiers[i].Pct
}

// ceilPct 计算 ceil(base*pct/100)，base、pct 均非负。
func ceilPct(base, pct int64) int64 {
	return (base*pct + 99) / 100
}

func sumBatches(batches []Batch) int64 {
	var total int64
	for _, b := range batches {
		total += b.Count
	}
	return total
}
