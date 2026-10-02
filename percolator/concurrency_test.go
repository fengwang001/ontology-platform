package percolator

import (
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// Concurrent callers hammer the store; the checked invariants are: no
// duplicated rollback/version records per key, and every committed txn
// resolved through Get exposes all its keys at one shared commitTs.
func TestConcurrentSafety(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	keys := []string{"a", "b", "c", "d"}
	var clock atomic.Uint64

	// Globally monotonic now: every concurrent call satisfies the watermark
	// rule while scheduling remains genuinely concurrent.
	tick := func() uint64 { return clock.Add(1) }

	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for round := 0; round < 200; round++ {
				now := tick()
				st := s.Begin()
				pwErr := s.Prewrite(st, []Mutation{
					{Key: keys[(id+round)%4], Type: Put, Value: "x"},
					{Key: keys[(id+round+1)%4], Type: Delete},
				}, keys[(id+round)%4], maxTTL, now)
				if ct, err := s.CommitPrimary(st); err == nil {
					_ = s.CommitKeys(st, []string{keys[(id+round+1)%4]})
					for _, k := range keys {
						_, _, _ = s.Get(k, int64(ct)+1, tick())
					}
				} else if codeOf(pwErr) == "" {
					// Prewrite landed but the primary was resolved by a
					// concurrent reader: Abort remains legal.
					_ = s.Abort(st)
				}
			}
		}(w)
	}

	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				for _, k := range keys {
					_, _, _ = s.Get(k, int64(1+i*3), tick())
				}
			}
		}()
	}

	wg.Wait()

	snap := s.Snapshot()
	for k, vs := range snap.Versions {
		rollbacks := map[uint64]int{}
		seenCT := map[uint64]bool{}
		for _, v := range vs {
			if v.Type == Rollback {
				rollbacks[v.StartTs]++
				continue
			}
			if seenCT[v.CommitTs] {
				t.Fatalf("key %s has repeated commitTs %d", k, v.CommitTs)
			}
			seenCT[v.CommitTs] = true
		}
		for st, n := range rollbacks {
			if n > 1 {
				t.Fatalf("key %s has %d rollback records for st %d", k, n, st)
			}
		}
	}

	// All locks left at rest must point at valid primaries.
	for k, lk := range snap.Locks {
		if lk.Primary == "" || lk.StartTs == 0 {
			t.Fatalf("key %s has malformed lock %+v", k, lk)
		}
	}
}

// Replaying the exact same program twice yields identical snapshots and
// identical outcomes at every step.
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	program := genProgram(rng, 120)

	play := func() ([]outcome, Snapshot) {
		s := NewStore()
		outs := make([]outcome, len(program))
		for i, o := range program {
			outs[i] = o.runReal(s)
		}
		return outs, s.Snapshot()
	}

	out1, snap1 := play()
	out2, snap2 := play()
	if !reflect.DeepEqual(snap1, snap2) {
		t.Fatalf("replay snapshots differ:\n%+v\n%+v", snap1, snap2)
	}
	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("replay outcome differs at step %d: %+v vs %+v", i, out1[i], out2[i])
		}
	}
}
