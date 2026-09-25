package epoch

import (
	"errors"
	"testing"
)

func TestEnterExitSentinels(t *testing.T) {
	r := New()
	if err := r.Exit(7); !errors.Is(err, ErrNotActive) {
		t.Fatalf("Exit inactive = %v, want ErrNotActive", err)
	}
	if err := r.Enter(7); err != nil {
		t.Fatalf("Enter = %v", err)
	}
	if err := r.Enter(7); !errors.Is(err, ErrDuplicateEnter) {
		t.Fatalf("dup Enter = %v, want ErrDuplicateEnter", err)
	}
	if err := r.Exit(7); err != nil {
		t.Fatalf("Exit = %v", err)
	}
	if err := r.Exit(7); !errors.Is(err, ErrNotActive) {
		t.Fatalf("second Exit = %v, want ErrNotActive", err)
	}
}

func TestMinActive(t *testing.T) {
	r := New()
	if _, ok := r.MinActive(); ok {
		t.Fatal("MinActive with no threads should report ok=false")
	}
	must := func(want int64) {
		t.Helper()
		if got, ok := r.MinActive(); !ok || got != want {
			t.Fatalf("MinActive = (%d,%v), want (%d,true)", got, ok, want)
		}
	}
	_ = r.Enter(1)
	_ = r.Enter(2)
	must(0) // both at G=0
	r.Advance()
	_ = r.Enter(3)
	must(0) // threads 1,2 still announced at 0
	_ = r.Exit(1)
	_ = r.Exit(2) // epoch-0 bucket emptied, floor must walk past it
	must(1)
	_ = r.Exit(3)
	r.Advance()
	_ = r.Enter(4) // announces at G=2
	must(2)
}

// TestMinActiveConstantWork pins the O(1) per-epoch-count design: the minimum
// computation must inspect ZERO thread records regardless of how many threads
// are active (a whole-table scan would inspect m).
func TestMinActiveConstantWork(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			if err := r.Enter(i); err != nil {
				t.Fatalf("m=%d Enter(%d)=%v", m, i, err)
			}
		}
		if e, ok := r.MinActive(); !ok || e != 0 {
			t.Fatalf("m=%d MinActive=(%d,%v)", m, e, ok)
		}
		if r.lastMinScannedThreads != 0 {
			t.Fatalf("m=%d inspected %d thread records, want constant 0", m, r.lastMinScannedThreads)
		}
		r.Advance()
		for i := 0; i < m/2; i++ { // migrate half to epoch 1
			_ = r.Exit(i)
			_ = r.Enter(i)
		}
		_, _ = r.MinActive()
		if r.lastMinScannedThreads != 0 {
			t.Fatalf("m=%d post-migration inspected %d, want 0", m, r.lastMinScannedThreads)
		}
	}
}
