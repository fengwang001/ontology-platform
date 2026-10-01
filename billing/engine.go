// Package billing 实现订阅套餐月中变更按日折算的账单引擎。
package billing

import (
	"errors"
	"fmt"
	"sync"
)

const (
	// MinDays 计费周期最短天数。
	MinDays = 1
	// MaxDays 计费周期最长天数。
	MaxDays = 366
	// MaxPrice 套餐整周期标价上限（分）。
	MaxPrice = int64(1_000_000_000_000)
)

var (
	// ErrInvalidDays 周期天数越界。
	ErrInvalidDays = errors.New("billing: days out of range [1, 366]")
	// ErrInvalidPrice 套餐价格越界。
	ErrInvalidPrice = errors.New("billing: price out of range [0, 1e12]")
	// ErrUnknownPlan 未知套餐。
	ErrUnknownPlan = errors.New("billing: unknown plan")
	// ErrSamePlan 与当前套餐相同。
	ErrSamePlan = errors.New("billing: same as current plan")
	// ErrInvalidDay 变更日不在 1 至 D-1。
	ErrInvalidDay = errors.New("billing: day out of range [1, D-1]")
	// ErrSameDayChange 变更日等于上一次变更日。
	ErrSameDayChange = errors.New("billing: day equals previous change day")
	// ErrOutOfOrderDay 变更日小于上一次变更日。
	ErrOutOfOrderDay = errors.New("billing: day before previous change day")
)

// LineKind 账单行类型。
type LineKind int

const (
	// Prepay 周期第 0 天预收全价。
	Prepay LineKind = iota
	// Refund 折退（金额为负），按当前套餐标价乘剩余天数向下取整。
	Refund
	// Surcharge 补收（金额为正），按新套餐标价乘剩余天数向上取整。
	Surcharge
)

func (k LineKind) String() string {
	switch k {
	case Prepay:
		return "PREPAY"
	case Refund:
		return "REFUND"
	case Surcharge:
		return "SURCHARGE"
	}
	return "UNKNOWN"
}

// Line 一条账单行。Refund 行 Amount 为负，其余为正。
type Line struct {
	Seq    int      // 周期内行号，从 0 开始
	Kind   LineKind // 行类型
	Plan   string   // 计价所用套餐
	Day    int      // 生效日（预收为 0，变更为变更日）
	Amount int64    // 金额（分）
}

func (l Line) String() string {
	return fmt.Sprintf("#%d %s plan=%s day=%d amount=%d", l.Seq, l.Kind, l.Plan, l.Day, l.Amount)
}

// Engine 账单引擎。所有方法可并发调用，效果等价于某个串行顺序。
type Engine struct {
	mu            sync.Mutex
	days          int
	prices        map[string]int64
	cycle         int
	plan          string
	lastChangeDay int
	hasChange     bool
	lines         []Line
}

// NewEngine 构造引擎：D 为周期天数，prices 为套餐表，initial 为初始套餐。
func NewEngine(days int, prices map[string]int64, initial string) (*Engine, error) {
	if days < MinDays || days > MaxDays {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidDays, days)
	}
	table := make(map[string]int64, len(prices))
	for name, price := range prices {
		if price < 0 || price > MaxPrice {
			return nil, fmt.Errorf("%w: plan %q price %d", ErrInvalidPrice, name, price)
		}
		table[name] = price
	}
	if _, ok := table[initial]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPlan, initial)
	}
	e := &Engine{days: days, prices: table, cycle: 1}
	e.startCycleLocked(initial)
	return e, nil
}

// ChangePlan 在第 day 天切换到 newPlan，依次生成折退与补收两行。
// 校验顺序：未知套餐、与当前相同、day 越界、等于上次变更日、早于上次变更日。
// 被拒绝时不改变任何状态。
func (e *Engine) ChangePlan(day int, newPlan string) (refund, surcharge Line, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	newPrice, ok := e.prices[newPlan]
	if !ok {
		return Line{}, Line{}, fmt.Errorf("%w: %q", ErrUnknownPlan, newPlan)
	}
	if newPlan == e.plan {
		return Line{}, Line{}, fmt.Errorf("%w: %q", ErrSamePlan, newPlan)
	}
	if day < 1 || day > e.days-1 {
		return Line{}, Line{}, fmt.Errorf("%w: day %d, D %d", ErrInvalidDay, day, e.days)
	}
	if e.hasChange {
		if day == e.lastChangeDay {
			return Line{}, Line{}, fmt.Errorf("%w: day %d", ErrSameDayChange, day)
		}
		if day < e.lastChangeDay {
			return Line{}, Line{}, fmt.Errorf("%w: day %d < %d", ErrOutOfOrderDay, day, e.lastChangeDay)
		}
	}

	remaining := int64(e.days - day)
	d := int64(e.days)
	curPrice := e.prices[e.plan]

	refund = Line{
		Seq:    len(e.lines),
		Kind:   Refund,
		Plan:   e.plan,
		Day:    day,
		Amount: -(curPrice * remaining / d), // 向下取整：折退不多退
	}
	surcharge = Line{
		Seq:    len(e.lines) + 1,
		Kind:   Surcharge,
		Plan:   newPlan,
		Day:    day,
		Amount: (newPrice*remaining + d - 1) / d, // 向上取整：补收不少收
	}
	e.lines = append(e.lines, refund, surcharge)
	e.plan = newPlan
	e.lastChangeDay = day
	e.hasChange = true
	return refund, surcharge, nil
}

// Settle 结算当前周期：返回全部行与应收总额（各行代数和），并开启新周期，
// 新周期按当前套餐在第 0 天预收全价。
func (e *Engine) Settle() (lines []Line, total int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	lines = make([]Line, len(e.lines))
	copy(lines, e.lines)
	for _, l := range lines {
		total += l.Amount
	}
	e.cycle++
	e.startCycleLocked(e.plan)
	return lines, total
}

// Lines 返回当前周期已生成的账单行副本。
func (e *Engine) Lines() []Line {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Line, len(e.lines))
	copy(out, e.lines)
	return out
}

// Total 返回当前周期已生成各行的代数和。
func (e *Engine) Total() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	var total int64
	for _, l := range e.lines {
		total += l.Amount
	}
	return total
}

// CurrentPlan 返回当前生效套餐。
func (e *Engine) CurrentPlan() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.plan
}

// Cycle 返回当前周期序号（从 1 开始）。
func (e *Engine) Cycle() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cycle
}

func (e *Engine) startCycleLocked(plan string) {
	e.plan = plan
	e.hasChange = false
	e.lastChangeDay = 0
	e.lines = []Line{{
		Seq:    0,
		Kind:   Prepay,
		Plan:   plan,
		Day:    0,
		Amount: e.prices[plan],
	}}
}
