package win

import (
	"reflect"
	"testing"
)

func TestQueueSemantics(t *testing.T) {
	cases := []struct {
		name         string
		window       int64
		pushes       []int64
		evictAt      []int64 // successive EvictExpired calls before assertions
		wantLastEv   int
		wantInWin    int
		wantSnapshot []int64
	}{
		{"empty queue", 10, nil, []int64{5}, 0, 0, nil},
		{"nothing expired at left edge", 10, []int64{0, 2, 5}, []int64{10}, 0, 3, []int64{0, 2, 5}},
		{"evict strictly below cutoff", 10, []int64{0, 2, 5}, []int64{11}, 1, 2, []int64{2, 5}},
		{"left edge closed: ts==cutoff kept", 10, []int64{2, 5, 11}, []int64{12}, 0, 3, []int64{2, 5, 11}},
		{"evict multiple from head", 10, []int64{2, 5, 11}, []int64{20}, 2, 1, []int64{11}},
		{"repeated eviction drains queue", 10, []int64{1, 2}, []int64{11, 12, 13}, 1, 0, []int64{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := New(tc.window)
			for _, p := range tc.pushes {
				q.Push(p)
			}
			evicted := 0
			for _, at := range tc.evictAt {
				evicted = q.EvictExpired(at)
			}
			if evicted != tc.wantLastEv {
				t.Fatalf("evicted = %d, want %d", evicted, tc.wantLastEv)
			}
			if got := q.InWindow(tc.evictAt[len(tc.evictAt)-1]); got != tc.wantInWin {
				t.Fatalf("InWindow = %d, want %d", got, tc.wantInWin)
			}
			got := q.Snapshot()
			if tc.wantSnapshot == nil {
				tc.wantSnapshot = []int64{}
			}
			if got == nil {
				got = []int64{}
			}
			if !reflect.DeepEqual(got, tc.wantSnapshot) {
				t.Fatalf("snapshot = %v, want %v", got, tc.wantSnapshot)
			}
		})
	}
}

// TestEvictionProbesConstant pins the head-pointer design: with m accepted
// requests still inside the window, one eviction pass inspects only the
// oldest entry and stops, so the probe count must not grow with m.
func TestEvictionProbesConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		q := New(int64(m) + 1)
		for i := 0; i < m; i++ {
			q.Push(int64(i)) // tightly adjacent, all still fresh
		}
		q.EvictExpired(int64(m - 1))
		if q.probes != 1 {
			t.Fatalf("m=%d: probes = %d, want 1 (head inspected once, found fresh)", m, q.probes)
		}
		if q.InWindow(int64(m-1)) != m {
			t.Fatalf("m=%d: InWindow = %d, want %d (nothing must be evicted)", m, q.InWindow(int64(m-1)), m)
		}
	}
}

// TestProbeBoundVerifiedExercisesCounter is the in-package check backing the
// demo; it must report true for the shipped head-pointer implementation.
func TestProbeBoundVerifiedExercisesCounter(t *testing.T) {
	if !ProbeBoundVerified() {
		t.Fatal("ProbeBoundVerified = false")
	}
}
