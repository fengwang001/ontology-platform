package api_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/lim"
)

func TestNewRejectsInvalidConfig(t *testing.T) {
	for i, c := range [][2]int64{{0, 1}, {-1, 1}, {1, 0}, {1, -5}, {0, 0}} {
		if l, err := api.New(c[0], c[1]); !errors.Is(err, api.ErrInvalidConfig) || l != nil {
			t.Fatalf("case %d (%v): got (%v,%v), want ErrInvalidConfig", i, c, l, err)
		}
	}
	if _, err := api.New(1, 1); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

// TestAllowEightSteps is the worked eight-step example (capacity=20, rate=2).
func TestAllowEightSteps(t *testing.T) {
	steps := []struct {
		t, need, wantTok int64
		wantAllow        bool
	}{
		{0, 15, 5, true}, {0, 8, 5, false}, {3, 10, 1, true}, {3, 5, 1, false},
		{8, 12, 11, false}, {9, 6, 7, true}, {20, 18, 2, true}, {20, 3, 2, false},
	}
	l, _ := api.New(20, 2)
	for i, s := range steps {
		got, err := l.Allow(s.t, s.need)
		if err != nil || got != s.wantAllow || l.Tokens() != s.wantTok {
			t.Fatalf("step %d: got=(%v,%v) tokens=%d, want allow=%v tokens=%d",
				i, got, err, l.Tokens(), s.wantAllow, s.wantTok)
		}
	}
}

// TestNaiveReferenceEquivalence pins every decision/token count to the
// naive reference across generated monotone sequences (invariant 1).
func TestNaiveReferenceEquivalence(t *testing.T) {
	for _, tc := range [][2]int64{{1, 1}, {5, 2}, {20, 2}, {37, 3}, {100, 7}} {
		cap, rate := tc[0], tc[1]
		l, _ := api.New(cap, rate)
		ref, prevT, now, seed := cap, int64(0), int64(0), int64(1)
		for i := 0; i < 2000; i++ {
			seed = seed*1103515245 + 12345
			delta := (seed >> 16) % 5
			if delta < 0 {
				delta += 5
			}
			now += delta
			need := (seed>>8)%(cap+3) + 1
			if need < 1 {
				need += cap + 3
			}
			ref = min(cap, ref+(now-prevT)*rate)
			prevT = now
			want := ref >= need
			got, err := l.Allow(now, need)
			if err != nil || got != want {
				t.Fatalf("cap=%d i=%d: got=(%v,%v), want %v", cap, i, got, err, want)
			}
			if want {
				ref -= need
			}
			if l.Tokens() != ref {
				t.Fatalf("cap=%d i=%d: tokens=%d, naive ref=%d", cap, i, l.Tokens(), ref)
			}
		}
	}
}

// TestDeterminism replays one (t,need) sequence twice and requires identical results.
func TestDeterminism(t *testing.T) {
	build := func() ([]bool, []int64) {
		l, _ := api.New(23, 4)
		allows, toks := []bool{}, []int64{}
		for i, now := int64(0), int64(0); i < 500; i++ {
			now += (i*7 + 3) % 11
			a, _ := l.Allow(now, (i*13)%9+1) // valid monotone request, never errors
			allows, toks = append(allows, a), append(toks, l.Tokens())
		}
		return allows, toks
	}
	a1, t1 := build()
	a2, t2 := build()
	for i := range a1 {
		if a1[i] != a2[i] || t1[i] != t2[i] {
			t.Fatalf("step %d: replays differ (%v,%d) vs (%v,%d)", i, a1[i], t1[i], a2[i], t2[i])
		}
	}
}

// TestFailureLeavesNoTrace: bad requests change neither tokens nor last
// (invariant 4), the three sentinels are distinct, and use continues.
func TestFailureLeavesNoTrace(t *testing.T) {
	l, _ := api.New(20, 2)
	if _, err := l.Allow(5, 10); err != nil {
		t.Fatal(err)
	}
	want := l.Tokens() // 10
	cases := []struct {
		t, need int64
		wantErr error
	}{{6, 0, lim.ErrInvalidNeed}, {6, -3, lim.ErrInvalidNeed}, {4, 1, lim.ErrClockRollback}}
	for i, c := range cases {
		ok, err := l.Allow(c.t, c.need)
		if ok || !errors.Is(err, c.wantErr) || l.Tokens() != want {
			t.Fatalf("case %d: (%v,%v) tokens=%d, want err=%v tokens=%d", i, ok, err, l.Tokens(), c.wantErr, want)
		}
	}
	if errors.Is(api.ErrInvalidConfig, lim.ErrInvalidNeed) ||
		errors.Is(lim.ErrInvalidNeed, lim.ErrClockRollback) {
		t.Fatal("the three sentinel errors must be distinct")
	}
	if ok, err := l.Allow(5, 1); !ok || err != nil || l.Tokens() != want-1 {
		t.Fatalf("limiter unusable after failures: (%v,%v) tokens=%d", ok, err, l.Tokens())
	}
}

// TestConcurrentSameTimestamp: N goroutines share one timestamp; all admitted, N consumed (no sleeps).
func TestConcurrentSameTimestamp(t *testing.T) {
	const N = 64
	for _, cap := range []int64{N, N + 1, 2 * N} {
		l, _ := api.New(cap, 2)
		var admitted int64
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if a, err := l.Allow(10, 1); err == nil && a {
					atomic.AddInt64(&admitted, 1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if admitted != N || l.Tokens() != cap-N {
			t.Fatalf("cap=%d: admitted=%d tokens=%d, want %d and %d", cap, admitted, l.Tokens(), N, cap-N)
		}
	}
}
