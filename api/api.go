// Package api 是对外入口：New/Add/Allocate/SelfCheck。依赖 alloc。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/alloc"
	"ontology/mf"
)

// 可判定的哨兵错误，四者互不相同。
var (
	ErrEmptyID         = errors.New("api: empty id")
	ErrDuplicateID     = errors.New("api: duplicate id")
	ErrNegativeDemand  = errors.New("api: negative demand")
	ErrInvalidCapacity = errors.New("api: capacity must be positive")
)

// Allocator 是并发安全的单资源最大最小公平分配器。
type Allocator struct {
	mu       sync.Mutex
	capacity int64
	demands  map[string]int64
	eng      alloc.Engine
}

// New 构造分配器；capacity <= 0 整体失败，不留任何状态。
func New(capacity int64) (*Allocator, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Allocator{capacity: capacity, demands: map[string]int64{}}, nil
}

// Add 登记一个任务。任何校验失败都不改变已有状态。
func (a *Allocator) Add(id string, demand int64) error {
	if id == "" {
		return ErrEmptyID
	}
	if demand < 0 {
		return ErrNegativeDemand
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.demands[id]; ok {
		return ErrDuplicateID
	}
	a.demands[id] = demand
	return nil
}

// Allocate 返回逐 id 的精确最大最小公平份额。
func (a *Allocator) Allocate() map[string]mf.Frac {
	a.mu.Lock()
	defer a.mu.Unlock()
	tasks := make([]alloc.Task, 0, len(a.demands))
	for id, d := range a.demands {
		tasks = append(tasks, alloc.Task{ID: id, Demand: d})
	}
	return a.eng.Allocate(a.capacity, tasks)
}

// SelfCheck 在一组内置任务（C=30，需求 6/12/18/30）上核验四条不变量：
// 与朴素参照一致、最大最小、守恒、失败不留痕。不改动 a 的任何状态。
func (a *Allocator) SelfCheck() error {
	fresh, err := New(30)
	if err != nil {
		return err
	}
	demands := map[string]int64{"A": 6, "B": 12, "C": 18, "D": 30}
	for id, d := range demands {
		if err := fresh.Add(id, d); err != nil {
			return err
		}
	}
	got := fresh.Allocate()

	// 不变量 1：与朴素参照逐 id 一致。
	var tasks []alloc.Task
	for id, d := range demands {
		tasks = append(tasks, alloc.Task{ID: id, Demand: d})
	}
	for id, want := range alloc.Naive(30, tasks) {
		if mf.Cmp(got[id], want) != 0 {
			return fmt.Errorf("selfcheck: naive mismatch %s: %s != %s", id, got[id], want)
		}
	}
	// 不变量 2：最大最小——未满额者不小于任何其他人。
	for id, d := range demands {
		if mf.Leq(d, got[id]) {
			continue
		}
		for jd := range demands {
			if mf.Cmp(got[id], got[jd]) < 0 {
				return fmt.Errorf("selfcheck: max-min violated at %s", id)
			}
		}
	}
	// 不变量 3：守恒 sum == min(C, sum demand)。
	sum := mf.Frac{N: 0, D: 1}
	var dsum int64
	for id, d := range demands {
		sum = mf.Add(sum, got[id])
		dsum += d
	}
	want := dsum
	if want > 30 {
		want = 30
	}
	if mf.Cmp(sum, mf.New(want, 1)) != 0 {
		return fmt.Errorf("selfcheck: conservation violated: sum=%s want %d", sum, want)
	}
	// 不变量 4：失败不留痕——四类拒绝后状态与之前完全一致。
	before := fresh.Allocate()
	if _, err := New(0); !errors.Is(err, ErrInvalidCapacity) {
		return fmt.Errorf("selfcheck: bad capacity not rejected: %v", err)
	}
	for _, op := range []error{fresh.Add("", 1), fresh.Add("A", 1), fresh.Add("Z", -1)} {
		if op == nil {
			return errors.New("selfcheck: invalid op accepted")
		}
	}
	after := fresh.Allocate()
	if len(before) != len(after) {
		return errors.New("selfcheck: state changed after rejects")
	}
	for id, v := range before {
		if mf.Cmp(after[id], v) != 0 {
			return fmt.Errorf("selfcheck: state changed for %s after rejects", id)
		}
	}
	return nil
}
