package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/api"
	"sync"
	"sync/atomic"
	"testing"
)

// TestSevenStepTrace pins the worked example from NOTES.md section 3.
func TestSevenStepTrace(t *testing.T) {
	a, _ := api.New(8)
	want := [][3]string{
		{"[0]", "-1", "0"}, {"[0 2]", "-1", "0"}, {"[0 2 3]", "-1", "0"}, {"[2 3]", "0", "10"}, {"[2 3]", "0", "10"},
		{"[1 2 3]", "0", "10"}, {"[]", "3", "100"},
	}
	ops := []struct{ o, d int64 }{{0, 10}, {2, 20}, {3, 30}, {-1, 0}, {2, 20}, {1, 40}, {-1, 0}}
	for i, op := range ops {
		if op.o < 0 {
			a.Commit()
		} else if err := a.Apply("k", op.o, op.d); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if got := ([3]string{fmt.Sprint(a.Pending()), fmt.Sprint(a.Checkpoint()), fmt.Sprint(a.Sum("k"))}); got != want[i] {
			t.Fatalf("step %d: got %+v want %+v", i+1, got, want[i])
		}
	}
}

// TestNaiveReference: random Apply/Commit interleavings vs brute force.
func TestNaiveReference(t *testing.T) {
	for _, tc := range [][4]int{{1, 300, 40, 64}, {2, 600, 90, 128}} {
		a, _ := api.New(tc[3])
		seen := map[int64][2]int64{} // offset -> [key index, delta]
		cpN, want := int64(-1), [3]int64{}
		rng := rand.New(rand.NewSource(int64(tc[0])))
		for i := 0; i < tc[1]; i++ {
			if rng.Intn(3) > 0 {
				o, k, d := int64(rng.Intn(tc[2])), rng.Intn(3), int64(rng.Intn(21)-10)
				if _, dup := seen[o]; !dup {
					seen[o] = [2]int64{int64(k), d}
				}
				_ = a.Apply(string(rune('a'+k)), o, d) // never full: maxP > maxOff
			} else {
				a.Commit()
				for r, ok := seen[cpN+1]; ok; r, ok = seen[cpN+1] {
					cpN++
					want[r[0]] += r[1]
				}
			}
			if a.Checkpoint() != cpN || [3]int64{a.Sum("a"), a.Sum("b"), a.Sum("c")} != want {
				t.Fatalf("tc=%v i=%d: cp=%d/%d", tc, i, a.Checkpoint(), cpN)
			}
		}
	}
}

// TestIdempotentAndRestore: dup Apply no-op; Restore drops only pending.
func TestIdempotentAndRestore(t *testing.T) {
	a, _ := api.New(8)
	for _, op := range [][2]int64{{0, 10}, {2, 20}, {3, 30}} {
		_ = a.Apply("k", op[0], op[1])
	}
	a.Commit() // cp=0, sum=10, pending={2,3}
	before := fmt.Sprint(a.Checkpoint(), a.Sum("k"), a.Pending())
	for _, dup := range [][2]int64{{0, 99}, {2, 99}} { // persisted + inflight
		if err := a.Apply("k", dup[0], dup[1]); err != nil || fmt.Sprint(a.Checkpoint(), a.Sum("k"), a.Pending()) != before {
			t.Fatalf("dup o=%d: err=%v, state mutated", dup[0], err)
		}
	}
	a.Restore()
	if a.Checkpoint() != 0 || a.Sum("k") != 10 || len(a.Pending()) != 0 {
		t.Fatalf("restore: cp=%d sum=%d pending=%v", a.Checkpoint(), a.Sum("k"), a.Pending())
	}
	for _, op := range [][2]int64{{1, 40}, {2, 20}, {3, 30}} { // redeliver from cp+1
		_ = a.Apply("k", op[0], op[1])
	}
	a.Commit()
	if a.Checkpoint() != 3 || a.Sum("k") != 100 {
		t.Fatalf("resume: cp=%d sum=%d", a.Checkpoint(), a.Sum("k"))
	}
}

// TestRejections: three distinguishable sentinels, no trace, still usable.
func TestRejections(t *testing.T) {
	a, _ := api.New(1)
	_ = a.Apply("k", 0, 7)
	before := fmt.Sprint(a.Checkpoint(), a.Sum("k"), a.Pending())
	for i, c := range [][2]error{
		{a.Apply("", 1, 1), api.ErrEmptyKey},
		{a.Apply("k", -1, 1), api.ErrNegativeOffset},
		{a.Apply("k", 9, 1), api.ErrPendingFull},
	} {
		if !errors.Is(c[0], c[1]) || fmt.Sprint(a.Checkpoint(), a.Sum("k"), a.Pending()) != before {
			t.Fatalf("case %d: err=%v, state mutated", i, c[0])
		}
	}
	if api.ErrEmptyKey == api.ErrNegativeOffset || api.ErrEmptyKey == api.ErrPendingFull || api.ErrNegativeOffset == api.ErrPendingFull {
		t.Fatal("sentinels not distinct")
	}
	a.Commit()
	if a.Checkpoint() != 0 || a.Sum("k") != 7 {
		t.Fatal("unusable after rejections")
	}
}

// TestConcurrentApply: concurrent distinct Apply == serial; cp monotone.
func TestConcurrentApply(t *testing.T) {
	const n = 128
	a, _ := api.New(n)
	var mono, stop atomic.Bool
	var wg sync.WaitGroup
	mono.Store(true)
	start := make(chan struct{})
	for r := 0; r < 4; r++ {
		go func() {
			prev := int64(-1)
			for !stop.Load() {
				if cp := a.Checkpoint(); cp < prev {
					mono.Store(false)
				} else {
					prev = cp
				}
			}
		}()
	}
	for g := int64(0); g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = a.Apply("k", g, g+1) // cannot fail: distinct offsets, capacity n
		}()
	}
	close(start)
	wg.Wait()
	stop.Store(true)
	a.Commit()
	if !mono.Load() || a.Checkpoint() != n-1 || a.Sum("k") != n*(n+1)/2 {
		t.Fatalf("mono=%v cp=%d sum=%d", mono.Load(), a.Checkpoint(), a.Sum("k"))
	}
}
func TestSelfCheck(t *testing.T) {
	if err := new(api.A).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
