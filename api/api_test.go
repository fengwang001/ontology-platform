package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// TestMatchesNaiveReference: Read must equal a naive append-only log with every
// truncated prefix removed, over many random operation sequences.
func TestMatchesNaiveReference(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 300; iter++ {
		s := api.New()
		var all []string
		var cut, cp uint64
		hasCP := false
		for step := 0; step < 2+r.Intn(18); step++ {
			switch r.Intn(4) {
			case 0, 1:
				p := fmt.Sprintf("p%d", len(all))
				if off, err := s.Append(p); err != nil || off != uint64(len(all)) {
					t.Fatalf("iter %d append: %d,%v", iter, off, err)
				}
				all = append(all, p)
			case 2:
				if len(all) == 0 {
					break
				}
				cand := uint64(r.Intn(len(all) + 2))
				legal := int(cand) < len(all) && (!hasCP || cand >= cp)
				if err := s.Checkpoint(cand); (err == nil) != legal {
					t.Fatalf("iter %d cp %d: err=%v legal=%v", iter, cand, err, legal)
				}
				if legal && (!hasCP || cand > cp) {
					cp, hasCP = cand, true
				}
			case 3:
				cand := uint64(r.Intn(len(all) + 2))
				legal := hasCP && cand <= cp
				if err := s.Truncate(cand); (err == nil) != legal {
					t.Fatalf("iter %d trunc %d: err=%v legal=%v", iter, cand, err, legal)
				}
				if legal && cand > cut {
					cut = cand
				}
			}
			if s.First() != cut {
				t.Fatalf("iter %d: f=%d want %d", iter, s.First(), cut)
			}
			for _, from := range []uint64{0, cut, uint64(r.Intn(len(all) + 2))} {
				es := s.Read(from)
				start := min(max(from, cut), uint64(len(all)))
				if len(es) != len(all)-int(start) {
					t.Fatalf("iter %d read(%d) len %d want %d", iter, from, len(es), len(all)-int(start))
				}
				for j, e := range es {
					if e.Offset != start+uint64(j) || e.Payload != all[start+uint64(j)] {
						t.Fatalf("iter %d read(%d) torn at %d", iter, from, j)
					}
				}
			}
		}
	}
}

// TestRejectedOpsLeaveNoTrace: distinct sentinels, no state change, still usable.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name string
		run  func(s *api.System) error
		err  error
	}{
		{"cp out of range", func(s *api.System) error { return s.Checkpoint(9) }, api.ErrCheckpointInvalid},
		{"cp retreat", func(s *api.System) error { return s.Checkpoint(0) }, api.ErrCheckpointInvalid},
		{"trunc beyond cp", func(s *api.System) error { return s.Truncate(3) }, api.ErrTruncateBeyondCheckpoint},
		{"recover over-delete", func(s *api.System) error { return s.Recover(3, 4) }, api.ErrRecoverOverDeletion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := api.New()
			for _, p := range []string{"a", "b", "c"} {
				s.Append(p)
			}
			s.Checkpoint(2)
			s.Truncate(2) // f=2, tm=2
			if err := tc.run(s); !errors.Is(err, tc.err) {
				t.Fatalf("got %v want %v", err, tc.err)
			}
			v, ok := s.CP()
			es := s.Read(0)
			if s.First() != 2 || !ok || v != 2 || len(es) != 1 || es[0].Offset != 2 {
				t.Fatal("rejected op changed state")
			}
			off, _ := s.Append("d")
			if es := s.Read(0); len(es) != 2 || es[1].Offset != off {
				t.Fatal("system not usable after rejection")
			}
		})
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentReadersCoherent: one append/truncate writer vs N readers, no
// sleeps; every view is a dense suffix, never a half-truncated prefix.
func TestConcurrentReadersCoherent(t *testing.T) {
	s := api.New()
	var wg sync.WaitGroup
	var stop, bad atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			off, _ := s.Append(fmt.Sprintf("p%d", i))
			if off > 0 && off%5 == 0 && s.Checkpoint(off-1) == nil {
				_ = s.Truncate(off - 1)
			}
		}
		stop.Store(true)
	}()
	for r := 0; r < 6; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				es := s.Read(0)
				for j, e := range es {
					if e.Offset != es[0].Offset+uint64(j) || e.Payload != fmt.Sprintf("p%d", e.Offset) {
						bad.Store(true)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("saw a non-contiguous view")
	}
}
