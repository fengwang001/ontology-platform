// Package proc 描述一个待调度进程：到达时刻、总 CPU 需求与剩余需求。
// 本包不依赖工程内任何其他包。
package proc

import (
	"errors"
	"sync"
)

// 可判定的哨兵错误：字段级非法输入。
var (
	// ErrBadArrival：到达时刻不能为负。
	ErrBadArrival = errors.New("proc: arrival must be >= 0")
	// ErrBadBurst：CPU 时长必须至少为 1。
	ErrBadBurst = errors.New("proc: burst must be >= 1")
	// ErrDupPID：pid 已登记，必须唯一。
	ErrDupPID = errors.New("proc: duplicate pid")
)

// Proc 是进程描述。Remaining 初始等于 Burst，由调度器在模拟中扣减。
type Proc struct {
	PID       int64
	Arrival   int64
	Burst     int64
	Remaining int64
}

// New 构造并校验一个进程：arrival >= 0、burst >= 1。
// pid 唯一性需要登记状态，由持有进程集合的上层包负责。
// 校验失败时返回零值 Proc，调用方在入集合前拒绝即可做到“失败不留痕”。
func New(pid, arrival, burst int64) (Proc, error) {
	if arrival < 0 {
		return Proc{}, ErrBadArrival
	}
	if burst < 1 {
		return Proc{}, ErrBadBurst
	}
	return Proc{
		PID:       pid,
		Arrival:   arrival,
		Burst:     burst,
		Remaining: burst,
	}, nil
}

// Set 是并发安全的已登记进程集合，负责 pid 唯一性。
type Set struct {
	mu sync.Mutex
	ps map[int64]Proc
}

// NewSet 创建空集合。
func NewSet() *Set {
	return &Set{ps: map[int64]Proc{}}
}

// Add 在锁内完成查重与插入：重复 pid 被整体拒绝，集合不发生任何变化。
func (st *Set) Add(p Proc) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.ps[p.PID]; ok {
		return ErrDupPID
	}
	st.ps[p.PID] = p
	return nil
}

// Len 返回已登记进程数。
func (st *Set) Len() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.ps)
}

// List 返回内容快照（调用方可任意修改），供调度器在 Run 中排序模拟。
func (st *Set) List() []Proc {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]Proc, 0, len(st.ps))
	for _, p := range st.ps {
		out = append(out, p)
	}
	return out
}
