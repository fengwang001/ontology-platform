package api_test

import (
	"errors"
	"fmt"
	"ontology/api"
	"ontology/router"
	"sync"
	"sync/atomic"
	"testing"
)

func must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

// TestEightStepDerivation：NOTES.md 第三节八步，逐步核对计数与成败判定。
func TestEightStepDerivation(t *testing.T) {
	a := api.New()
	steps := []struct {
		op     func() error
		reject bool
		counts [3]int
	}{
		{func() error { return a.Assign("a", 0) }, false, [3]int{1, 0, 0}},
		{func() error { return a.Assign("b", 0) }, false, [3]int{2, 0, 0}},
		{func() error { return a.Assign("c", 1) }, false, [3]int{2, 1, 0}},
		{func() error { return a.Assign("d", 2) }, false, [3]int{2, 1, 1}},
		{func() error { return a.BeginDrain(2) }, false, [3]int{2, 1, 1}},
		{func() error { return a.Assign("e", 2) }, true, [3]int{2, 1, 1}},
		{func() error { return a.Put("d", "x") }, true, [3]int{2, 1, 1}},
		{func() error { return a.Migrate(2, 0) }, false, [3]int{3, 1, 0}},
	}
	for i, s := range steps {
		if err := s.op(); (err != nil) != s.reject {
			t.Fatalf("step %d: err=%v reject=%v", i+1, err, s.reject)
		}
		for p, w := range s.counts {
			if got := a.Count(p); got != w {
				t.Fatalf("step %d: Count(%d)=%d want %d", i+1, p, got, w)
			}
		}
	}
	if v, ok := a.Get("d"); ok || v != "" { // 被拒的 Put 不留痕
		t.Fatalf("Get(d)=%q,%v want empty", v, ok)
	}
	must(t, a.Put("d", "x")) // 迁移后 d 归属 Active 的 P0，可写
	if v, ok := a.Get("d"); !ok || v != "x" {
		t.Fatalf("Get(d)=%q,%v want x", v, ok)
	}
}

// TestRejectedOpsLeaveNoTrace：四类哨兵互不相同；被拒操作不改变状态，之后仍可正常使用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	sents := []error{router.ErrNoSuchPartition, router.ErrAffinityConflict, router.ErrNoAffinity, router.ErrBadMigrate}
	for i, si := range sents {
		for _, sj := range sents[i+1:] {
			if errors.Is(si, sj) || errors.Is(sj, si) {
				t.Fatalf("sentinels not distinct: %v vs %v", si, sj)
			}
		}
	}
	a := api.New()
	for k, p := range map[string]int{"a": 0, "b": 1, "c": 2} {
		must(t, a.Assign(k, p))
	}
	must(t, a.BeginDrain(2))
	must(t, a.Migrate(2, 0)) // P2 Removed，c 迁入 P0
	must(t, a.BeginDrain(1)) // 现状：P0 Active{a,c}，P1 Draining{b}，P2 Removed{}
	cases := []struct {
		op   func() error
		want error
	}{
		{func() error { return a.Assign("x", 9) }, router.ErrNoSuchPartition},
		{func() error { return a.Assign("x", 1) }, router.ErrAffinityConflict},
		{func() error { return a.Assign("x", 2) }, router.ErrAffinityConflict},
		{func() error { return a.Assign("a", 0) }, router.ErrAlreadyAssigned},
		{func() error { return a.Assign("", 0) }, router.ErrEmptyKey},
		{func() error { return a.Put("ghost", "v") }, router.ErrNoAffinity},
		{func() error { return a.Put("b", "v") }, router.ErrAffinityConflict},
		{func() error { return a.Migrate(9, 0) }, router.ErrNoSuchPartition},
		{func() error { return a.Migrate(0, 0) }, router.ErrBadMigrate},
		{func() error { return a.Migrate(0, 1) }, router.ErrBadMigrate},
		{func() error { return a.Migrate(2, 0) }, router.ErrBadMigrate},
		{func() error { return a.Migrate(1, 2) }, router.ErrBadMigrate},
	}
	for i, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Errorf("case %d: got %v want %v", i, err, c.want)
		}
	}
	for p, w := range map[int]int{0: 2, 1: 1, 2: 0} {
		if got := a.Count(p); got != w {
			t.Errorf("Count(%d)=%d want %d after rejected ops", p, got, w)
		}
	}
	if v, ok := a.Get("b"); ok || v != "" {
		t.Errorf("Get(b)=%q,%v after rejected put", v, ok)
	}
	if a.Assign("new", 0) != nil || a.Migrate(1, 0) != nil {
		t.Error("instance unusable after rejected ops")
	}
}

// TestConcurrentReadersAndMigrate：并发只读逐键逐分区一致，迁移原子，SelfCheck 可并发。
func TestConcurrentReadersAndMigrate(t *testing.T) {
	const K = 100
	a := api.New()
	for i := 0; i < K; i++ {
		must(t, a.Assign(fmt.Sprintf("k%03d", i), 2))
		must(t, a.Put(fmt.Sprintf("k%03d", i), "v"))
	}
	must(t, a.BeginDrain(2))
	done := make(chan struct{})
	var wg sync.WaitGroup
	var torn int32
	reader := func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			if n := a.Count(2); n != 0 && n != K { // 迁移原子：不得读到中间计数
				atomic.StoreInt32(&torn, 1)
				return
			}
			for i := 0; i < K; i++ { // 逐键一致：Draining/迁移期间值始终可读
				if v, ok := a.Get(fmt.Sprintf("k%03d", i)); !ok || v != "v" {
					atomic.StoreInt32(&torn, 1)
					return
				}
			}
			a.SelfCheck() // 并发可调用；正确性由 demo 与八步测试钉住
		}
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go reader()
	}
	must(t, a.Migrate(2, 0))
	close(done)
	wg.Wait()
	if torn != 0 || a.Count(2) != 0 || a.Count(0) != K {
		t.Fatalf("torn=%d post-migrate P2=%d P0=%d", torn, a.Count(2), a.Count(0))
	}
}
