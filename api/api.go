// Package api 是 DRF 两资源分配器对外的并发安全入口。
// 所有非法操作都在改动任何状态之前被拒绝，返回互不相同的哨兵错误。
package api

import (
	"errors"
	"sync"

	"ontology/alloc"
	"ontology/drf"
)

// Frac 是已约分的精确分数，正分母。
type Frac = drf.Frac

// 五类可判定、互不相同的哨兵错误。
var (
	ErrEmptyID         = errors.New("drf: task id must not be empty")
	ErrDuplicateID     = errors.New("drf: task id already exists")
	ErrNegativeDemand  = errors.New("drf: cpu and mem demand must not be negative")
	ErrZeroDemand      = errors.New("drf: cpu and mem demand must not both be zero")
	ErrInvalidCapacity = errors.New("drf: cpu and mem capacity must both be positive")
)

// Allocator 并发安全地持有容量与任务集合，状态只在进程内存。
type Allocator struct {
	mu  sync.Mutex
	p   *alloc.Planner
	ids map[string]struct{}
}

// New 创建容量为 (cpuCap, memCap) 的分配器；容量非正整体失败、不留任何状态。
func New(cpuCap, memCap int64) (*Allocator, error) {
	if cpuCap <= 0 || memCap <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Allocator{p: alloc.New(cpuCap, memCap), ids: map[string]struct{}{}}, nil
}

// Add 加入一个任务；任一非法条件都在状态变更前整体拒绝（失败不留痕）。
func (a *Allocator) Add(id string, cpu, mem int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	// 全部校验集中在任何写操作之前：先查 id，再查需求向量。
	if id == "" {
		return ErrEmptyID
	}
	if _, exists := a.ids[id]; exists {
		return ErrDuplicateID
	}
	if cpu < 0 || mem < 0 {
		return ErrNegativeDemand
	}
	if cpu == 0 && mem == 0 {
		return ErrZeroDemand
	}
	a.ids[id] = struct{}{}
	a.p.Add(alloc.Task{ID: id, CPU: cpu, Mem: mem})
	return nil
}

// Allocate 返回每任务单位数（精确分数）；无任务时返回空映射。可并发调用。
func (a *Allocator) Allocate() map[string]Frac {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.p.Allocate()
}

var one = drf.Frac{N: 1, D: 1}

// SelfCheck 用一组内置任务核验四条不变量与五类错误；使用局部分配器，不改变自身状态。
func (a *Allocator) SelfCheck() error {
	for i, s := range []error{ErrEmptyID, ErrDuplicateID, ErrNegativeDemand, ErrZeroDemand, ErrInvalidCapacity} {
		for _, t := range []error{ErrEmptyID, ErrDuplicateID, ErrNegativeDemand, ErrZeroDemand, ErrInvalidCapacity}[:i] {
			if s == t {
				return errors.New("selfcheck: 哨兵错误不互异")
			}
		}
	}
	c, err := New(60, 60)
	if err != nil {
		return err
	}
	if err := c.Add("A", 1, 6); err != nil || c.Add("B", 4, 1) != nil {
		return errors.New("selfcheck: 合法 Add 被拒绝")
	}
	r := c.Allocate()
	// 不变量1：内置算例的朴素结果即 A=8、B=12。
	if drf.Cmp(r["A"], drf.Frac{N: 8, D: 1}) != 0 || drf.Cmp(r["B"], drf.Frac{N: 12, D: 1}) != 0 {
		return errors.New("selfcheck: 不变量1 与朴素参照不一致")
	}
	// 不变量2：硬约束不突破，mem 恰达上界 60，cpu 用 56。
	uCPU := drf.Add(drf.Mul(r["A"], one), drf.Mul(r["B"], drf.Frac{N: 4, D: 1}))
	uMem := drf.Add(drf.Mul(r["A"], drf.Frac{N: 6, D: 1}), drf.Mul(r["B"], one))
	if drf.Cmp(uCPU, drf.Frac{N: 60, D: 1}) > 0 || drf.Cmp(uMem, drf.Frac{N: 60, D: 1}) != 0 {
		return errors.New("selfcheck: 不变量2 硬约束或绑定不成立")
	}
	// 不变量3：两任务主导份额相等。
	sA := drf.Mul(r["A"], drf.DominantRatio(1, 6, 60, 60))
	sB := drf.Mul(r["B"], drf.DominantRatio(4, 1, 60, 60))
	if drf.Cmp(sA, sB) != 0 {
		return errors.New("selfcheck: 不变量3 主导份额不公平")
	}
	// 不变量4：五类拒绝可判定，且拒绝前后 A/B 结果逐 id 不变。
	type bad struct {
		id   string
		c, m int64
		want error
	}
	bads := []bad{
		{"", 1, 1, ErrEmptyID},
		{"A", 1, 1, ErrDuplicateID},
		{"x", -1, 1, ErrNegativeDemand},
		{"x", 1, -1, ErrNegativeDemand},
		{"x", 0, 0, ErrZeroDemand},
	}
	for _, b := range bads {
		if err := c.Add(b.id, b.c, b.m); !errors.Is(err, b.want) {
			return errors.New("selfcheck: 拒绝错误不匹配")
		}
	}
	if _, err := New(0, 60); !errors.Is(err, ErrInvalidCapacity) {
		return errors.New("selfcheck: 容量错误不匹配")
	}
	r2 := c.Allocate()
	if len(r2) != 2 || drf.Cmp(r2["A"], r["A"]) != 0 || drf.Cmp(r2["B"], r["B"]) != 0 {
		return errors.New("selfcheck: 不变量4 被拒操作留下了痕迹")
	}
	if err := c.Add("ok", 1, 1); err != nil { // 拒绝后仍可正常使用
		return errors.New("selfcheck: 被拒后分配器不可继续使用")
	}
	return nil
}
