// Package probe 执行对外的保活事件（Activity/Tick），依赖 keep 的状态与规则。
// 在 keep.State 之外加一把互斥锁，使读访问与事件处理可被多 goroutine 并发调用。
package probe

import (
	"sync"

	"ontology/keep"
)

// Runner 把 keep.State 包装成可并发调用的执行器。
type Runner struct {
	mu sync.Mutex
	st *keep.State
}

// New 创建执行器；参数透传给 keep.New，非法时返回其哨兵错误，不留状态。
func New(idleTimeout, probeInterval, maxProbes int64) (*Runner, error) {
	st, err := keep.New(idleTimeout, probeInterval, maxProbes)
	if err != nil {
		return nil, err
	}
	return &Runner{st: st}, nil
}

// Activity 对端有响应。已死返回 keep.ErrDead、时钟回退返回 keep.ErrClockBack；
// 拒绝发生在 keep.State 任何写入之前，状态整体不变。
func (r *Runner) Activity(now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st.Activity(now)
}

// Tick 推进时钟，返回本次是否发出探测、是否刚刚判死。
func (r *Runner) Tick(now int64) (probeSent bool, becameDead bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st.Tick(now)
}

// Probes 返回连续无响应的探测次数。
func (r *Runner) Probes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st.Probes()
}

// Dead 返回连接是否已被判死。
func (r *Runner) Dead() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st.Dead()
}

// LastActive 返回最后一次活动时刻。
func (r *Runner) LastActive() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st.LastActive()
}
