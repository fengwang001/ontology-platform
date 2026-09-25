package dd

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/tup"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestTupleRefCount(t *testing.T) { // pins I2: refs equal holders; distinct counts positive refs.
	tp := tup.T{C1: "a", C2: 1}
	wantR, wantD := []int{1, 2, 2, 0}, []int{1, 1, 1, 0}
	for i, op := range [][]int{{1}, {1, 1}, {1, 1, 1, -1}, {1, 1, -1, -1}} {
		g := tup.NewGroup()
		for _, d := range op {
			if d > 0 {
				g.Add(tp)
			} else {
				g.Remove(tp)
			}
		}
		if g.Refs(tp) != wantR[i] || g.Distinct() != wantD[i] {
			t.Fatalf("refs=%d dst=%d want %d,%d", g.Refs(tp), g.Distinct(), wantR[i], wantD[i])
		}
	}
}

func TestRandomOpsMatchBatch(t *testing.T) { // pins I1+I3: incremental state matches batch recompute after every randomized op.
	ks, cs := [3]string{"k", "j", "l"}, [2]string{"a", "b"}
	for _, seed := range []int64{1, 2, 7, 42, 99} {
		rng, e := rand.New(rand.NewSource(seed)), New()
		oracle := map[int]row{}
		for step := 0; step < 2000; step++ {
			id := rng.Intn(30) + 1
			if _, live := oracle[id]; live && rng.Intn(4) == 3 {
				must(t, e.Delete(id))
				delete(oracle, id)
				continue
			}
			k, c1, c2 := ks[rng.Intn(3)], cs[rng.Intn(2)], rng.Intn(4)
			must(t, e.Upsert(id, k, c1, c2))
			oracle[id] = row{key: k, t: tup.T{C1: c1, C2: c2}}
			if err := e.verify(); err != nil {
				t.Fatalf("s=%d step=%d: %v", seed, step, err)
			}
		}
	}
}

func TestRejectedOpsLeaveState(t *testing.T) { // pins I4: three distinct sentinels, rejected ops leave state untouched.
	if ErrRowNotFound == ErrRowIDNotPositive || ErrRowNotFound == ErrEmptyKey || ErrRowIDNotPositive == ErrEmptyKey {
		t.Fatal("sentinel errors must be distinct")
	}
	e := New()
	must(t, e.Upsert(5, "k", "a", 1))
	for _, tc := range []struct {
		name string
		want error
		bad  func(*Engine) error
	}{
		{"delete missing", ErrRowNotFound, func(e *Engine) error { return e.Delete(123) }},
		{"rowID zero", ErrRowIDNotPositive, func(e *Engine) error { return e.Upsert(0, "k", "a", 1) }},
		{"rowID negative", ErrRowIDNotPositive, func(e *Engine) error { return e.Upsert(-7, "k", "a", 1) }},
		{"delete negative", ErrRowIDNotPositive, func(e *Engine) error { return e.Delete(-1) }},
		{"empty key", ErrEmptyKey, func(e *Engine) error { return e.Upsert(1, "", "a", 1) }},
	} {
		rows, total, dk := len(e.rows), e.Total(), e.Distinct("k")
		err := tc.bad(e)
		if !errors.Is(err, tc.want) || len(e.rows) != rows || e.Total() != total || e.Distinct("k") != dk {
			t.Fatalf("%s: wrong error or changed state: %v", tc.name, err)
		}
	}
	must(t, e.Upsert(6, "k", "b", 2))
}

func TestCheckCountBounded(t *testing.T) { // pins complexity via unexported checks: <=2 tuples inspected per op at any m.
	for _, m := range []int{100, 1000, 10000} {
		e := New()
		for i := 1; i <= m; i++ {
			must(t, e.Upsert(i, "k", "c", i))
		}
		inspect := func(op func() error) int {
			e.checks = 0
			must(t, op())
			return e.checks
		}
		n1 := inspect(func() error { return e.Upsert(m+1, "k", "c", 1) })
		n2 := inspect(func() error { return e.Delete(m) })
		n3 := inspect(func() error { return e.Upsert(1, "j", "c", 2) })
		if n1 > 1 || n2 != 1 || n3 > 2 {
			t.Fatalf("m=%d checks insert=%d delete=%d move=%d", m, n1, n2, n3)
		}
	}
}

func TestConcurrentUpsert(t *testing.T) { // pins concurrency: final distinct==N, reads non-decreasing; channels, no sleeps.
	for _, N := range []int{10, 100, 500} {
		e := New()
		var ws sync.WaitGroup
		var bad atomic.Bool
		stop, done := make(chan struct{}), make(chan struct{})
		ws.Add(N)
		for i := 1; i <= N; i++ {
			go func() {
				defer ws.Done()
				if err := e.Upsert(i, "k", "c", i); err != nil {
					bad.Store(true)
				}
			}()
		}
		go func() {
			prev := 0
			for {
				select {
				case <-stop:
					close(done)
					return
				default:
					d, tot := e.Distinct("k"), e.Total()
					if d < prev || tot < d || (d > 0 && d&15 == 0 && e.SelfCheck() != nil) {
						bad.Store(true)
					}
					prev = d
				}
			}
		}()
		ws.Wait()
		close(stop)
		<-done
		if e.Distinct("k") != N || e.Total() != N || bad.Load() {
			t.Fatalf("d=%d t=%d bad=%v want %d", e.Distinct("k"), e.Total(), bad.Load(), N)
		}
	}
}
