// Package cmt 管理多个分区的位点提交：Assign/Deliver/Ack/Committed/Commit/Restart。
// 依赖 ofs，并发安全（互斥锁）。
package cmt

import (
	"errors"
	"sync"

	"ontology/ofs"
)

var (
	// ErrUnassigned 表示分区尚未 Assign。
	ErrUnassigned = errors.New("cmt: partition not assigned")
	// ErrTooManyInFlight 表示已投递未提交的位点数达到 maxInFlight。
	ErrTooManyInFlight = errors.New("cmt: too many in-flight offsets")
)

// Committer 是多分区提交器。
type Committer struct {
	mu          sync.Mutex
	maxInFlight int64
	parts       map[int]*ofs.State
}

// New 构造提交器，maxInFlight 为每分区允许的在途（已投递未提交）上限。
func New(maxInFlight int) *Committer {
	return &Committer{maxInFlight: int64(maxInFlight), parts: map[int]*ofs.State{}}
}

// Assign 声明分区起点（初始已提交位点 = start）；重复 Assign 幂等。
func (c *Committer) Assign(p int, start int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.parts[p]; !ok {
		c.parts[p] = ofs.New(start)
	}
	return nil
}

// Deliver 登记一条投递。先校验（未 Assign / 在途超限 / 不连续），全部通过才变更状态。
func (c *Committer) Deliver(p int, off int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.parts[p]
	if !ok {
		return ErrUnassigned
	}
	if st.InFlight() >= c.maxInFlight {
		return ErrTooManyInFlight
	}
	return st.Deliver(off)
}

// Ack 登记一条处理完成。未 Assign 或越界时报错且状态不变；重复 Ack 幂等。
func (c *Committer) Ack(p int, off int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.parts[p]
	if !ok {
		return ErrUnassigned
	}
	return st.Ack(off)
}

// Committed 返回分区已提交位点；未 Assign 时 ok=false。
func (c *Committer) Committed(p int) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.parts[p]
	if !ok {
		return 0, false
	}
	return st.Committed(), true
}

// Commit 返回全部分区可提交位点的快照。
func (c *Committer) Commit() map[int]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := make(map[int]int64, len(c.parts))
	for p, st := range c.parts {
		snap[p] = st.Committed()
	}
	return snap
}

// Restart 模拟崩溃重启：丢弃所有未提交状态，按已提交位点重建。
func (c *Committer) Restart() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, st := range c.parts {
		st.Reset()
	}
}
