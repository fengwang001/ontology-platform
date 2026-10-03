// Package budget 实现带阶梯告警、线性外推预测与超支冻结的预算花费守卫。
//
// 守卫在花费记入后按累计比例触发一次性阶梯告警，按已过天数线性外推
// 月末花费触发预测告警，并在超支时冻结正向花费。预算被调整时精确地
// 重新武装阶梯标志并重新判定，保证事件、冻结状态与告警标志可精确复现。
package budget

import (
	"errors"
	"fmt"
	"math/bits"
	"sync"
)

// 取值范围常量。
const (
	MinPeriodDays = 1
	MaxPeriodDays = 366
	MinLadders    = 1
	MaxLadders    = 8
	MinPct        = 1
	MaxPct        = 1000
	MaxBudget     = int64(1_000_000_000_000_000) // 预算上限 10^15，单位分
	MaxSpend      = int64(1_000_000_000_000_000) // 累计花费上限 10^15
	MaxSpendDelta = int64(1_000_000_000_000)     // 单笔金额绝对值上限 10^12
	pctScale      = uint64(100)
)

// ErrInvalidConfig 表示构造参数不满足约束，整体拒绝。
var ErrInvalidConfig = errors.New("budget: invalid config")

// RejectReason 区分 Spend / AdjustBudget 的拒绝原因。
type RejectReason int

const (
	// RejectInvalidParam 参数非法（day 或 x 越界，或 B2 越界）。
	RejectInvalidParam RejectReason = iota
	// RejectDayRegression 日期回退（day 小于 cur）。
	RejectDayRegression
	// RejectFrozen 已冻结（x 大于 0 且 S 不小于 B）。
	RejectFrozen
	// RejectOutOfRange 额度越界（S 加 x 小于 0 或大于 10^15）。
	RejectOutOfRange
)

func (r RejectReason) String() string {
	switch r {
	case RejectInvalidParam:
		return "INVALID_PARAM"
	case RejectDayRegression:
		return "DAY_REGRESSION"
	case RejectFrozen:
		return "FROZEN"
	case RejectOutOfRange:
		return "OUT_OF_RANGE"
	}
	return "UNKNOWN"
}

// RejectError 是被拒绝操作返回的错误，携带可区分的拒绝原因。
type RejectError struct {
	Reason RejectReason
}

func (e *RejectError) Error() string { return "budget: rejected: " + e.Reason.String() }

// EventKind 事件类型。
type EventKind int

const (
	// EventLadder 阶梯告警，Pct 为触发的阶梯百分比。
	EventLadder EventKind = iota
	// EventForecast 预测告警。
	EventForecast
	// EventFroze 进入冻结。
	EventFroze
	// EventThawed 解除冻结。
	EventThawed
)

// Event 一次评估产生的事件。仅 EventLadder 使用 Pct。
type Event struct {
	Kind EventKind
	Pct  int
}

func (e Event) String() string {
	switch e.Kind {
	case EventLadder:
		return fmt.Sprintf("LADDER(%d)", e.Pct)
	case EventForecast:
		return "FORECAST"
	case EventFroze:
		return "FROZE"
	case EventThawed:
		return "THAWED"
	}
	return "UNKNOWN"
}

// State 守卫状态快照，用于查询与测试对照。
type State struct {
	Spent    int64  // 累计花费 S
	Cur      int    // 当前日 cur
	Budget   int64  // 当前预算 B
	Fired    []bool // 各阶梯已触发标志
	Forecast bool   // 预测标志 f
	Frozen   bool   // 冻结状态（恒等于 S >= B）
}

// Guard 预算花费守卫。所有方法可并发调用，效果等价于某个串行顺序。
type Guard struct {
	mu       sync.Mutex
	d        int   // 周期天数 D
	p        []int // 阶梯百分比，严格升序
	fPct     int   // 预测触发百分比 F
	dmin     int   // 预测所需最小已过天数 Dmin
	budget   int64 // 当前预算 B
	spent    int64 // 累计花费 S
	cur      int   // 当前日 cur
	fired    []bool
	forecast bool
}

// NewGuard 构造守卫。任一参数不满足约束时整体拒绝并返回 ErrInvalidConfig。
func NewGuard(d int, p []int, fPct, dmin int, budget int64) (*Guard, error) {
	if d < MinPeriodDays || d > MaxPeriodDays {
		return nil, ErrInvalidConfig
	}
	if len(p) < MinLadders || len(p) > MaxLadders {
		return nil, ErrInvalidConfig
	}
	for i, pct := range p {
		if pct < MinPct || pct > MaxPct {
			return nil, ErrInvalidConfig
		}
		if i > 0 && pct <= p[i-1] {
			return nil, ErrInvalidConfig
		}
	}
	if fPct < MinPct || fPct > MaxPct {
		return nil, ErrInvalidConfig
	}
	if dmin < 1 || dmin > d {
		return nil, ErrInvalidConfig
	}
	if budget < 1 || budget > MaxBudget {
		return nil, ErrInvalidConfig
	}
	pc := make([]int, len(p))
	copy(pc, p)
	return &Guard{
		d:      d,
		p:      pc,
		fPct:   fPct,
		dmin:   dmin,
		budget: budget,
		fired:  make([]bool, len(pc)),
	}, nil
}

// Spend 记入当日花费。通过校验后令 cur=day、S 加 x 并做一次评估，
// 返回本次评估产生的事件（按 LADDER、FORECAST、FROZE/THAWED 次序）。
// 被拒绝时不改变任何状态，并按校验顺序返回第一个拒绝原因。
func (g *Guard) Spend(day int, x int64) ([]Event, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if day < 0 || day >= g.d || x < -MaxSpendDelta || x > MaxSpendDelta {
		return nil, &RejectError{Reason: RejectInvalidParam}
	}
	if day < g.cur {
		return nil, &RejectError{Reason: RejectDayRegression}
	}
	if x > 0 && g.spent >= g.budget {
		return nil, &RejectError{Reason: RejectFrozen}
	}
	ns := g.spent + x
	if ns < 0 || ns > MaxSpend {
		return nil, &RejectError{Reason: RejectOutOfRange}
	}

	frozenBefore := g.spent >= g.budget
	g.cur = day
	g.spent = ns
	return g.evaluate(frozenBefore), nil
}

// AdjustBudget 调整预算：先按新预算重新武装阶梯标志（退款本身从不重新
// 武装），然后令 B=B2 并做一次评估。B2 越界时以参数非法拒绝。
func (g *Guard) AdjustBudget(b2 int64) ([]Event, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if b2 < 1 || b2 > MaxBudget {
		return nil, &RejectError{Reason: RejectInvalidParam}
	}

	frozenBefore := g.spent >= g.budget
	// 重新武装：已触发且 S*100 < B2*p 的阶梯清除标志（恰等保持）。
	for i, pct := range g.p {
		if g.fired[i] && !gePct(uint64(g.spent), b2, pct) {
			g.fired[i] = false
		}
	}
	g.budget = b2
	return g.evaluate(frozenBefore), nil
}

// Frozen 返回冻结状态，恒等于 S 不小于 B。
func (g *Guard) Frozen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.spent >= g.budget
}

// Snapshot 返回当前状态快照。
func (g *Guard) Snapshot() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	fired := make([]bool, len(g.fired))
	copy(fired, g.fired)
	return State{
		Spent:    g.spent,
		Cur:      g.cur,
		Budget:   g.budget,
		Fired:    fired,
		Forecast: g.forecast,
		Frozen:   g.spent >= g.budget,
	}
}

// evaluate 在花费或预算变更后按固定次序评估并产生事件。
// frozenBefore 为本次操作开始前的冻结状态（旧 S 与旧 B 比较）。
func (g *Guard) evaluate(frozenBefore bool) []Event {
	var events []Event

	// 一、阶梯告警：按 p 升序，恰等即触发，一次性。
	for i, pct := range g.p {
		if !g.fired[i] && gePct(uint64(g.spent), g.budget, pct) {
			g.fired[i] = true
			events = append(events, Event{Kind: EventLadder, Pct: pct})
		}
	}

	// 二、预测告警：e 为已过天数（含当日），proj=floor(S*D/e)。
	e := g.cur + 1
	cond := false
	if e >= g.dmin {
		proj := uint64(g.spent) * uint64(g.d) / uint64(e)
		cond = gePct(proj, g.budget, g.fPct)
	}
	if cond && !g.forecast {
		g.forecast = true
		events = append(events, Event{Kind: EventForecast})
	} else if !cond {
		g.forecast = false
	}

	// 三、冻结状态迁移，排在前两类事件之后。
	frozenAfter := g.spent >= g.budget
	if !frozenBefore && frozenAfter {
		events = append(events, Event{Kind: EventFroze})
	} else if frozenBefore && !frozenAfter {
		events = append(events, Event{Kind: EventThawed})
	}
	return events
}

// gePct 判断 s*100 >= b*pct。乘积可超出 int64，使用 128 位比较。
func gePct(s uint64, b int64, pct int) bool {
	hi, lo := bits.Mul64(s, pctScale)
	if hi != 0 {
		return true
	}
	return lo >= uint64(b)*uint64(pct)
}
