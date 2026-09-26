package api_test

import (
	"errors"
	"math/rand"
	"slices"
	"testing"

	"ontology/api"
	"ontology/elect"
)

func TestSelfCheck(t *testing.T) {
	if err := api.New(3).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// replay 回放一段随机操作序列（含越界/负任期/自投等非法操作），每步后回调。
func replay(n, seed, steps int, after func(c *api.Cluster, k int)) {
	r := rand.New(rand.NewSource(int64(seed)))
	c := api.New(n)
	for k := 0; k < steps; k++ {
		if r.Intn(2) == 0 {
			_ = c.StartElection(r.Intn(n+1) - 1)
		} else {
			_ = c.RequestVote(r.Intn(n+1)-1, r.Intn(n+1)-1, r.Intn(4)-1)
		}
		if after != nil {
			after(c, k)
		}
	}
}

// 不变量 1：Winner 必须等于朴素重算。
func TestWinnerMatchesNaive(t *testing.T) {
	for _, tc := range []struct{ n, seed, steps int }{{3, 1, 200}, {5, 2, 400}, {9, 3, 600}, {21, 4, 800}} {
		replay(tc.n, tc.seed, tc.steps, func(c *api.Cluster, k int) {
			if got, want := c.Winner(), naive(c); got != want {
				t.Fatalf("n=%d seed=%d step=%d: winner=%d, naive=%d", tc.n, tc.seed, k, got, want)
			}
		})
	}
}

// 不变量 2：同一 (节点, 任期) 上至多一票，投出后同任期不可更改。
func TestOneVotePerTerm(t *testing.T) {
	cast := map[[2]int]int{}
	replay(7, 42, 800, func(c *api.Cluster, k int) {
		for i := 0; i < c.N(); i++ {
			tm, v := c.Term(i), c.VotedFor(i)
			if v == -1 {
				continue
			}
			key := [2]int{i, tm}
			if prev, ok := cast[key]; ok && prev != v {
				t.Fatalf("step %d: node %d revoted in term %d: %d -> %d", k, i, tm, prev, v)
			}
			cast[key] = v
		}
	})
}

// 不变量 3：任何节点的 term 不得回退。
func TestTermMonotonic(t *testing.T) {
	terms := map[int]int{}
	replay(7, 43, 800, func(c *api.Cluster, k int) {
		for i := 0; i < c.N(); i++ {
			if c.Term(i) < terms[i] {
				t.Fatalf("step %d: node %d term regressed to %d", k, i, c.Term(i))
			}
			terms[i] = c.Term(i)
		}
	})
}

// 不变量 4：三类非法操作给出互不相同的哨兵错误，被拒后状态不变、仍可使用。
func TestRejectedNoStateChange(t *testing.T) {
	c := api.New(3)
	_ = c.StartElection(0)
	_ = c.RequestVote(1, 0, 1)
	snap := func() (s [][2]int) {
		for i := 0; i < c.N(); i++ {
			s = append(s, [2]int{c.Term(i), c.VotedFor(i)})
		}
		return s
	}
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"start-oob-neg", func() error { return c.StartElection(-1) }, elect.ErrNodeIndex},
		{"start-oob-high", func() error { return c.StartElection(3) }, elect.ErrNodeIndex},
		{"vote-target-oob", func() error { return c.RequestVote(3, 0, 1) }, elect.ErrNodeIndex},
		{"vote-cand-oob", func() error { return c.RequestVote(0, 3, 1) }, elect.ErrNodeIndex},
		{"vote-neg-term", func() error { return c.RequestVote(2, 0, -1) }, elect.ErrBadTerm},
		{"vote-self", func() error { return c.RequestVote(2, 2, 2) }, elect.ErrSelfVote},
	}
	if elect.ErrNodeIndex == elect.ErrBadTerm || elect.ErrBadTerm == elect.ErrSelfVote ||
		elect.ErrNodeIndex == elect.ErrSelfVote {
		t.Fatal("sentinel errors must be distinct")
	}
	for _, tc := range cases {
		before := snap()
		if err := tc.run(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err=%v, want %v", tc.name, err, tc.want)
		}
		if !slices.Equal(snap(), before) {
			t.Fatalf("%s: state changed after rejection", tc.name)
		}
	}
	if err := c.RequestVote(2, 0, 1); err != nil || c.Winner() != 0 {
		t.Fatal("cluster unusable after rejections")
	}
}

// naive 朴素重算：扫描全部节点统计得票，取达到多数派者。
func naive(c *api.Cluster) int {
	n := c.N()
	cnt := map[int]int{}
	for i := 0; i < n; i++ {
		if v := c.VotedFor(i); v != -1 {
			cnt[v]++
		}
	}
	for cand, k := range cnt {
		if k >= n/2+1 {
			return cand
		}
	}
	return -1
}
