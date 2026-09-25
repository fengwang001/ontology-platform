// Package bar 在 phase 的单轮状态机之上提供进程登记、
// Arrive/Depart 合法性判定与轮次隔离。所有校验先于任何状态修改，
// 被拒绝的操作整体失败、不留痕迹（不变量 4）。
package bar

import (
	"errors"
	"sync"

	"ontology/phase"
)

// 可判定的哨兵错误，互不相同。
var (
	// ErrEmptyID 空进程 ID。
	ErrEmptyID = errors.New("bar: empty process id")
	// ErrRegistryFull 登记表已满（进程总数 N 固定），新 ID 拒绝登记。
	ErrRegistryFull = errors.New("bar: registry full")
	// ErrDuplicateArrive 该进程本轮已到达，重复 Arrive。
	ErrDuplicateArrive = errors.New("bar: duplicate arrive")
	// ErrEarlyDepart 到达阶段内 Depart（本轮尚未释放）。
	ErrEarlyDepart = errors.New("bar: depart before release")
	// ErrNotArrived 该进程不在本轮已到达集中（未到达或已离开）。
	ErrNotArrived = errors.New("bar: process not in arrived set")
	// ErrArriveAfterDepart 离开后本轮未结束就再次 Arrive（轮次隔离）。
	ErrArriveAfterDepart = errors.New("bar: arrive after depart in same round")
)

// Barrier 是可复用的两阶段屏障，容量 n 固定，并发安全。
type Barrier struct {
	mu       sync.Mutex
	n        int
	registry map[string]struct{} // 已登记进程，按 ID 哈希定位 O(1)
	rnd      *phase.Round
	round    int
	checked  int // 非导出：最近一次 Arrive 为登记进程而检查的条目个数
}

// New 创建容量为 n 的屏障；n <= 0 时 panic。
func New(n int) *Barrier {
	if n <= 0 {
		panic("bar: n must be positive")
	}
	return &Barrier{
		n:        n,
		registry: make(map[string]struct{}, n),
		rnd:      phase.New(n),
	}
}

// Arrive 登记（首次）并记录 id 本轮到达。全部校验先于任何写操作。
func (b *Barrier) Arrive(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id == "" {
		return ErrEmptyID
	}
	// 登记查找：按 ID 哈希定位，检查的条目数与进程总数无关。
	b.checked = 1
	if _, ok := b.registry[id]; !ok {
		if len(b.registry) == b.n {
			return ErrRegistryFull
		}
		// 新进程：先确认本轮阶段允许其到达，再一并登记+到达。
		if b.rnd.Stage() != phase.Arriving {
			return ErrArriveAfterDepart
		}
		b.registry[id] = struct{}{}
		b.rnd.AddArrival(id)
		return nil
	}
	if b.rnd.HasDeparted(id) {
		return ErrArriveAfterDepart
	}
	if b.rnd.HasArrived(id) {
		return ErrDuplicateArrive
	}
	b.rnd.AddArrival(id)
	return nil
}

// Depart 把 id 从已到达移入已离开；离开集满 N 时本轮结束、轮次+1。
func (b *Barrier) Depart(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id == "" {
		return ErrEmptyID
	}
	if !b.rnd.Released() {
		return ErrEarlyDepart
	}
	if !b.rnd.HasArrived(id) {
		return ErrNotArrived
	}
	if b.rnd.MoveToDeparted(id) {
		b.round++
		b.rnd = phase.New(b.n)
	}
	return nil
}

// Round 返回当前轮次（从 0 起）。
func (b *Barrier) Round() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.round
}

// Released 报告本轮是否已释放。
func (b *Barrier) Released() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rnd.Released()
}
