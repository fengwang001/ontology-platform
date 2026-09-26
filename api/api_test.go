package api

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestRandomMatchesNaive: random sequences match a naive explicit slice and conserve exactly (invariants 1, 3).
func TestRandomMatchesNaive(t *testing.T) {
	for _, capV := range []int64{1, 7, 64} {
		q, _ := New(capV)
		r := rand.New(rand.NewSource(1))
		var ref []struct{}
		var sumP, sumC int64
		for step := 0; step < 2000; step++ {
			n := int64(1 + r.Intn(int(capV)+2)) // sometimes beyond capacity
			if r.Intn(2) == 0 {
				ok, _ := q.Produce(n)
				if ok != (int64(len(ref))+n <= capV) {
					t.Fatalf("cap=%d step=%d: backpressure mismatch", capV, step)
				}
				if ok {
					ref = append(ref, make([]struct{}, n)...)
					sumP += n
				}
			} else if err := q.Consume(n); n > int64(len(ref)) {
				if !errors.Is(err, ErrUnderflow) {
					t.Fatalf("cap=%d step=%d want underflow got %v", capV, step, err)
				}
			} else {
				ref, sumC = ref[n:], sumC+n
			}
			if q.Count() != int64(len(ref)) || sumP-sumC != q.Count() {
				t.Fatalf("cap=%d step=%d: count=%d naive=%d", capV, step, q.Count(), len(ref))
			}
		}
	}
}

func TestBounds(t *testing.T) {
	cases := []struct {
		cap int64
		ops []int64
	}{{1, []int64{1, 1, -1, -1}}, {5, []int64{5, 5, -5, 5, 6, -6}}}
	for _, tc := range cases {
		q, _ := New(tc.cap)
		for _, op := range tc.ops {
			if op > 0 {
				q.Produce(op)
			} else {
				q.Consume(-op)
			}
			if c := q.Count(); c < 0 || c > tc.cap || c+q.Free() != tc.cap {
				t.Fatalf("cap=%d: count=%d free=%d", tc.cap, c, q.Free())
			}
		}
	}
}

func TestConservation(t *testing.T) {
	q, _ := New(10)
	var sumP, sumC int64
	for i, b := range [][2]int64{{4, 2}, {6, 8}, {3, 1}, {7, 5}} {
		if ok, _ := q.Produce(b[0]); ok {
			sumP += b[0]
		}
		if q.Consume(b[1]) == nil {
			sumC += b[1]
		}
		if q.Count() != sumP-sumC {
			t.Fatalf("step %d: count=%d want %d", i, q.Count(), sumP-sumC)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	for _, c := range []int64{0, -4} {
		if _, err := New(c); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("cap=%d: %v", c, err)
		}
	}
	q, _ := New(5)
	q.Produce(3)
	reject := func(prod bool, n int64, want error) {
		before := q.Count()
		var err error
		var ok bool
		if prod {
			ok, err = q.Produce(n)
		} else {
			err = q.Consume(n)
		}
		if !errors.Is(err, want) || ok || q.Count() != before {
			t.Fatalf("prod=%v n=%d: err=%v ok=%v %d->%d", prod, n, err, ok, before, q.Count())
		}
	}
	reject(true, 0, ErrInvalidProduce)
	reject(true, -9, ErrInvalidProduce)
	reject(false, 0, ErrInvalidConsume)
	reject(false, -1, ErrInvalidConsume)
	reject(false, 4, ErrUnderflow)
	if ok, err := q.Produce(3); ok || err != nil || q.Count() != 3 {
		t.Fatal("backpressure left a trace")
	}
	if ok, _ := q.Produce(2); !ok || q.Count() != 5 || q.Consume(5) != nil || q.Count() != 0 {
		t.Fatal("queue not usable after rejections")
	}
}

func TestConcurrentProduce(t *testing.T) {
	const N = 300
	q, _ := New(N)
	var running atomic.Bool
	running.Store(true)
	var readers, prods sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for running.Load() {
			if c := q.Count(); c < 0 || c > N {
				t.Errorf("bounds violated: count=%d", c)
			}
		}
	}()
	for range N {
		prods.Add(1)
		go func() {
			defer prods.Done()
			if ok, err := q.Produce(1); !ok || err != nil {
				t.Errorf("produce failed: ok=%v err=%v", ok, err)
			}
		}()
	}
	prods.Wait()
	running.Store(false)
	readers.Wait()
	if q.Count() != N {
		t.Fatalf("count=%d want %d", q.Count(), N)
	}
}

func TestSelfCheck(t *testing.T) {
	q, _ := New(5)
	if !q.SelfCheck() {
		t.Fatal("SelfCheck returned false")
	}
}
