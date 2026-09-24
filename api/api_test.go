package api_test

import (
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/idx"
)

var configs = []struct {
	base, interval, seed int64
	n                    int
}{{0, 1, 1, 50}, {1000, 100, 2, 200}, {7, 33, 3, 1000}}

func build(t *testing.T, base, interval, seed int64, n int) (*api.Log, []int64, []int64, []int64) {
	t.Helper()
	l, err := api.New(base, interval)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(seed))
	var offs, poss, sizes []int64
	for i := 0; i < n; i++ {
		off := base + int64(rng.Intn(3))
		if i > 0 {
			off = offs[i-1] + 1 + int64(rng.Intn(3))
		}
		sz := int64(rng.Intn(9) + 1)
		p, err := l.Append(off, sz)
		if err != nil {
			t.Fatal(err)
		}
		offs, poss, sizes = append(offs, off), append(poss, p), append(sizes, sz)
	}
	return l, offs, poss, sizes
}

func naiveAt(offs, poss []int64, tgt int64) (int64, int64, error) {
	for i := range offs {
		if offs[i] >= tgt {
			return offs[i], poss[i], nil
		}
	}
	return 0, 0, api.ErrNotFound
}

func TestLookupMatchesNaive(t *testing.T) {
	for _, c := range configs {
		l, offs, poss, _ := build(t, c.base, c.interval, c.seed, c.n)
		for tgt := c.base; tgt <= offs[len(offs)-1]+1; tgt++ {
			o, p, _, err := l.Lookup(tgt)
			no, np, nerr := naiveAt(offs, poss, tgt)
			if (err == nil) != (nerr == nil) || (err == nil && (o != no || p != np)) {
				t.Fatalf("cfg %+v target %d: got (%d,%d,%v) want (%d,%d,%v)", c, tgt, o, p, err, no, np, nerr)
			}
		}
	}
}

func TestIndexWellFormed(t *testing.T) {
	for _, c := range configs {
		l, offs, poss, _ := build(t, c.base, c.interval, c.seed, c.n)
		es := l.Entries()
		for i, e := range es {
			if i > 0 && (e.Rel <= es[i-1].Rel || e.Pos <= es[i-1].Pos) {
				t.Fatalf("cfg %+v: entries not increasing at %d", c, i)
			}
			if j := slices.Index(poss, e.Pos); j < 0 || offs[j]-c.base != int64(e.Rel) {
				t.Fatalf("cfg %+v: entry %v matches no record", c, e)
			}
		}
	}
}

func TestIndexMatchesRecompute(t *testing.T) {
	for _, c := range configs {
		l, offs, _, sizes := build(t, c.base, c.interval, c.seed, c.n)
		var want []idx.Entry
		var accum, bytes int64
		for i := range offs {
			if accum >= c.interval {
				want = append(want, idx.Entry{Rel: int32(offs[i] - c.base), Pos: bytes})
				accum = 0
			}
			accum += sizes[i]
			bytes += sizes[i]
		}
		if fmt.Sprint(want) != fmt.Sprint(l.Entries()) {
			t.Fatalf("cfg %+v: index != recomputed", c)
		}
	}
}

func TestConcurrentLookup(t *testing.T) {
	const n = 2000
	l, _ := api.New(0, 25)
	poss := make([]int64, n)
	var count atomic.Int64
	var bad atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			p, err := l.Append(int64(i), 10)
			if err != nil {
				bad.Store(true)
				return
			}
			poss[i] = p
			count.Store(int64(i + 1))
		}
	}()
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for j := 0; j < 3000; j++ {
				if c := count.Load(); c > 0 {
					i := rng.Int63n(c)
					if o, p, _, err := l.Lookup(i); err != nil || o != i || p != poss[i] {
						bad.Store(true)
					}
				}
			}
		}(int64(r))
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent lookup mismatch")
	}
	// 结束后索引仍满足不变量 2、3（size 恒为 10、interval 25 → 每 3 条一个条目）。
	es := l.Entries()
	for i, e := range es {
		if (i > 0 && (e.Rel <= es[i-1].Rel || e.Pos <= es[i-1].Pos)) ||
			int64(e.Rel) != e.Pos/10 || e.Pos%10 != 0 {
			t.Fatalf("index ill-formed at %d", i)
		}
	}
	if want := (n - 1) / 3; len(es) != want {
		t.Fatalf("index count %d != recomputed %d", len(es), want)
	}
}
