// Package api 是位点提交器的对外门面，暴露 Assign/Deliver/Ack/Committed/
// Commit/Restart 与内置自检 SelfCheck。
package api

import (
	"errors"
	"math/rand"

	"ontology/cmt"
	"ontology/ofs"
)

// 四类可判定哨兵错误，互不相同（两个来自 ofs，两个来自 cmt）。
var (
	ErrPartitionNotAssigned = cmt.ErrPartitionNotAssigned // 分区未经 Assign
	ErrDeliverGap           = ofs.ErrDeliverGap           // Deliver 不连续
	ErrAckOutOfRange        = ofs.ErrAckOutOfRange        // Ack 越过已投递上界
	ErrTooManyInFlight      = cmt.ErrTooManyInFlight      // 在途位点数超限
)

// Committer 是位点提交器的对外句柄，并发安全。
type Committer struct{ m *cmt.Manager }

func New(maxInFlight int) *Committer { return &Committer{m: cmt.NewManager(maxInFlight)} }

// Assign 声明分区 p，start 等价于其初始已提交位点；重复声明不改既有状态。
func (c *Committer) Assign(p int, start int64)      { c.m.Assign(p, start) }
func (c *Committer) Deliver(p int, off int64) error { return c.m.Deliver(p, off) }
func (c *Committer) Ack(p int, off int64) error     { return c.m.Ack(p, off) }
func (c *Committer) Committed(p int) (int64, error) { return c.m.Committed(p) }
func (c *Committer) Commit() map[int]int64          { return c.m.Commit().Committed }
func (c *Committer) Restart()                       { c.m.Restart() }

// naive 是不变量 3 的朴素参照：独立保存 Ack 集合，从起点逐个扫描，遇首未 Ack 即停。
type naive struct {
	start, deliv int64
	acked        map[int64]bool
}

func newNaive(start int64) *naive {
	return &naive{start: start, deliv: start, acked: map[int64]bool{}}
}
func (n *naive) ack(off int64) bool {
	if off < n.start || off >= n.deliv {
		return false
	}
	n.acked[off] = true
	return true
}
func (n *naive) committed() int64 {
	c := n.start
	for n.acked[c] {
		c++
	}
	return c
}

func deliverRange(c *Committer, p int, from, to int64) error {
	for off := from; off < to; off++ {
		if err := c.Deliver(p, off); err != nil {
			return err
		}
	}
	return nil
}

// SelfCheck 对内置操作序列核验第二节四条不变量；不改接收者状态，任一不符即返回错误。
func (c *Committer) SelfCheck() error {
	s := New(0) // 不变量 1/2/3：第三节七步序列逐步比对 C
	s.Assign(0, 100)
	if err := deliverRange(s, 0, 100, 108); err != nil {
		return err
	}
	acks := []int64{103, 100, 101, 105, 101, 102, 107}
	want := []int64{100, 101, 102, 102, 102, 104, 104}
	for i, a := range acks {
		if err := s.Ack(0, a); err != nil {
			return err
		}
		if got, _ := s.Committed(0); got != want[i] {
			return errors.New("api: self-check section-3 mismatch")
		}
	}
	s.Restart() // 重启后须从 C=104 重投 104..107
	if got, _ := s.Committed(0); got != 104 || deliverRange(s, 0, 104, 108) != nil {
		return errors.New("api: self-check restart redeliver failed")
	}
	// 不变量 3（蕴含 1、2）：多分区随机 Ack 顺序，逐步比对朴素参照。
	r := rand.New(rand.NewSource(1))
	for _, p := range []int{1, 7} {
		start := int64(p * 50)
		s.Assign(p, start)
		nv := newNaive(start)
		if err := deliverRange(s, p, start, start+60); err != nil {
			return errors.New("api: self-check deliver failed")
		}
		nv.deliv = start + 60 // 投递连续，朴素模型同步已投递上界
		for _, idx := range r.Perm(60) {
			off := start + int64(idx)
			if err := s.Ack(p, off); err != nil || !nv.ack(off) {
				return errors.New("api: self-check ack failed")
			}
			if got, _ := s.Committed(p); got != nv.committed() {
				return errors.New("api: self-check naive mismatch")
			}
		}
	}
	return checkSentinelsAndRejections() // 不变量 4
}

// checkSentinelsAndRejections 核验四类哨兵互不相同、拒绝不留痕、拒后可用。
func checkSentinelsAndRejections() error {
	sen := []error{ErrPartitionNotAssigned, ErrDeliverGap, ErrAckOutOfRange, ErrTooManyInFlight}
	seen := map[error]bool{}
	for _, e := range sen {
		if seen[e] {
			return errors.New("api: self-check sentinels not distinct")
		}
		seen[e] = true
	}
	f := New(1)
	f.Assign(2, 10)
	checks := []struct {
		err  error
		call func() error
	}{
		{ErrPartitionNotAssigned, func() error { return f.Deliver(9, 0) }},
		{ErrDeliverGap, func() error { return f.Deliver(2, 11) }},
		{nil, func() error { return f.Deliver(2, 10) }},
		{ErrTooManyInFlight, func() error { return f.Deliver(2, 11) }},
		{ErrAckOutOfRange, func() error { return f.Ack(2, 99) }},
	}
	for _, c := range checks {
		if err := c.call(); !errors.Is(err, c.err) {
			return errors.New("api: self-check rejection error mismatch")
		}
	}
	if got, _ := f.Committed(2); got != 10 {
		return errors.New("api: rejected call left a trace")
	}
	if err := f.Ack(2, 10); err != nil {
		return err
	}
	if err := f.Deliver(2, 11); err != nil { // 被拒后仍可正常使用
		return errors.New("api: partition unusable after rejection")
	}
	return nil
}
