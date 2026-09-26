// demo 依次核验：六步推导、三类可判定错误、拒绝不留痕、
// Winner 读取数不随规模增长、并发只读一致、SelfCheck。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/elect"
)

var failed bool

func report(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func main() {
	sixStep()
	sentinelErrors()
	rejectedKeepsState()
	winnerReadBound()
	concurrentRead()
	c := api.New(3)
	report("selfcheck", c.SelfCheck() == nil, "invariants 1-4")
	if failed {
		os.Exit(1)
	}
}

// 第三节六步：逐步核对 Winner 与关键 term/votedFor。
func sixStep() {
	c := api.New(3)
	type want struct{ win, t1, v1 int }
	steps := []struct {
		run  func() error
		want want
	}{
		{func() error { return c.StartElection(0) }, want{-1, 0, -1}},
		{func() error { return c.RequestVote(1, 0, 1) }, want{0, 1, 0}},
		{func() error { return c.RequestVote(2, 0, 1) }, want{0, 1, 0}},
		{func() error { return c.RequestVote(2, 1, 1) }, want{0, 1, 0}},
		{func() error { return c.RequestVote(0, 2, 0) }, want{0, 1, 0}},
		{func() error { return c.StartElection(1) }, want{0, 2, 1}},
	}
	ok, wins := true, []int{}
	for _, s := range steps {
		if err := s.run(); err != nil {
			ok = false
		}
		wins = append(wins, c.Winner())
		got := want{c.Winner(), c.Term(1), c.VotedFor(1)}
		if got != s.want {
			ok = false
		}
	}
	report("six-step", ok, fmt.Sprintf("winners=%v term1/votedFor1@S6=%d/%d", wins, c.Term(1), c.VotedFor(1)))
}

// 三类故障注入：哨兵错误互不相同。
func sentinelErrors() {
	c := api.New(3)
	e1 := c.StartElection(3)
	e2 := c.RequestVote(0, 1, -1)
	e3 := c.RequestVote(1, 1, 1)
	ok := errors.Is(e1, elect.ErrNodeIndex) && errors.Is(e2, elect.ErrBadTerm) &&
		errors.Is(e3, elect.ErrSelfVote) &&
		e1 != e2 && e2 != e3 && e1 != e3
	report("sentinel-errors", ok, "index/term/self distinct")
}

// 被拒操作不改变任何状态，且集群仍可正常使用。
func rejectedKeepsState() {
	c := api.New(3)
	_ = c.StartElection(0)
	_ = c.RequestVote(1, 0, 1)
	before := fmt.Sprintf("%d%d%d", c.Term(0), c.Term(1), c.VotedFor(1))
	_ = c.RequestVote(0, 2, 0) // 过期任期
	_ = c.RequestVote(1, 1, 1) // 自投票
	_ = c.StartElection(-1)    // 越界
	after := fmt.Sprintf("%d%d%d", c.Term(0), c.Term(1), c.VotedFor(1))
	ok := before == after && c.RequestVote(2, 0, 1) == nil && c.Winner() == 0
	report("reject-no-trace", ok, "state unchanged, still usable")
}

// m 取多档奇数规模，Winner 的节点读取数不随 m 增长。
func winnerReadBound() {
	ok := true
	for _, m := range []int{101, 501, 1001, 5001, 9999} {
		c := api.New(m)
		_ = c.StartElection(0)
		for i := 1; i < m; i++ {
			_ = c.RequestVote(i, 0, 1)
		}
		if c.Winner() != 0 || !c.WinnerReadBounded() {
			ok = false
		}
	}
	report("winner-read-bound", ok, "m=101..9999 reads constant")
}

// N 个 goroutine 并发只读 Winner，结果必须逐字段相同。
func concurrentRead() {
	c := api.New(5)
	_ = c.StartElection(0)
	for i := 1; i < 5; i++ {
		_ = c.RequestVote(i, 0, 1)
	}
	const n = 64
	var wg sync.WaitGroup
	res := make([]int, n)
	start := make(chan struct{})
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			<-start
			res[k] = c.Winner()
		}(g)
	}
	close(start)
	wg.Wait()
	ok := true
	for _, r := range res {
		if r != 0 {
			ok = false
		}
	}
	report("concurrent-read", ok, "64 goroutines agree")
}
