package api

import (
	"errors"
	"sync"
	"testing"
)

// drive runs a deterministic pseudo-random op sequence over n servers,
// invoking check after every step with the successful-op tallies.
func drive(t *testing.T, n, steps int, check func(lb *LB, acquired, released int)) {
	t.Helper()
	lb, _ := New(n)
	acquired, released := 0, 0
	seed := uint64(n)*1469598103934665603 + 11
	rand := func(m int) int { // xorshift, deterministic
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(m))
	}
	for step := 0; step < steps; step++ {
		i := rand(n + 2) // sometimes out of range: must be rejected
		if rand(3) == 2 {
			if lb.Release(i) == nil {
				released++
			}
		} else if lb.Acquire(i) == nil {
			acquired++
		}
		check(lb, acquired, released)
	}
}

// TestCountsNonNegative nails invariant 2: counts never go below zero.
func TestCountsNonNegative(t *testing.T) {
	for _, n := range []int{1, 2, 3, 9, 50} {
		drive(t, n, 300, func(lb *LB, _, _ int) {
			for j := 0; j < n; j++ {
				if c, err := lb.Count(j); err != nil || c < 0 {
					t.Fatalf("n=%d: Count(%d)=%d, err=%v", n, j, c, err)
				}
			}
		})
	}
}

// TestConservation nails invariant 3: sum of counts equals successful
// Acquires minus successful Releases.
func TestConservation(t *testing.T) {
	for _, n := range []int{1, 3, 16, 200} {
		drive(t, n, 300, func(lb *LB, acquired, released int) {
			sum := 0
			for j := 0; j < n; j++ {
				c, _ := lb.Count(j)
				sum += c
			}
			if sum != acquired-released {
				t.Fatalf("n=%d: sum=%d, acquired-released=%d", n, sum, acquired-released)
			}
		})
	}
}

// TestRejectedOpsLeaveNoTrace nails invariant 4: rejected ops change
// nothing and the balancer keeps working afterwards.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name string
		op   func(lb *LB) error
		want error
	}{
		{"acquire bad index", func(lb *LB) error { return lb.Acquire(-1) }, ErrBadIndex},
		{"release bad index", func(lb *LB) error { return lb.Release(3) }, ErrBadIndex},
		{"release underflow", func(lb *LB) error { return lb.Release(2) }, ErrUnderflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lb, _ := New(3)
			_ = lb.Acquire(0)
			_ = lb.Acquire(0)
			_ = lb.Acquire(1)
			if err := tc.op(lb); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
			for j, want := range []int{2, 1, 0} {
				if c, _ := lb.Count(j); c != want {
					t.Fatalf("Count(%d)=%d, want %d: reject left a trace", j, c, want)
				}
			}
			if err := lb.Acquire(2); err != nil {
				t.Fatalf("balancer broken after reject: %v", err)
			}
		})
	}
}

// TestThreeDistinctErrors: the three failure kinds are distinguishable.
func TestThreeDistinctErrors(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrBadConfig) {
		t.Fatalf("New(0) err=%v, want ErrBadConfig", err)
	}
	for _, p := range [][2]error{{ErrBadConfig, ErrBadIndex}, {ErrBadIndex, ErrUnderflow}, {ErrUnderflow, ErrBadConfig}} {
		if errors.Is(p[0], p[1]) || errors.Is(p[1], p[0]) {
			t.Fatalf("errors not distinct: %v vs %v", p[0], p[1])
		}
	}
}

// TestConcurrentAcquire: N goroutines each Acquire(0) once; the final
// count must be exactly N and every observed count stays non-negative.
func TestConcurrentAcquire(t *testing.T) {
	for _, n := range []int{64, 500, 2000} {
		lb, _ := New(4)
		var wg sync.WaitGroup
		stop, done := make(chan struct{}), make(chan struct{})
		go func() { // observer: counts must stay non-negative
			defer close(done)
			for {
				select {
				case <-stop:
					return
				default:
					for j := 0; j < 4; j++ {
						if c, _ := lb.Count(j); c < 0 {
							t.Errorf("negative count c%d=%d", j, c)
							return
						}
					}
					_ = lb.Pick()
				}
			}
		}()
		for k := 0; k < n; k++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = lb.Acquire(0)
			}()
		}
		wg.Wait()
		close(stop)
		<-done
		if c, _ := lb.Count(0); c != n {
			t.Fatalf("n=%d: Count(0)=%d, want %d", n, c, n)
		}
	}
}
