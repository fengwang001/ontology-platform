// Package api 是对外入口：包装 cmt 的操作并提供 SelfCheck 自检。依赖 cmt。
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/cmt"
)

// Committer 是对外暴露的提交器。
type Committer struct{ c *cmt.Committer }

// New 构造提交器，maxInFlight 为每分区允许的在途（已投递未提交）上限。
func New(maxInFlight int) *Committer { return &Committer{c: cmt.New(maxInFlight)} }

func (a *Committer) Assign(p int, start int64) error { return a.c.Assign(p, start) }
func (a *Committer) Deliver(p int, off int64) error  { return a.c.Deliver(p, off) }
func (a *Committer) Ack(p int, off int64) error      { return a.c.Ack(p, off) }
func (a *Committer) Committed(p int) (int64, bool)   { return a.c.Committed(p) }
func (a *Committer) Commit() map[int]int64           { return a.c.Commit() }
func (a *Committer) Restart()                        { a.c.Restart() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (a *Committer) SelfCheck() error {
	if err := checkNaive(); err != nil {
		return err
	}
	return checkFailures()
}

// naive 朴素参照：从起点逐个检查已 Ack 集合，遇第一个未 Ack 位点停下。
func naive(start int64, acked map[int64]bool) int64 {
	for acked[start] {
		start++
	}
	return start
}

// checkNaive 核验不变量 1/2/3：随机交错 Deliver/Ack（含重复 Ack）后，
// Committed 必须等于朴素参照——隐含 [起点,C) 全部已 Ack（1）且 C 不能再大（2）。
func checkNaive() error {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		c := cmt.New(1 << 20)
		start, n := int64(rng.Intn(1000)), 1+rng.Intn(64)
		acked := map[int64]bool{}
		if err := c.Assign(trial%3, start); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			if err := c.Deliver(trial%3, start+int64(i)); err != nil {
				return err
			}
		}
		for _, i := range rng.Perm(n) {
			off := start + int64(i)
			if rng.Intn(4) == 0 {
				_ = c.Ack(trial%3, off) // 重复 Ack，幂等
			}
			if err := c.Ack(trial%3, off); err != nil {
				return err
			}
			acked[off] = true
			got, _ := c.Committed(trial % 3)
			if want := naive(start, acked); got != want {
				return fmt.Errorf("selfcheck: committed=%d want=%d", got, want)
			}
		}
	}
	return nil
}

// checkFailures 核验不变量 4：四类错误可判定且互不相同，被拒后状态不变、仍可用。
// 在途校验先于连续性校验，故用两个提交器分别制造两类 Deliver 错误。
func checkFailures() error {
	c1 := cmt.New(4) // 在途有富余：覆盖未 Assign / 不连续 / 越界
	if err := c1.Assign(0, 10); err != nil {
		return err
	}
	if err := c1.Deliver(0, 10); err != nil {
		return err
	}
	c2 := cmt.New(1) // 在途打满：覆盖超限
	if err := c2.Assign(1, 0); err != nil {
		return err
	}
	if err := c2.Deliver(1, 0); err != nil {
		return err
	}
	before := fmt.Sprint(c1.Commit(), c2.Commit())
	errs := []error{
		c1.Ack(9, 0),      // 分区未 Assign
		c1.Deliver(0, 12), // Deliver 不连续（上界是 11）
		c1.Ack(0, 11),     // Ack 越界（上界是 11）
		c2.Deliver(1, 1),  // 在途数超限
	}
	seen := map[error]bool{}
	for _, e := range errs {
		if e == nil || seen[e] {
			return fmt.Errorf("selfcheck: 错误不可判定或不互不相同: %v", e)
		}
		seen[e] = true
	}
	if fmt.Sprint(c1.Commit(), c2.Commit()) != before {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	if err := c1.Ack(0, 10); err != nil { // 被拒后仍可正常使用
		return err
	}
	if got, _ := c1.Committed(0); got != 11 {
		return fmt.Errorf("selfcheck: committed=%d want=11", got)
	}
	return nil
}
