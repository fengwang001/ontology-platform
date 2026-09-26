package bq

import (
	"sync"
	"testing"
)

// TestMovedCounterNotLinear proves Consume(1) does not shift/copy the backing
// slice: the unexported moved counter stays 0 regardless of queue depth m.
func TestMovedCounterNotLinear(t *testing.T) {
	for _, m := range []int64{100, 500, 1000, 5000, 10000} {
		r := New(m)
		if !r.TryProduce(m) {
			t.Fatalf("m=%d: produce failed", m)
		}
		if !r.TryConsume(1) {
			t.Fatalf("m=%d: consume failed", m)
		}
		if r.moved != 0 {
			t.Fatalf("m=%d: moved=%d, want 0 (must not grow with queue depth)", m, r.moved)
		}
		if r.Count() != m-1 || r.Free() != 1 {
			t.Fatalf("m=%d: count=%d free=%d", m, r.Count(), r.Free())
		}
	}
}

func TestRingAllOrNone(t *testing.T) {
	cases := []struct {
		name          string
		capacity      int64
		ops           []op
		wantCount     int64
		wantProduceOK []bool
		wantConsumeOK []bool
	}{
		{
			name:          "fill exactly then backpressure",
			capacity:      5,
			ops:           []op{{p: 3}, {p: 2}, {p: 1}, {c: 2}, {p: 3}},
			wantCount:     3,
			wantProduceOK: []bool{true, true, false, false},
			wantConsumeOK: []bool{true},
		},
		{
			name:          "underflow removes nothing",
			capacity:      5,
			ops:           []op{{p: 2}, {c: 6}, {c: 2}},
			wantCount:     0,
			wantProduceOK: []bool{true},
			wantConsumeOK: []bool{false, true},
		},
		{
			name:          "wrapping around head pointer",
			capacity:      4,
			ops:           []op{{p: 4}, {c: 3}, {p: 3}, {c: 4}},
			wantCount:     0,
			wantProduceOK: []bool{true, true},
			wantConsumeOK: []bool{true, true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(tc.capacity)
			var pi, ci int
			for _, o := range tc.ops {
				if o.p > 0 {
					if got := r.TryProduce(o.p); got != tc.wantProduceOK[pi] {
						t.Fatalf("Produce(%d)=%v want %v", o.p, got, tc.wantProduceOK[pi])
					}
					pi++
				} else {
					if got := r.TryConsume(o.c); got != tc.wantConsumeOK[ci] {
						t.Fatalf("Consume(%d)=%v want %v", o.c, got, tc.wantConsumeOK[ci])
					}
					ci++
				}
				if r.Count() < 0 || r.Count() > tc.capacity {
					t.Fatalf("count %d out of [0,%d]", r.Count(), tc.capacity)
				}
			}
			if r.Count() != tc.wantCount {
				t.Fatalf("count=%d want %d", r.Count(), tc.wantCount)
			}
		})
	}
}

func TestRingConcurrentProduce(t *testing.T) {
	const n = 200
	r := New(n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !r.TryProduce(1) {
				t.Error("produce failed")
			}
		}()
	}
	wg.Wait()
	if r.Count() != n {
		t.Fatalf("count=%d want %d", r.Count(), n)
	}
}

type op struct {
	p, c int64
}
