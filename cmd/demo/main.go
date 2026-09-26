package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/repl"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
	}
	fmt.Printf("%s %s\n", name, verdict)
}

// 第三节八步场景：n=3，term=5，log=[1,1,1,1,1]，逐步核对 NOTES.md 八行表。
func eightSteps() bool {
	c := api.NewFrom(3, 5, 1, 1, 1, 1, 1)
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
		if op() != nil {
			return false
		}
		got := [5]int{c.MatchIndex(2), c.MatchIndex(3), c.NextIndex(2), c.NextIndex(3), c.CommitIndex()}
		if got != want[i] {
			return false
		}
	}
	return true
}

// 四类故障注入：哨兵错误互不相同，且各自可判定。
func faultInjection() bool {
	sentinels := []error{repl.ErrFollowerRange, repl.ErrAppendTerm, repl.ErrElectTerm, repl.ErrReplicateRange}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				return false
			}
		}
	}
	c := api.NewFrom(3, 5, 1, 1, 1, 1, 1)
	ops := []func() error{
		func() error { return c.Replicate(1, true, 1) }, // 对 leader 自己复制
		func() error { return c.Append(4) },             // 任期非当前任期
		func() error { return c.Elect(5) },              // 任期不大于当前任期
		func() error { return c.Replicate(2, true, 6) }, // r 越界
	}
	for i, op := range ops {
		if !errors.Is(op(), sentinels[i]) {
			return false
		}
	}
	return true
}

// 被拒操作不改变任何状态，且实例仍可正常使用。
func rejectKeepsState() bool {
	c := api.NewFrom(3, 5, 1, 1, 1, 1, 1)
	_ = c.Replicate(2, true, 5)
	snap := func() [5]int {
		return [5]int{c.CommitIndex(), c.MatchIndex(2), c.MatchIndex(3), c.NextIndex(2), c.Len()}
	}
	before := snap()
	_ = c.Replicate(1, true, 5)
	_ = c.Append(4)
	_ = c.Elect(5)
	_ = c.Replicate(2, false, 99)
	return before == snap() && c.Replicate(3, true, 5) == nil
}

// 多 goroutine 并发只读，读到的三元组逐字段相同。
func concurrentReads() bool {
	c := api.NewFrom(3, 1, 1, 1, 1)
	_ = c.Replicate(2, true, 3)
	_ = c.Replicate(3, true, 3)
	want := [3]int{c.CommitIndex(), c.MatchIndex(2), c.MatchIndex(3)}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var bad int32
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				if got := [3]int{c.CommitIndex(), c.MatchIndex(2), c.MatchIndex(3)}; got != want {
					atomic.StoreInt32(&bad, 1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	return bad == 0
}

func main() {
	check("eight-steps", eightSteps())
	check("fault-injection", faultInjection())
	check("reject-keeps-state", rejectKeepsState())
	check("reads-constant", repl.CheckCommitReads())
	check("concurrent-reads", concurrentReads())
	check("selfcheck", api.New(3).SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
