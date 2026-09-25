package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestSelfCheck exercises the package-level self-test of all four invariants,
// the O(1) probe verdict and the concurrent non-crossing guarantee.
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestPublicEightSteps pins the canonical sequence through the public API.
func TestPublicEightSteps(t *testing.T) {
	a := New()
	st := []struct {
		k         byte
		src       string
		v, r1, r2 int64
		e         error
	}{
		{'s', "A", 1000, 0, 1001, nil},
		{'s', "A", 1000, 0, 1001, nil},
		{'s', "B", 2000, 1, 2001, nil},
		{'a', "A", 1, 1000, 0, nil},
		{'a', "A", 1, 0, 0, nil},
		{'s', "C", 3000, 2, 3001, nil},
		{'s', "C", 9999, 3, 10000, nil},
		{'a', "D", 999, 0, 0, ErrHalfOpen},
	}
	for i, s := range st {
		var x, y int64
		var err error
		if s.k == 's' {
			x, y, err = a.RecvSYN(s.src, s.v)
		} else {
			x, y, err = a.RecvACK(s.src, s.v)
		}
		if !errors.Is(err, s.e) || x != s.r1 || y != s.r2 {
			t.Fatalf("step %d: got (%d,%d,%v) want (%d,%d,%v)", i+1, x, y, err, s.r1, s.r2, s.e)
		}
	}
	if !a.Established("A") {
		t.Fatal("A must be established after step 4 and stay so after step 5")
	}
}

// TestRandomInterleaveAgainstNaiveModel diffs the public API against a
// loop-generated model over random SYN/retransmit/replacement/ACK sequences.
func TestRandomInterleaveAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	srcs := []string{"a", "b", "c", "d", "e"}
	for iter := 0; iter < 80; iter++ {
		a := New()
		rHalf := map[string][2]int64{}
		rEst := map[string][2]int64{}
		var rNext int64
		for n := 0; n < 40; n++ {
			src := srcs[rng.Intn(len(srcs))]
			if rng.Intn(2) == 0 {
				seq := int64(rng.Intn(7)) - 1 // -1..5: covers ErrBadSeq, dup and replace
				g1, g2, ge := a.RecvSYN(src, seq)
				var re error
				c, dup := rHalf[src]
				if seq < 0 {
					re = ErrBadSeq
				} else if !dup || c[0] != seq {
					c = [2]int64{seq, rNext}
					rNext++
					rHalf[src] = c
				}
				if !errors.Is(ge, re) || ge == nil && (g1 != c[1] || g2 != c[0]+1) {
					t.Fatalf("iter %d syn(%s,%d): (%d,%d,%v)", iter, src, seq, g1, g2, ge)
				}
			} else {
				ack := int64(rng.Intn(7))
				g1, g2, ge := a.RecvACK(src, ack)
				var r1, r2 int64
				var re error
				if c, ok := rHalf[src]; ok {
					if ack != c[1]+1 {
						re = ErrBadAck
					} else {
						delete(rHalf, src)
						rEst[src] = c
						r1, r2 = c[0], c[1]
					}
				} else if _, ok := rEst[src]; !ok {
					re = ErrHalfOpen
				}
				if g1 != r1 || g2 != r2 || !errors.Is(ge, re) {
					t.Fatalf("iter %d ack(%s,%d): (%d,%d,%v) vs (%d,%d,%v)", iter, src, ack, g1, g2, ge, r1, r2, re)
				}
			}
		}
		for _, s := range srcs {
			_, want := rEst[s]
			if a.Established(s) != want {
				t.Fatalf("iter %d established(%s)=%v want %v", iter, s, a.Established(s), want)
			}
		}
		if p, _, _ := a.RecvSYN("zz", 0); p != rNext {
			t.Fatalf("iter %d nextISN %d vs %d", iter, p, rNext)
		}
	}
}

// TestConcurrentHandshakesNoCrossing: N goroutines handshake on distinct srcs;
// every pair must match and all serverISNs must be pairwise distinct.
func TestConcurrentHandshakesNoCrossing(t *testing.T) {
	const N = 128
	a := New()
	isns := make([]int64, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			src := fmt.Sprintf("g%03d", g)
			isn, _, err := a.RecvSYN(src, int64(1000+g))
			if err != nil {
				t.Errorf("syn %d: %v", g, err)
				return
			}
			isns[g] = isn
			if c, s, err := a.RecvACK(src, isn+1); err != nil || c != int64(1000+g) || s != isn {
				t.Errorf("ack %d: (%d,%d,%v)", g, c, s, err)
			}
		}(g)
	}
	wg.Wait()
	seen := map[int64]bool{}
	for g := 0; g < N; g++ {
		if !a.Established(fmt.Sprintf("g%03d", g)) || seen[isns[g]] {
			t.Fatalf("g%03d not established or serverISN %d reused", g, isns[g])
		}
		seen[isns[g]] = true
	}
}
