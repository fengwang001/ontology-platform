package api_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// TestAPIFacade drives the whole system only through the exported API and
// checks counts, drain read semantics and the atomic migrate outcome.
func TestAPIFacade(t *testing.T) {
	s := api.New(0, 1, 2)
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	must := func(step string, err error) {
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	must("a", s.Assign("a", 0))
	must("b", s.Assign("b", 0))
	must("c", s.Assign("c", 1))
	must("d", s.Assign("d", 2))
	if got := [3]int{s.Count(0), s.Count(1), s.Count(2)}; got != [3]int{2, 1, 1} {
		t.Fatalf("counts %v", got)
	}
	must("drain", s.BeginDrain(2))
	if v, ok := s.Get("d"); ok || v != "" {
		t.Fatal("d unwritten but Draining read must be empty+false")
	}
	if err := s.Put("d", "x"); !errors.Is(err, api.ErrAffinityConflict) {
		t.Fatalf("put on draining err=%v", err)
	}
	if err := s.Assign("e", 2); !errors.Is(err, api.ErrAffinityConflict) {
		t.Fatalf("assign on draining err=%v", err)
	}
	must("migrate", s.Migrate(2, 0))
	if got := [3]int{s.Count(0), s.Count(1), s.Count(2)}; got != [3]int{3, 1, 0} {
		t.Fatalf("post-migrate counts %v", got)
	}
	if v, ok := s.Get("d"); ok || v != "" {
		t.Fatalf("Get(d)=(%q,%v), want empty+false", v, ok)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestAPIErrorSentinels is table-driven: each rejected public op yields its
// distinct, judgeable sentinel and leaves counts unchanged.
func TestAPIErrorSentinels(t *testing.T) {
	cases := []struct {
		name string
		run  func(*api.System) error
		want error
	}{
		{"partition missing", func(s *api.System) error { return s.Assign("z", 99) }, api.ErrPartitionNotFound},
		{"affinity conflict", func(s *api.System) error { return s.Assign("z", 2) }, api.ErrAffinityConflict},
		{"no affinity", func(s *api.System) error { return s.Put("ghost", "v") }, api.ErrNoAffinity},
		{"invalid migrate same", func(s *api.System) error { return s.Migrate(1, 1) }, api.ErrInvalidMigrate},
		{"invalid migrate state", func(s *api.System) error { return s.Migrate(0, 1) }, api.ErrInvalidMigrate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := api.New(0, 1, 2)
			_ = s.Assign("a", 0)
			_ = s.BeginDrain(2)
			_ = s.Migrate(2, 0)
			before := [3]int{s.Count(0), s.Count(1), s.Count(2)}
			if err := tc.run(s); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			after := [3]int{s.Count(0), s.Count(1), s.Count(2)}
			if before != after || s.SelfCheck() != nil {
				t.Fatalf("state changed %v->%v", before, after)
			}
		})
	}
}

// TestConcurrentReadAtomicMigrate: many readers vs one migrating writer. Every
// observation is strictly pre- or post-migrate, per-key Gets stay identical;
// coordination uses channels only (no sleeps).
func TestConcurrentReadAtomicMigrate(t *testing.T) {
	s := api.New(0, 1)
	const n = 128
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("k%d", i)
		if s.Assign(k, 1) != nil || s.Put(k, "v") != nil {
			t.Fatal("setup")
		}
	}
	if s.BeginDrain(1) != nil {
		t.Fatal("drain setup")
	}
	const readers = 16
	var wg sync.WaitGroup
	started := make(chan struct{}, readers)
	sawPost := make(chan struct{}, readers)
	stop := make(chan struct{})
	var once sync.Once
	var bad atomic.Bool
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			postSeen := false
			for {
				c := s.Count(1) // single atomic read
				if c != n && c != 0 {
					bad.Store(true)
				}
				if c == 0 && !postSeen {
					postSeen = true
					sawPost <- struct{}{}
				}
				for i := 0; i < n; i++ { // per-key Get identical before/after
					if v, ok := s.Get(fmt.Sprintf("k%d", i)); !ok || v != "v" {
						bad.Store(true)
					}
				}
				once.Do(func() { close(started) })
				select {
				case <-stop:
					return
				default:
				}
			}
		}()
	}
	for g := 0; g < readers; g++ {
		<-started
	}
	if err := s.Migrate(1, 0); err != nil { // before this, readers saw only pre
		t.Fatal(err)
	}
	for g := 0; g < readers; g++ {
		<-sawPost // post state is permanent, so this must arrive without sleeping
	}
	close(stop)
	wg.Wait()
	if bad.Load() || s.Count(1) != 0 || s.Count(0) != n || s.SelfCheck() != nil {
		t.Fatalf("torn/inconsistent read; check %v", s.SelfCheck())
	}
}
