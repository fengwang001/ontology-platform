package api_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/api"
	"ontology/elect"
)

// TestSixStepScenario 复现 NOTES.md 第三节的六步推导，逐步核对状态与 Winner。
func TestSixStepScenario(t *testing.T) {
	a, err := api.New(3)
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		run          func()
		terms, votes []int
		winner       int
	}{
		{func() { _ = a.StartElection(0) }, []int{1, 0, 0}, []int{0, -1, -1}, -1},
		{func() { _, _ = a.RequestVote(1, 0, 1) }, []int{1, 1, 0}, []int{0, 0, -1}, 0},
		{func() { _, _ = a.RequestVote(2, 0, 1) }, []int{1, 1, 1}, []int{0, 0, 0}, 0},
		{func() { _, _ = a.RequestVote(2, 1, 1) }, []int{1, 1, 1}, []int{0, 0, 0}, 0},
		{func() { _, _ = a.RequestVote(0, 2, 0) }, []int{1, 1, 1}, []int{0, 0, 0}, 0},
		{func() { _ = a.StartElection(1) }, []int{1, 2, 1}, []int{0, 1, 0}, 0},
	}
	for i, s := range steps {
		s.run()
		terms, votes := a.Snapshot()
		if !reflect.DeepEqual(terms, s.terms) || !reflect.DeepEqual(votes, s.votes) || a.Winner() != s.winner {
			t.Fatalf("S%d: term=%v votedFor=%v winner=%d, want %v %v %d",
				i+1, terms, votes, a.Winner(), s.terms, s.votes, s.winner)
		}
	}
}

// TestRejectedOpsLeaveState 三类故障各有可判定且互不相同的哨兵错误，
// 被拒后状态不变且集群仍可正常使用。
func TestRejectedOpsLeaveState(t *testing.T) {
	a, _ := api.New(3)
	_ = a.StartElection(0)
	_, _ = a.RequestVote(1, 0, 1)
	bt, bv := a.Snapshot()
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"start-out-of-range", func() error { return a.StartElection(3) }, elect.ErrNodeOutOfRange},
		{"vote-out-of-range", func() error { _, e := a.RequestVote(-1, 0, 1); return e }, elect.ErrNodeOutOfRange},
		{"cand-out-of-range", func() error { _, e := a.RequestVote(1, 7, 1); return e }, elect.ErrNodeOutOfRange},
		{"negative-term", func() error { _, e := a.RequestVote(1, 0, -1); return e }, elect.ErrBadTerm},
		{"self-vote", func() error { _, e := a.RequestVote(2, 2, 7); return e }, elect.ErrSelfVote},
	}
	seen := map[error]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
			at, av := a.Snapshot()
			if !reflect.DeepEqual(bt, at) || !reflect.DeepEqual(bv, av) {
				t.Fatal("rejected op mutated state")
			}
			seen[tc.want] = true
		})
	}
	if len(seen) != 3 {
		t.Fatalf("want 3 distinct sentinel errors, got %d", len(seen))
	}
	if ok, err := a.RequestVote(2, 0, 1); !ok || err != nil { // 被拒后仍可正常使用
		t.Fatalf("cluster unusable after rejections: ok=%v err=%v", ok, err)
	}
}

// TestOneVotePerTerm 同任期已投出的票不可改，只有更高任期才能改投。
func TestOneVotePerTerm(t *testing.T) {
	a, _ := api.New(3)
	_ = a.StartElection(0)
	if ok, _ := a.RequestVote(1, 0, 1); !ok {
		t.Fatal("first vote should be granted")
	}
	if ok, _ := a.RequestVote(1, 2, 1); ok {
		t.Fatal("same-term revote for another candidate granted")
	}
	if _, v := a.Snapshot(); v[1] != 0 {
		t.Fatalf("votedFor[1]=%d, want 0", v[1])
	}
	if ok, _ := a.RequestVote(1, 0, 1); !ok {
		t.Fatal("same-term revote for same candidate should be granted")
	}
	if ok, _ := a.RequestVote(1, 2, 2); !ok {
		t.Fatal("higher-term change should be granted")
	}
	if tm, v := a.Snapshot(); tm[1] != 2 || v[1] != 2 {
		t.Fatalf("term=%d votedFor=%d, want 2/2", tm[1], v[1])
	}
}

// TestTermMonotone 任意操作序列后任何节点的 term 都不得回退。
func TestTermMonotone(t *testing.T) {
	a, _ := api.New(5)
	ops := []func(){
		func() { _ = a.StartElection(0) },
		func() { _, _ = a.RequestVote(1, 0, 1) },
		func() { _, _ = a.RequestVote(2, 0, 0) }, // 过期任期，拒绝
		func() { _ = a.StartElection(3) },
		func() { _, _ = a.RequestVote(4, 3, 5) },
		func() { _ = a.StartElection(0) },
	}
	prev, _ := a.Snapshot()
	for i, op := range ops {
		op()
		now, _ := a.Snapshot()
		for j := range now {
			if now[j] < prev[j] {
				t.Fatalf("op %d: term[%d] regressed %d -> %d", i, j, prev[j], now[j])
			}
		}
		prev = now
	}
}

// TestSelfCheck 各档奇数规模的 SelfCheck 都必须通过；偶数规模被拒绝。
func TestSelfCheck(t *testing.T) {
	for _, n := range []int{1, 3, 5, 9} {
		a, err := api.New(n)
		if err != nil {
			t.Fatalf("New(%d): %v", n, err)
		}
		if err := a.SelfCheck(); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
	if _, err := api.New(4); !errors.Is(err, api.ErrClusterSize) {
		t.Fatalf("New(4): err=%v, want ErrClusterSize", err)
	}
}
