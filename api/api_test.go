package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/repl"
)

// 第三节八步场景：逐步核对 NOTES.md 八行表（m2, m3, n2, n3, commitIndex）。
func TestEightSteps(t *testing.T) {
	c := api.NewFrom(3, 5, 1, 1, 1, 1, 1)
	steps := []struct {
		op   func() error
		want [5]int
	}{
		{func() error { return c.Replicate(2, true, 5) }, [5]int{5, 0, 6, 1, 0}},
		{func() error { return c.Replicate(3, true, 5) }, [5]int{5, 5, 6, 6, 0}},
		{func() error { return c.Append(5) }, [5]int{5, 5, 6, 6, 0}},
		{func() error { return c.Replicate(2, true, 6) }, [5]int{6, 5, 7, 6, 6}},
		{func() error { return c.Replicate(3, true, 6) }, [5]int{6, 6, 7, 7, 6}},
		{func() error { return c.Elect(6) }, [5]int{0, 0, 7, 7, 6}},
		{func() error { return c.Replicate(2, true, 6) }, [5]int{6, 0, 7, 7, 6}},
		{func() error { return c.Replicate(3, false, 2) }, [5]int{6, 0, 7, 3, 6}},
	}
	for i, s := range steps {
		if err := s.op(); err != nil {
			t.Fatalf("S%d: %v", i+1, err)
		}
		got := [5]int{c.MatchIndex(2), c.MatchIndex(3), c.NextIndex(2), c.NextIndex(3), c.CommitIndex()}
		if got != s.want {
			t.Fatalf("S%d: got %v want %v", i+1, got, s.want)
		}
	}
}

// 不变量 4：四类故障可判定、哨兵互不相同、被拒后状态不变且可继续使用。
func TestRejectedOpsNoStateChange(t *testing.T) {
	sentinels := []error{repl.ErrFollowerRange, repl.ErrAppendTerm, repl.ErrElectTerm, repl.ErrReplicateRange}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinels %d and %d not distinct", i, j)
			}
		}
	}
	c := api.NewFrom(3, 5, 1, 1, 1, 1, 1)
	if err := c.Replicate(2, true, 5); err != nil {
		t.Fatal(err)
	}
	snap := func() [6]int {
		return [6]int{c.CommitIndex(), c.MatchIndex(2), c.MatchIndex(3), c.NextIndex(2), c.NextIndex(3), c.Len()}
	}
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"follower-zero", func() error { return c.Replicate(0, true, 1) }, repl.ErrFollowerRange},
		{"follower-self", func() error { return c.Replicate(1, true, 1) }, repl.ErrFollowerRange},
		{"follower-over", func() error { return c.Replicate(4, true, 1) }, repl.ErrFollowerRange},
		{"append-term", func() error { return c.Append(6) }, repl.ErrAppendTerm},
		{"elect-equal", func() error { return c.Elect(5) }, repl.ErrElectTerm},
		{"elect-less", func() error { return c.Elect(3) }, repl.ErrElectTerm},
		{"replicate-neg", func() error { return c.Replicate(2, true, -1) }, repl.ErrReplicateRange},
		{"replicate-over", func() error { return c.Replicate(3, false, 6) }, repl.ErrReplicateRange},
	}
	for _, tc := range cases {
		before := snap()
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
		if after := snap(); before != after {
			t.Fatalf("%s: state changed %v -> %v", tc.name, before, after)
		}
	}
	if err := c.Replicate(3, true, 5); err != nil {
		t.Fatal(err)
	}
	if err := c.Append(5); err != nil {
		t.Fatal(err)
	}
}

// 并发：N 个 goroutine 并发只读，三元组逐字段相同；不用 sleep 造时序。
func TestConcurrentReads(t *testing.T) {
	c := api.NewFrom(3, 1, 1, 1, 1)
	if err := c.Replicate(2, true, 3); err != nil {
		t.Fatal(err)
	}
	if err := c.Replicate(3, true, 3); err != nil {
		t.Fatal(err)
	}
	want := [3]int{c.CommitIndex(), c.MatchIndex(2), c.MatchIndex(3)}
	start := make(chan struct{})
	errs := make(chan [3]int, 64)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 200; i++ {
				if got := [3]int{c.CommitIndex(), c.MatchIndex(2), c.MatchIndex(3)}; got != want {
					errs <- got
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for got := range errs {
		t.Fatalf("got %v want %v", got, want)
	}
}

// SelfCheck 自检方法可直接被测试调用。
func TestSelfCheck(t *testing.T) {
	for _, n := range []int{1, 3, 5} {
		if err := api.New(n).SelfCheck(); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
}
