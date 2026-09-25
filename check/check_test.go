package check_test

import (
	"errors"
	"maps"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/bank"
	"ontology/check"
	"ontology/vec"
)

func TestErrorsAndUnsafe(t *testing.T) {
	b := bank.New(vec.V{6, 6})
	for p, m := range []vec.V{{3, 3}, {3, 3}, {4, 4}} {
		b.Declare(p, m)
		b.Request(p, []vec.V{{2, 1}, {1, 2}, {1, 1}}[p])
	}
	avail, _, alloc := b.Snapshot()
	if ok, _ := vec.LE(vec.V{1, 1}, avail); !ok {
		t.Fatal("avail-only check should approve")
	}
	for p := 3; p < bank.MaxProcs; p++ {
		b.Declare(p, vec.V{1, 1})
	}
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"unsafe", func() error { return b.Request(2, vec.V{1, 1}) }, bank.ErrUnsafe},
		{"unknown", func() error { return b.Request(99, vec.V{1, 1}) }, bank.ErrUnknown},
		{"exceeds-claim", func() error { return b.Request(0, vec.V{3, 3}) }, bank.ErrExceedsClaim},
		{"over-release", func() error { return b.Release(0, vec.V{3, 3}) }, bank.ErrOverRelease},
		{"full", func() error { return b.Declare(bank.MaxProcs, vec.V{1, 1}) }, bank.ErrFull},
	}
	for _, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if a, _, al := b.Snapshot(); !slices.Equal(a, avail) || !slices.Equal(al[2], alloc[2]) {
		t.Fatal("rejected ops mutated state")
	}
}
func TestMatchesNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		b := bank.New(vec.V{9, 9, 9})
		for p := 0; p < 1+rng.Intn(6); p++ {
			b.Declare(p, vec.V{rng.Intn(5), rng.Intn(5), rng.Intn(5)})
			b.Request(p, vec.V{rng.Intn(3), rng.Intn(3), rng.Intn(3)})
		}
		avail, max, alloc := b.Snapshot()
		req := vec.V{rng.Intn(4), rng.Intn(4), rng.Intn(4)}
		tAlloc := maps.Clone(alloc)
		tAlloc[0], _ = vec.Add(alloc[0], req)
		tAvail, _ := vec.Sub(avail, req)
		need, _ := vec.Sub(max[0], alloc[0])
		if fits, _ := vec.LE(req, need); (fits && check.Safe(tAvail, max, tAlloc)) != (b.Request(0, req) == nil) {
			t.Fatalf("i=%d", i)
		}
	}
	big := bank.New(vec.V{50, 50, 50})
	for p := 0; p < 50; p++ {
		big.Declare(p, vec.V{1, 1, 1})
	}
	if err := big.Request(0, vec.V{1, 1, 1}); err != nil || big.Comparisons() < 50 || big.Comparisons() > 50*51/2 {
		t.Fatalf("err=%v cmp=%d", err, big.Comparisons())
	}
}
func TestConcurrentSafe(t *testing.T) {
	b := bank.New(vec.V{8, 8, 8})
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		b.Declare(g, vec.V{4, 4, 4})
		wg.Add(1)
		go func(pid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(pid)))
			for i := 0; i < 100; i++ {
				ok := b.Request(pid, vec.V{rng.Intn(3), rng.Intn(3), rng.Intn(3)}) == nil
				if rng.Intn(2) == 0 {
					ok = b.Release(pid, vec.V{rng.Intn(3), rng.Intn(3), rng.Intn(3)}) == nil || ok
				}
				if rng.Intn(8) == 0 {
					b.Finish(pid)
					ok = b.Declare(pid, vec.V{4, 4, 4}) == nil
				}
				if ok && !check.Consistent(b, vec.V{8, 8, 8}) {
					t.Error("invariant violated")
				}
			}
		}(g)
	}
	wg.Wait()
}
