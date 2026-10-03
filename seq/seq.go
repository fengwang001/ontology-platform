// Package seq 是跨系统自增编号对齐器的门面：
// 持有各系统计数器与活跃标志，签发全局唯一编号，
// 成员变更委托给 cut 包，同余对齐委托给 stride 包。
// 所有操作在互斥锁内完成，并发调用等价于某个串行顺序。
package seq

import (
	"errors"
	"sync"

	"ontology/cut"
	"ontology/stride"
)

// MaxID 是编号上限 10^15。
const MaxID = int64(1_000_000_000_000_000)

// MaxReserve 是 Reserve 单次可取编号数的上限。
const MaxReserve = 1000

var (
	ErrParam      = cut.ErrParam
	ErrPermission = cut.ErrPermission
	ErrState      = cut.ErrState
	ErrLast       = cut.ErrLast
	ErrInactive   = errors.New("seq: 系统不活跃")
	ErrExhausted  = errors.New("seq: 编号空间耗尽")
)

// Allocator 是编号对齐器，零值不可用，须用 New 构造。
type Allocator struct {
	mu      sync.Mutex
	st      cut.State
	touched int // 非导出计数器：最近一次 Issue/Reserve 触碰的系统数
}

// New 构造 K 个系统、步长模数为 m 的对齐器。
// 要求 2 <= K <= 8 且 K <= m <= 16，否则返回 ErrParam。
// 初始只有系统 1 活跃，各计数器 next 均为 1。
func New(K int, m int64) (*Allocator, error) {
	if K < 2 || K > 8 {
		return nil, ErrParam
	}
	if m < int64(K) || m > 16 {
		return nil, ErrParam
	}
	st := cut.State{
		M:           m,
		Next:        make([]int64, K),
		Active:      make([]bool, K),
		ActiveCount: 1,
	}
	for i := range st.Next {
		st.Next[i] = 1
	}
	st.Active[0] = true
	return &Allocator{st: st}, nil
}

// Issue 从系统 s 签发一个编号。s 须活跃，且编号不超过 MaxID。
func (a *Allocator) Issue(s int) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids, err := a.reserveLocked(s, 1)
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// Reserve 从系统 s 一次签发 n 个编号（1 <= n <= MaxReserve），
// 编号为 next_s, next_s+stride, ...，要求最后一个不大于 MaxID，
// 否则整体拒绝且状态不变。
func (a *Allocator) Reserve(s, n int) ([]int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reserveLocked(s, n)
}

func (a *Allocator) reserveLocked(s, n int) ([]int64, error) {
	a.touched = 0
	if s < 1 || s > len(a.st.Next) {
		return nil, ErrParam
	}
	if n < 1 || n > MaxReserve {
		return nil, ErrParam
	}
	i := s - 1
	a.touched++
	if !a.st.Active[i] {
		return nil, ErrInactive
	}
	step := stride.Mode(a.st.ActiveCount, a.st.M)
	last := a.st.Next[i] + int64(n-1)*step
	if last > MaxID {
		return nil, ErrExhausted
	}
	ids := make([]int64, n)
	for j := range ids {
		ids[j] = a.st.Next[i] + int64(j)*step
	}
	a.st.Next[i] += int64(n) * step
	return ids, nil
}

// Observe 模拟向系统 s 导入了带原编号 id（1..MaxID）的行。
// 若 id 不小于 next_s，则推进 next_s 使其此后不再签发不大于 id 的编号，
// 增量计入 J；否则无变化。
func (a *Allocator) Observe(s int, id int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s < 1 || s > len(a.st.Next) {
		return ErrParam
	}
	if id < 1 || id > MaxID {
		return ErrParam
	}
	i := s - 1
	if !a.st.Active[i] {
		return ErrInactive
	}
	if id < a.st.Next[i] {
		return nil
	}
	var newNext int64
	if stride.Mode(a.st.ActiveCount, a.st.M) == 1 {
		newNext = id + 1
	} else {
		newNext = stride.AlignUp(id+1, int64(i), a.st.M)
	}
	a.st.J += newNext - a.st.Next[i]
	a.st.Next[i] = newNext
	return nil
}

// Join 让系统 s 加入，需要 role=2。语义见 cut 包。
func (a *Allocator) Join(role, s int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st.Join(role, s)
}

// Leave 让系统 s 退出，需要 role=2。语义见 cut 包。
func (a *Allocator) Leave(role, s int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st.Leave(role, s)
}

// J 返回跳号计数（Join/Leave/Observe 造成的 next 增量之和）。
func (a *Allocator) J() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st.J
}

// HighWater 返回全部系统（含不活跃者）next 的最大值。
func (a *Allocator) HighWater() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st.HighWater()
}

// Next 返回系统 s 的当前计数器；s 越界时返回 0。
func (a *Allocator) Next(s int) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s < 1 || s > len(a.st.Next) {
		return 0
	}
	return a.st.Next[s-1]
}

// Active 返回系统 s 是否活跃；s 越界时返回 false。
func (a *Allocator) Active(s int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s < 1 || s > len(a.st.Active) {
		return false
	}
	return a.st.Active[s-1]
}

// Stride 返回当前签发步长。
func (a *Allocator) Stride() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return stride.Mode(a.st.ActiveCount, a.st.M)
}

// setNext 是测试用的非导出入口，直接改写系统 s 的计数器。
func (a *Allocator) setNext(s int, v int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.Next[s-1] = v
}
