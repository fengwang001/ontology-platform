// Package check 提供朴素参照实现与差分比对；全部测试在 check_test.go。
package check

import (
	"sync"

	"ontology/aging"
	"ontology/clock"
)

// Ref 是朴素参照队列：每次 Pop 都在当前刻全量扫描、现算有效优先级。
// 有效优先级 EP = base + (now-t)/K = (K*base+now-t)/K，分母同为 K，
// 故按分子 K*base+now-t 降序即可（全程 int64 精确比较，无浮点）。
type Ref struct {
	mu    sync.Mutex
	clk   *clock.Clock
	k     int64
	max   int
	tasks []struct {
		id      int
		base, t int64
	}
}

// New 创建参照队列，与 aging.New 同参数、同哨兵错误。
func New(clk *clock.Clock, k, maxLen int) (*Ref, error) {
	if k <= 0 {
		return nil, aging.ErrRange
	}
	return &Ref{clk: clk, k: int64(k), max: maxLen}, nil
}

// Push 入队，范围/重复/容量错误与 aging 完全一致。
func (r *Ref) Push(id, base int) error {
	now, b := r.clk.Now(), int64(base)
	if err := aging.InRange(now, b, r.k); err != nil {
		return aging.ErrRange
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.tasks {
		if e.id == id {
			return aging.ErrDuplicate
		}
	}
	if r.max > 0 && len(r.tasks) >= r.max {
		return aging.ErrFull
	}
	r.tasks = append(r.tasks, struct {
		id      int
		base, t int64
	}{id, b, now})
	return nil
}

// Pop 全量扫描选出当前刻 EP 最大者（并列 t 早、id 小）。
func (r *Ref) Pop() (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.tasks) == 0 {
		return 0, false
	}
	now := r.clk.Now()
	best := 0
	num := func(i int) int64 { return r.k*r.tasks[i].base + now - r.tasks[i].t }
	for i := 1; i < len(r.tasks); i++ {
		ni, nb := num(i), num(best)
		e, b := r.tasks[i], r.tasks[best]
		if ni > nb || ni == nb && (e.t < b.t || e.t == b.t && e.id < b.id) {
			best = i
		}
	}
	id := r.tasks[best].id
	r.tasks = append(r.tasks[:best], r.tasks[best+1:]...)
	return id, true
}

// Remove 线性查找删除；不存在返回 ErrNotFound。
func (r *Ref) Remove(id int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.tasks {
		if e.id == id {
			r.tasks = append(r.tasks[:i], r.tasks[i+1:]...)
			return nil
		}
	}
	return aging.ErrNotFound
}

// Len 返回在队任务数。
func (r *Ref) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.tasks)
}

// Advance 推进共享时钟，倒退返回 clock.ErrClockBackward。
func (r *Ref) Advance(d int64) (int64, error) { return r.clk.Advance(d) }
