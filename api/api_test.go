package api_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func TestRejections(t *testing.T) {
	if _, err := api.New(0); !errors.Is(err, api.ErrBadConfig) {
		t.Fatalf("New(0) = %v, want ErrBadConfig", err)
	}
	s1, _ := api.New(1)
	s2, _ := api.New(1)
	_ = s1.Apply("k", "a", 1, 5)
	_ = s2.Apply("k", "a", 1, 1)
	cases := []struct{ err, want error }{
		{s1.Apply("", "v", 1, 1), api.ErrEmptyKey},
		{s1.Apply("k", "v", 2, 5), api.ErrNonMonotonicIngest},
		{s2.Apply("k", "v", 2, 2), api.ErrCapacity},
	}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("case %d: got %v, want %v", i, c.err, c.want)
		}
	}
	if api.ErrBadConfig == api.ErrEmptyKey || api.ErrBadConfig == api.ErrNonMonotonicIngest ||
		api.ErrBadConfig == api.ErrCapacity || api.ErrBadConfig == api.ErrNotFound ||
		api.ErrEmptyKey == api.ErrNonMonotonicIngest || api.ErrEmptyKey == api.ErrCapacity ||
		api.ErrEmptyKey == api.ErrNotFound || api.ErrNonMonotonicIngest == api.ErrCapacity ||
		api.ErrNonMonotonicIngest == api.ErrNotFound || api.ErrCapacity == api.ErrNotFound {
		t.Fatal("sentinels not distinct")
	}
}

func TestFailedApplyNoSideEffect(t *testing.T) {
	s, _ := api.New(2)
	_ = s.Apply("k", "a", 10, 1)
	_ = s.Apply("k", "b", 20, 2)
	beforeE, _ := s.LatestEvent("k")
	beforeI, _ := s.LatestIngest("k")
	_ = s.Apply("", "x", 1, 3) // rejected-return nil is pinned in TestRejections
	_ = s.Apply("k", "x", 99, 2)
	_ = s.Apply("k", "x", 99, 3)
	afterE, _ := s.LatestEvent("k")
	afterI, _ := s.LatestIngest("k")
	at, _ := s.AtEvent("k", 15)
	if afterE != beforeE || afterI != beforeI || at.Value != "a" {
		t.Fatal("rejected applies changed state")
	}
	if err := s.Apply("k2", "ok", 1, 1); err != nil {
		t.Fatal(err)
	}
}

func TestIngestStrictlyIncreasing(t *testing.T) {
	s, _ := api.New(10)
	for in := int64(1); in <= 8; in++ {
		if err := s.Apply("k", fmt.Sprintf("v%d", in), 0, in); err != nil {
			t.Fatal(err)
		}
		v, err := s.AtIngest("k", in)
		if err != nil || v.In != in {
			t.Fatalf("AtIngest(%d) = %+v, %v", in, v, err)
		}
	}
	for _, in := range []int64{8, 3} { // duplicate and regressed In both rejected
		if err := s.Apply("k", "x", 0, in); !errors.Is(err, api.ErrNonMonotonicIngest) {
			t.Fatalf("In=%d accepted: %v", in, err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	s, _ := api.New(4)
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func readLoop(s *api.System, done *atomic.Bool, wg *sync.WaitGroup) {
	defer wg.Done()
	for !done.Load() {
		_, _ = s.LatestEvent("shared")
		_, _ = s.AtEvent("shared", 10)
		_, _ = s.AtIngest("shared", 10)
		_ = s.SelfCheck()
	}
}

func TestConcurrentApplyAndQuery(t *testing.T) {
	s, _ := api.New(64)
	var done atomic.Bool
	var readers, writers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go readLoop(s, &done, &readers)
	}
	const nk, ns = 16, 32
	for i := 0; i < nk; i++ {
		writers.Add(1)
		go func(i int) { defer writers.Done(); _ = s.Apply(fmt.Sprintf("k%d", i), "v", 1, 1) }(i)
	}
	var mu sync.Mutex
	acc := map[int64]bool{}
	for in := int64(1); in <= ns; in++ {
		writers.Add(1)
		go func(in int64) {
			defer writers.Done()
			if err := s.Apply("shared", fmt.Sprintf("s%d", in), in, in); err == nil {
				mu.Lock()
				acc[in] = true
				mu.Unlock()
			} else if !errors.Is(err, api.ErrNonMonotonicIngest) {
				t.Errorf("In=%d: unexpected %v", in, err)
			}
		}(in)
	}
	writers.Wait()
	done.Store(true)
	readers.Wait()
	for i := 0; i < nk; i++ {
		if v, err := s.LatestIngest(fmt.Sprintf("k%d", i)); err != nil || v.In != 1 {
			t.Fatalf("k%d: %+v, %v", i, v, err)
		}
	}
	obs, prev := map[int64]bool{}, int64(0) // observed In set must equal accepted set, increasing
	for tt := int64(1); tt <= ns; tt++ {
		w, err := s.AtIngest("shared", tt)
		if errors.Is(err, api.ErrNotFound) {
			continue
		}
		if err != nil || w.In < prev {
			t.Fatalf("AtIngest(%d) = %+v, %v (prev=%d)", tt, w, err, prev)
		}
		obs[w.In], prev = true, w.In
	}
	if len(obs) != len(acc) { // obs subset acc: AtIngest only returns stored versions
		t.Fatalf("observed=%d accepted=%d", len(obs), len(acc))
	}
}
