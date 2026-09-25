package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/off"
)

// pmod logs a partition's commits and last evict time so recovery can be
// recomputed from scratch; one global clock keeps ts and now monotone.
type pmod struct {
	commits     [][2]int64
	cp, lastNow int64
	hasCP       bool
}

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

// TestRestartMatchesBatchRecomputation drives loop-generated random
// sequences and compares Restart against an independent batch recomputation.
func TestRestartMatchesBatchRecomputation(t *testing.T) {
	for _, cfg := range []struct{ seed, np int }{{1, 1}, {2, 4}, {3, 9}, {4, 9}} {
		v, _ := api.New(10)
		ms := make([]pmod, cfg.np)
		rng := rand.New(rand.NewSource(int64(cfg.seed)))
		var g int64
		for step := 0; step < 500; step++ {
			p := rng.Intn(cfg.np)
			m := &ms[p]
			switch rng.Intn(3) {
			case 0:
				g++
				must(t, v.Commit(p, g, g))
				m.commits = append(m.commits, [2]int64{g, g})
			case 1:
				if len(m.commits) > 0 {
					must(t, v.Checkpoint(p))
					m.cp, m.hasCP = m.commits[len(m.commits)-1][0], true
				}
			case 2:
				g += int64(rng.Intn(6))
				must(t, v.Evict(p, g))
				m.lastNow = g
			}
		}
		got := v.Restart()
		for p := range ms {
			m := &ms[p]
			want := off.NoCheckpoint
			if m.hasCP {
				want = m.cp
			}
			for _, c := range m.commits { // surviving: ts >= lastNow-retention
				if c[1] >= m.lastNow-10 && c[0] > want {
					want = c[0]
				}
			}
			if got[p] != want {
				t.Fatalf("seed=%d np=%d p=%d restart=%d batch=%d", cfg.seed, cfg.np, p, got[p], want)
			}
		}
	}
}

// TestRejectedOperationsLeaveNoTrace pins invariant 4: distinct sentinels,
// no state change, instance stays usable.
func TestRejectedOperationsLeaveNoTrace(t *testing.T) {
	v, _ := api.New(10)
	must(t, v.Commit(0, 100, 0))
	must(t, v.Evict(0, 20))
	cases := []struct {
		want error
		run  func() error
	}{
		{api.ErrInvalidRetention, func() error { _, e := api.New(0); return e }},
		{off.ErrCommitNotMonotonic, func() error { return v.Commit(0, 100, 5) }},
		{off.ErrCommitNotMonotonic, func() error { return v.Commit(0, 101, -1) }},
		{off.ErrNowRewound, func() error { return v.Evict(0, 19) }},
		{off.ErrNoCommit, func() error { return v.Checkpoint(7) }},
	}
	for _, c := range cases {
		if e := c.run(); !errors.Is(e, c.want) {
			t.Fatalf("want %v got %v", c.want, e)
		}
	}
	distinct := map[error]struct{}{api.ErrInvalidRetention: {}, off.ErrCommitNotMonotonic: {}, off.ErrNowRewound: {}, off.ErrNoCommit: {}}
	if len(distinct) != 4 {
		t.Fatal("sentinels must be distinct")
	}
	if c, ok := v.Committed(0); !ok || c != 100 {
		t.Fatalf("state changed after rejections: %d,%v", c, ok)
	}
	if _, p := v.Restart()[7]; p {
		t.Fatal("rejected Checkpoint created a partition entry")
	}
	if e := v.Commit(0, 130, 30); e != nil || v.Restart()[0] != 130 {
		t.Fatalf("instance unusable after rejection: %v", e)
	}
}

// TestConcurrentReaders: N read-only goroutines must agree per partition.
func TestConcurrentReaders(t *testing.T) {
	v, _ := api.New(10)
	for p := 0; p < 5; p++ {
		must(t, v.Commit(p, int64(100+p), 0))
		must(t, v.Commit(p, int64(200+p), 5))
	}
	want := v.Restart()
	var wg sync.WaitGroup
	var fail int32
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				ok := reflect.DeepEqual(v.Restart(), want)
				for p, w := range want {
					c, has := v.Committed(p)
					ok = ok && has && c == w
				}
				if !ok {
					atomic.AddInt32(&fail, 1)
					return
				}
			}
		}()
	}
	wg.Wait()
	if fail > 0 {
		t.Fatal("concurrent readers disagreed")
	}
}

// TestSelfCheck runs the built-in check of invariants 1, 2 and 3.
func TestSelfCheck(t *testing.T) {
	v, _ := api.New(10)
	must(t, v.SelfCheck())
}
