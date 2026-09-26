// Package proc 描述进程并做登记校验，不依赖其他包。
package proc

import (
	"errors"
	"fmt"
	"sort"
)

// 可判定的哨兵错误。
var (
	ErrInvalidProcess = errors.New("proc: invalid arrival or burst")
	ErrDuplicatePID   = errors.New("proc: duplicate pid")
)

// Proc 是一个进程的完整描述；Remaining 初值等于 Burst。
type Proc struct {
	PID       int64
	Arrival   int64
	Burst     int64
	Remaining int64
}

// New 校验 arrival>=0、burst>=1，失败时不产生任何进程。
func New(pid, arrival, burst int64) (Proc, error) {
	if arrival < 0 || burst < 1 {
		return Proc{}, fmt.Errorf("%w: pid=%d arrival=%d burst=%d", ErrInvalidProcess, pid, arrival, burst)
	}
	return Proc{PID: pid, Arrival: arrival, Burst: burst, Remaining: burst}, nil
}

// Registry 登记进程并保证 pid 唯一；不是并发安全的，由调用方加锁。
type Registry struct {
	procs map[int64]Proc
}

func NewRegistry() *Registry { return &Registry{procs: make(map[int64]Proc)} }

// Add 先完整校验再登记；任何拒绝路径都不改变已登记进程。
func (r *Registry) Add(pid, arrival, burst int64) error {
	p, err := New(pid, arrival, burst)
	if err != nil {
		return err
	}
	if _, dup := r.procs[pid]; dup {
		return fmt.Errorf("%w: pid=%d", ErrDuplicatePID, pid)
	}
	r.procs[pid] = p
	return nil
}

// Snapshot 返回按 (arrival, pid) 升序排列的进程副本，供调度模拟使用。
func (r *Registry) Snapshot() []Proc {
	out := make([]Proc, 0, len(r.procs))
	for _, p := range r.procs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Arrival != out[j].Arrival {
			return out[i].Arrival < out[j].Arrival
		}
		return out[i].PID < out[j].PID
	})
	return out
}

// Len 返回已登记进程数。
func (r *Registry) Len() int { return len(r.procs) }
