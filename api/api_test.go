package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

// TestNaiveReference pins invariant 1: randomized interleavings of
// Open/Close/Send/Recv must match the naive oracle step for step.
func TestNaiveReference(t *testing.T) {
	if err := New(4).SelfCheck(); err != nil { // also nails SelfCheck
		t.Fatal(err)
	}
	for _, c := range []int{1, 3, 4, 7} {
		for seed := int64(0); seed < 25; seed++ {
			x, r, rd := New(c), newNaive(c), rand.New(rand.NewSource(seed))
			pool := []Handle{{ID: c, Gen: 1}} // entry 0 is always a bad id
			for n := 0; n < 1200; n++ {
				h := pool[rd.Intn(len(pool))]
				s := string(rune('a' + n%26))
				var ei, ee error
				switch rd.Intn(4) {
				case 0:
					hi, e1 := x.Open()
					hr, e2 := r.open1()
					ei, ee = e1, e2
					if ei == nil {
						if hi != hr {
							t.Fatalf("handle %v vs %v", hi, hr)
						}
						pool = append(pool, hi)
					}
				case 1:
					ei, ee = x.Close(h), r.close1(h)
				case 2:
					ei, ee = x.Send(h, nil), r.check(h.ID, h.Gen)
				default:
					ei, ee = x.Recv(h.ID, h.Gen, []byte(s)), r.recv(h.ID, h.Gen, s)
				}
				if ei != ee {
					t.Fatalf("c=%d seed=%d n=%d: %v vs %v", c, seed, n, ei, ee)
				}
				for i := 0; i < c; i++ {
					if x.Gen(i) != r.gen[i] || string(x.Data(i)) != r.buf[i] {
						t.Fatalf("state diverge at id %d", i)
					}
				}
			}
		}
	}
}

// TestGenerationIsolation pins invariant 2 across repeated slot reuse.
func TestGenerationIsolation(t *testing.T) {
	for reuse := 1; reuse <= 5; reuse++ {
		x := New(1)
		h, _ := x.Open()
		for i := 0; i < reuse; i++ {
			x.Close(h) // h is valid by construction
			h, _ = x.Open()
		}
		if h.Gen != 1+reuse {
			t.Fatalf("gen %d want %d", h.Gen, 1+reuse)
		}
		late := x.Recv(0, 1, []byte("late"))
		if !errors.Is(late, ErrStale) || len(x.Data(0)) != 0 {
			t.Fatalf("stale not isolated: %v %q", late, x.Data(0))
		}
		if err := x.Recv(0, h.Gen, []byte("new")); err != nil || string(x.Data(0)) != "new" {
			t.Fatalf("cross-stream: %v %q", err, x.Data(0))
		}
	}
}

// TestHalfOpen pins invariant 3: frames to FREE slots are half-open.
func TestHalfOpen(t *testing.T) {
	x := New(4)
	for _, id := range []int{0, 1, 2, 3} {
		if err := x.Recv(id, 1, nil); !errors.Is(err, ErrHalfOpen) {
			t.Fatalf("id %d: want half-open, got %v", id, err)
		}
	}
}

// TestFailureNoSideEffect pins invariant 4: all four sentinel rejections.
func TestFailureNoSideEffect(t *testing.T) {
	cases := []struct {
		name string
		want error
		run  func(*Demux, Handle) error
	}{
		{"noslots", ErrNoSlots, func(x *Demux, h Handle) error { x.Open(); _, e := x.Open(); return e }},
		{"badid", ErrBadID, func(x *Demux, h Handle) error { return x.Recv(4, 1, nil) }},
		{"halfopen", ErrHalfOpen, func(x *Demux, h Handle) error { return x.Send(Handle{ID: 1, Gen: 1}, nil) }},
		{"stale", ErrStale, func(x *Demux, h Handle) error { return x.Recv(0, h.Gen+1, nil) }},
	}
	for _, tc := range cases {
		x := New(2)
		h, _ := x.Open()
		if err := x.Recv(0, h.Gen, []byte("keep")); err != nil {
			t.Fatal(err)
		}
		g, d := x.Gen(0), string(x.Data(0))
		if err := tc.run(x, h); !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
		if x.Gen(0) != g || string(x.Data(0)) != d {
			t.Fatalf("%s left a trace", tc.name)
		}
		if err := x.Recv(0, h.Gen, []byte("!")); err != nil || string(x.Data(0)) != "keep!" {
			t.Fatalf("%s: unusable after reject", tc.name)
		}
	}
}

// TestConcurrentNoCrossTalk: pairwise-unique handles, no cross-stream data.
func TestConcurrentNoCrossTalk(t *testing.T) {
	const n = 128
	x := New(n)
	hs := make([]Handle, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := x.Open() // cannot fail: n slots, n goroutines
			if err != nil || x.Send(h, []byte{byte(i)}) != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			hs[i] = h
		}(i)
	}
	wg.Wait()
	seen := map[Handle]bool{}
	for i, h := range hs {
		if seen[h] {
			t.Fatalf("duplicate handle %v", h)
		}
		seen[h] = true
		if err := x.Recv(h.ID, h.Gen, []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
		if string(x.Data(h.ID)) != string([]byte{byte(i)}) {
			t.Fatalf("conn %d cross-talk: %q", i, x.Data(h.ID))
		}
	}
}
