package elect

import (
	"errors"
	"sync"
	"testing"
)

// 第四节：m 取 100~10000 多档奇数，全部投给候选 0，
// Winner 读取的节点个数不随 m 线性增长（不超过小常数 1）。
func TestWinnerReadBound(t *testing.T) {
	for _, m := range []int{101, 501, 1001, 5001, 9999} {
		c := New(m)
		mustOK(t, c.StartElection(0))
		for i := 1; i < m; i++ {
			mustOK(t, c.RequestVote(i, 0, 1))
		}
		if got := c.Winner(); got != 0 {
			t.Fatalf("m=%d: winner=%d, want 0", m, got)
		}
		if c.lastReads > 1 {
			t.Fatalf("m=%d: Winner read %d nodes, want <= 1", m, c.lastReads)
		}
	}
}

// 第五节：三类故障注入的哨兵错误互不相同，被拒后状态不变且仍可使用。
func TestSentinelErrors(t *testing.T) {
	if ErrNodeIndex == ErrBadTerm || ErrBadTerm == ErrSelfVote || ErrNodeIndex == ErrSelfVote {
		t.Fatal("sentinel errors must be distinct")
	}
	c := New(3)
	mustOK(t, c.StartElection(0))
	mustOK(t, c.RequestVote(1, 0, 1))
	snap := func() [6]int {
		return [6]int{c.Term(0), c.Term(1), c.Term(2), c.VotedFor(0), c.VotedFor(1), c.VotedFor(2)}
	}
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"start-oob-high", func() error { return c.StartElection(3) }, ErrNodeIndex},
		{"start-oob-neg", func() error { return c.StartElection(-1) }, ErrNodeIndex},
		{"vote-target-oob", func() error { return c.RequestVote(9, 0, 1) }, ErrNodeIndex},
		{"vote-cand-oob", func() error { return c.RequestVote(0, -1, 1) }, ErrNodeIndex},
		{"vote-neg-term", func() error { return c.RequestVote(0, 1, -1) }, ErrBadTerm},
		{"vote-self", func() error { return c.RequestVote(2, 2, 5) }, ErrSelfVote},
	}
	for _, tc := range cases {
		before := snap()
		if err := tc.run(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err=%v, want %v", tc.name, err, tc.want)
		}
		if after := snap(); after != before {
			t.Fatalf("%s: state changed after rejection", tc.name)
		}
	}
	mustOK(t, c.RequestVote(2, 0, 1))
	if c.Winner() != 0 {
		t.Fatal("cluster unusable after rejections")
	}
}

// 第六节：N 个 goroutine 并发只读 Winner，结果逐字段相同（无 sleep）。
func TestConcurrentWinner(t *testing.T) {
	c := New(5)
	mustOK(t, c.StartElection(0))
	for i := 1; i < 5; i++ {
		mustOK(t, c.RequestVote(i, 0, 1))
	}
	const g = 64
	res := make([]int, g)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for k := 0; k < g; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			<-start
			res[k] = c.Winner()
		}(k)
	}
	close(start)
	wg.Wait()
	for k, r := range res {
		if r != 0 {
			t.Fatalf("goroutine %d read winner=%d, want 0", k, r)
		}
	}
}

// 第三节六步推导：逐步核对 Winner 与节点 1 的 term/votedFor。
func TestSixStepScenario(t *testing.T) {
	c := New(3)
	steps := []struct {
		elect       bool // true=StartElection(i)，false=RequestVote(i,cand,tm)
		i, cand, tm int
		win, t1, v1 int
	}{
		{true, 0, 0, 0, -1, 0, -1},
		{false, 1, 0, 1, 0, 1, 0},
		{false, 2, 0, 1, 0, 1, 0},
		{false, 2, 1, 1, 0, 1, 0},
		{false, 0, 2, 0, 0, 1, 0},
		{true, 1, 0, 0, 0, 2, 1},
	}
	for k, s := range steps {
		var err error
		if s.elect {
			err = c.StartElection(s.i)
		} else {
			err = c.RequestVote(s.i, s.cand, s.tm)
		}
		if err != nil {
			t.Fatalf("S%d: %v", k+1, err)
		}
		if c.Winner() != s.win || c.Term(1) != s.t1 || c.VotedFor(1) != s.v1 {
			t.Fatalf("S%d: got (%d,%d,%d), want (%d,%d,%d)", k+1,
				c.Winner(), c.Term(1), c.VotedFor(1), s.win, s.t1, s.v1)
		}
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
