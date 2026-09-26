// Package api 是对外面层：包装 repl 的簿记操作并提供 SelfCheck 自检。
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/repl"
)

// Cluster 是以节点 1 为 leader 的 n 节点集群簿记视图。
type Cluster struct {
	n int
	l *repl.Leader
}

// New 建 n 节点集群，初始任期 1、空日志。
func New(n int) *Cluster { return &Cluster{n: n, l: repl.New(n, 1)} }

// NewFrom 建预置状态的集群：当前任期 term，日志为任期 terms 的条目。
func NewFrom(n, term int, terms ...int) *Cluster {
	return &Cluster{n: n, l: repl.New(n, term, terms...)}
}

func (c *Cluster) Append(term int) error                 { return c.l.Append(term) }        // 追加当前任期条目
func (c *Cluster) Replicate(f int, ok bool, r int) error { return c.l.Replicate(f, ok, r) } // follower 回报
func (c *Cluster) Elect(t int) error                     { return c.l.Elect(t) }            // 任期 t 重新当选
func (c *Cluster) CommitIndex() int                      { return c.l.CommitIndex() }       // 推进并返回 commitIndex
func (c *Cluster) MatchIndex(f int) int                  { return c.l.MatchIndex(f) }       // f 的 matchIndex
func (c *Cluster) NextIndex(f int) int                   { return c.l.NextIndex(f) }        // f 的 nextIndex
func (c *Cluster) Len() int                              { return c.l.Len() }               // 日志最后下标

// naiveCommit 朴素重算：扫描 1..Len，找满足「当前任期 + 多数派」的最大 i，否则为 prev。
func (c *Cluster) naiveCommit(prev int) int {
	best := -1
	for i := 1; i <= c.l.Len(); i++ {
		if c.l.Term(i) != c.l.CurrentTerm() {
			continue
		}
		cnt := 1
		for f := 2; f <= c.n; f++ {
			if c.l.MatchIndex(f) >= i {
				cnt++
			}
		}
		if cnt >= c.n/2+1 {
			best = i
		}
	}
	if best < 0 {
		return prev
	}
	return best
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (c *Cluster) SelfCheck() error {
	for _, chk := range []func() error{checkScenario, checkRandom, checkRejections} {
		if err := chk(); err != nil {
			return err
		}
	}
	return nil
}

// checkScenario 重放第三节八步，对照手工推导表（不变量 1、2、3）。
func checkScenario() error {
	c := NewFrom(3, 5, 1, 1, 1, 1, 1)
	ops := []func() error{
		func() error { return c.Replicate(2, true, 5) },
		func() error { return c.Replicate(3, true, 5) },
		func() error { return c.Append(5) },
		func() error { return c.Replicate(2, true, 6) },
		func() error { return c.Replicate(3, true, 6) },
		func() error { return c.Elect(6) },
		func() error { return c.Replicate(2, true, 6) },
		func() error { return c.Replicate(3, false, 2) },
	}
	want := [][5]int{{5, 0, 6, 1, 0}, {5, 5, 6, 6, 0}, {5, 5, 6, 6, 0}, {6, 5, 7, 6, 6},
		{6, 6, 7, 7, 6}, {0, 0, 7, 7, 6}, {6, 0, 7, 7, 6}, {6, 0, 7, 3, 6}}
	for i, op := range ops {
		if err := op(); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, err)
		}
		got := [5]int{c.MatchIndex(2), c.MatchIndex(3), c.NextIndex(2), c.NextIndex(3), c.CommitIndex()}
		if got != want[i] {
			return fmt.Errorf("selfcheck step %d: got %v want %v", i+1, got, want[i])
		}
	}
	return nil
}

// checkRandom 随机操作序列：逐步核对朴素重算、簿记边界、commitIndex 单调（不变量 1、2、3）。
func checkRandom() error {
	r := rand.New(rand.NewSource(1))
	c := New(5)
	term, prev := 1, 0
	for step := 0; step < 2000; step++ {
		switch r.Intn(3) {
		case 0:
			_ = c.Append(term)
		case 1:
			_ = c.Replicate(2+r.Intn(c.n-1), r.Intn(2) == 0, r.Intn(c.Len()+1))
		case 2:
			term++
			_ = c.Elect(term)
		}
		ci := c.CommitIndex()
		if ci != c.naiveCommit(prev) || ci < prev {
			return fmt.Errorf("selfcheck: commit %d at step %d (prev %d)", ci, step, prev)
		}
		for f := 2; f <= c.n; f++ {
			if m, nx := c.MatchIndex(f), c.NextIndex(f); m < 0 || m > c.Len() || nx < 1 || nx > c.Len()+1 {
				return fmt.Errorf("selfcheck: bookkeeping out of bounds at step %d", step)
			}
		}
		prev = ci
	}
	return nil
}

// checkRejections 四类故障注入：错误可判定，被拒后状态不变（不变量 4）。
func checkRejections() error {
	c := NewFrom(3, 5, 1, 1, 1, 1, 1)
	before := [4]int{c.MatchIndex(2), c.NextIndex(2), c.CommitIndex(), c.Len()}
	bad := []struct {
		op   func() error
		want error
	}{
		{func() error { return c.Replicate(1, true, 5) }, repl.ErrFollowerRange},
		{func() error { return c.Append(4) }, repl.ErrAppendTerm},
		{func() error { return c.Elect(5) }, repl.ErrElectTerm},
		{func() error { return c.Replicate(2, true, 6) }, repl.ErrReplicateRange},
	}
	for _, b := range bad {
		if err := b.op(); !errors.Is(err, b.want) {
			return fmt.Errorf("selfcheck: want %v, got %v", b.want, err)
		}
	}
	if after := [4]int{c.MatchIndex(2), c.NextIndex(2), c.CommitIndex(), c.Len()}; before != after {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}
