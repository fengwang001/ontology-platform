package api

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"
)

func TestNewRejectsBadWindow(t *testing.T) {
	for _, w := range []int64{0, -1, -100} {
		d, err := New(w)
		if !errors.Is(err, ErrInvalidWindow) || d != nil {
			t.Fatalf("New(%d) = (%v,%v)", w, d, err)
		}
	}
}

func TestWalkAndErrors(t *testing.T) {
	d, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	// The mandated 11-event sequence through the public surface.
	for _, s := range []int64{1, 2, 3, 4, 6, 9, 2, 3, 6, 10, 11} {
		if err := d.Feed(s); err != nil {
			t.Fatal(err)
		}
	}
	if d.Watermark() != 11 || len(d.Gaps()) != 3 || d.Gaps()[0] != 5 ||
		d.Gaps()[1] != 7 || d.Gaps()[2] != 8 {
		t.Fatalf("walk end: h=%d gaps=%v", d.Watermark(), d.Gaps())
	}
	// Three failure modes: distinguishable, stateless, non-fatal.
	h0, g0 := d.Watermark(), len(d.Gaps())
	cases := []struct {
		seq int64
		err error
	}{
		{0, ErrInvalidSeq}, {-7, ErrInvalidSeq}, {math.MaxInt64, ErrSeqOverflow},
	}
	for _, c := range cases {
		if err := d.Feed(c.seq); !errors.Is(err, c.err) {
			t.Fatalf("Feed(%d) err=%v want %v", c.seq, err, c.err)
		}
		if d.Watermark() != h0 || len(d.Gaps()) != g0 {
			t.Fatalf("rejected Feed(%d) left a trace", c.seq)
		}
	}
	if errors.Is(ErrInvalidSeq, ErrSeqOverflow) || errors.Is(ErrInvalidSeq, ErrInvalidWindow) ||
		errors.Is(ErrSeqOverflow, ErrInvalidWindow) {
		t.Fatal("the three sentinel errors must be pairwise distinct")
	}
	// Detector stays usable after rejections.
	if err := d.Feed(12); err != nil || d.Watermark() != 12 {
		t.Fatalf("detector unusable after rejects: %v h=%d", err, d.Watermark())
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentFeed: N goroutines feed a shuffled 1..N; readers
// observe only monotonic watermarks; final state is H=N with no gaps.
// The window is set to N so arbitrary arrival order can never judge a
// still-pending number a gap (maxSeen-x >= N has no solution for x>=1).
func TestConcurrentFeed(t *testing.T) {
	cases := []struct {
		n    int
		seed int64
	}{{1, 1}, {7, 2}, {64, 3}, {257, 4}, {1000, 5}}
	for _, tc := range cases {
		d, _ := New(int64(tc.n))
		order := rand.New(rand.NewSource(tc.seed)).Perm(tc.n)
		stop := make(chan struct{})
		var rwg sync.WaitGroup
		for r := 0; r < 3; r++ {
			rwg.Add(1)
			go func() {
				defer rwg.Done()
				prev := int64(-1)
				for {
					select {
					case <-stop:
						return
					default:
					}
					v := d.Watermark()
					if v < prev {
						t.Errorf("watermark went backwards: %d -> %d", prev, v)
					}
					prev = v
				}
			}()
		}
		go func() { _ = d.SelfCheck() }() // concurrently callable, stateless
		var wg sync.WaitGroup
		errs := make(chan error, tc.n)
		for _, i := range order {
			wg.Add(1)
			go func(s int64) { defer wg.Done(); errs <- d.Feed(s) }(int64(i) + 1)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		close(stop)
		rwg.Wait()
		if d.Watermark() != int64(tc.n) || len(d.Gaps()) != 0 {
			t.Fatalf("n=%d: h=%d gaps=%v", tc.n, d.Watermark(), d.Gaps())
		}
	}
}
