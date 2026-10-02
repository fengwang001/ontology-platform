package gdsfcache

import (
	"errors"
	"fmt"
	"math/big"
	"math/bits"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// naiveCache is a reference implementation that finds the eviction victim
// by linearly scanning every entry. It shares no code with the heap-based
// Cache, so the differential test below cross-checks two independent
// implementations of the same spec.
type naiveCache struct {
	cap     int64
	used    int64
	l       *big.Rat
	tick    int64
	entries map[string]*naiveEntry
}

type naiveEntry struct {
	size int64
	cost int64
	freq int64
	h    *big.Rat
	last int64
}

func newNaive(capacity int64) *naiveCache {
	return &naiveCache{cap: capacity, l: new(big.Rat), entries: make(map[string]*naiveEntry)}
}

func naivePriority(l *big.Rat, freq, cost, size int64) *big.Rat {
	num := new(big.Int).Mul(big.NewInt(freq), big.NewInt(cost))
	h := new(big.Rat).SetFrac(num, big.NewInt(size))
	return h.Add(h, l)
}

func (n *naiveCache) put(key string, size, cost int64) ([]string, error) {
	switch {
	case key == "":
		return nil, ErrEmptyKey
	case size <= 0:
		return nil, ErrNonPositiveSize
	case cost < 1:
		return nil, ErrInvalidCost
	case size > n.cap:
		return nil, ErrSizeExceedsCapacity
	}
	if old, ok := n.entries[key]; ok {
		n.used -= old.size
		delete(n.entries, key)
	}
	var evicted []string
	for n.used+size > n.cap {
		victim := n.victim()
		n.l = new(big.Rat).Set(n.entries[victim].h)
		n.used -= n.entries[victim].size
		delete(n.entries, victim)
		evicted = append(evicted, victim)
	}
	n.entries[key] = &naiveEntry{
		size: size,
		cost: cost,
		freq: 1,
		h:    naivePriority(n.l, 1, cost, size),
		last: n.tick,
	}
	n.tick++
	n.used += size
	return evicted, nil
}

func (n *naiveCache) get(key string) (bool, error) {
	if key == "" {
		return false, ErrEmptyKey
	}
	e, ok := n.entries[key]
	if !ok {
		return false, nil
	}
	e.freq++
	e.h = naivePriority(n.l, e.freq, e.cost, e.size)
	e.last = n.tick
	n.tick++
	return true, nil
}

func (n *naiveCache) peek(key string) (Info, error) {
	if key == "" {
		return Info{}, ErrEmptyKey
	}
	e, ok := n.entries[key]
	if !ok {
		return Info{}, ErrNotFound
	}
	return Info{Freq: e.freq, H: new(big.Rat).Set(e.h), Last: e.last}, nil
}

func (n *naiveCache) victim() string {
	best := ""
	for key, e := range n.entries {
		if best == "" {
			best = key
			continue
		}
		b := n.entries[best]
		if c := e.h.Cmp(b.h); c < 0 || (c == 0 && e.last < b.last) {
			best = key
		}
	}
	return best
}

// TestDifferentialAgainstNaive replays 2000 random operation sequences
// against both the heap-based Cache and the linear-scan naive model,
// requiring identical results (errors, evictions, hits, peeks, Used, L)
// after every single operation.
func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261002))
	for seq := 0; seq < sequences; seq++ {
		seed := rng.Int63()
		runSequence(t, seq, seed)
	}
}

func runSequence(t *testing.T, seq int, seed int64) {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	capacity := 1 + r.Int63n(24)
	realCache, err := New(capacity)
	if err != nil {
		t.Fatalf("seq %d: New: %v", seq, err)
	}
	model := newNaive(capacity)
	ops := 40 + r.Intn(160)
	var log []string
	evictions := 0

	fail := func(format string, args ...any) {
		t.Helper()
		t.Logf("seq=%d seed=%d cap=%d input ops:", seq, seed, capacity)
		for i, line := range log {
			t.Logf("  op[%d] %s", i, line)
		}
		t.Fatalf("seq=%d: %s", seq, fmt.Sprintf(format, args...))
	}

	for i := 0; i < ops; i++ {
		key := fmt.Sprintf("k%d", r.Intn(16))
		switch r.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9: // peek
			if r.Intn(20) == 0 {
				key = "" // exercise rejection
			}
			log = append(log, fmt.Sprintf("Peek(%q)", key))
			gotInfo, gotErr := realCache.Peek(key)
			wantInfo, wantErr := model.peek(key)
			if !errors.Is(gotErr, wantErr) {
				fail("Peek(%q): err=%v, model=%v", key, gotErr, wantErr)
			}
			if gotErr == nil && (gotInfo.Freq != wantInfo.Freq ||
				gotInfo.Last != wantInfo.Last || gotInfo.H.Cmp(wantInfo.H) != 0) {
				fail("Peek(%q): got %+v, model %+v", key, gotInfo, wantInfo)
			}
		case 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24: // get
			if r.Intn(25) == 0 {
				key = ""
			}
			log = append(log, fmt.Sprintf("Get(%q)", key))
			gotHit, gotErr := realCache.Get(key)
			wantHit, wantErr := model.get(key)
			if !errors.Is(gotErr, wantErr) || gotHit != wantHit {
				fail("Get(%q): got (%v,%v), model (%v,%v)", key, gotHit, gotErr, wantHit, wantErr)
			}
		default: // put
			size := 1 + r.Int63n(capacity+2) // may exceed cap
			cost := 1 + r.Int63n(50)
			switch r.Intn(30) {
			case 0:
				key = ""
			case 1:
				size = 0
			case 2:
				cost = 0
			}
			log = append(log, fmt.Sprintf("Put(%q, %d, %d)", key, size, cost))
			gotEvicted, gotErr := realCache.Put(key, size, cost)
			wantEvicted, wantErr := model.put(key, size, cost)
			if !errors.Is(gotErr, wantErr) {
				fail("Put(%q,%d,%d): err=%v, model=%v", key, size, cost, gotErr, wantErr)
			}
			if !reflect.DeepEqual(gotEvicted, wantEvicted) {
				fail("Put(%q,%d,%d): evicted %v, model evicted %v",
					key, size, cost, gotEvicted, wantEvicted)
			}
			evictions += len(gotEvicted)
		}
		if got, want := realCache.Used(), model.used; got != want {
			fail("Used() = %d, model %d", got, want)
		}
		if got, want := realCache.L(), model.l; got.Cmp(want) != 0 {
			fail("L = %v, model %v", got, want)
		}
	}
	// Final full-state comparison: every surviving entry must match.
	for key, want := range model.entries {
		got, err := realCache.Peek(key)
		if err != nil {
			fail("final Peek(%q): %v", key, err)
		}
		if got.Freq != want.freq || got.Last != want.last || got.H.Cmp(want.h) != 0 {
			fail("final state of %q: got %+v, model %+v", key, got, want)
		}
	}
	if realCache.Len() != len(model.entries) {
		fail("Len = %d, model %d", realCache.Len(), len(model.entries))
	}
	t.Logf("seq=%d seed=%d cap=%d ops=%d evictions=%d finalL=%s used=%d/%d "+
		"verdict=MATCH basis: heap cache and linear-scan naive model produced identical "+
		"errors, eviction lists, hit flags, peeks, Used and L after every op",
		seq, seed, capacity, ops, evictions, realCache.L(), model.used, capacity)
}

// TestComparisonCountIsLogarithmic proves, with the unexported comparison
// counter, that a single Put (evicting exactly one entry), an overwriting
// Put, and a Get hit each cost at most 8*(floor(log2 n)+2) H comparisons.
func TestComparisonCountIsLogarithmic(t *testing.T) {
	for _, n := range []int64{1000, 50000} {
		c := mustCache(t, n)
		for i := int64(0); i < n; i++ {
			mustPut(t, c, fmt.Sprintf("k%d", i), 1, 1)
		}
		bound := 8 * (int64(bits.Len64(uint64(n))) - 1 + 2)

		c.cmps = 0
		evicted := mustPut(t, c, "fresh", 1, 1)
		if len(evicted) != 1 {
			t.Fatalf("n=%d: new-key Put evicted %d entries, want exactly 1", n, len(evicted))
		}
		if evicted[0] != "k0" {
			t.Fatalf("n=%d: new-key Put evicted %q, want k0 (tied H, smallest last)", n, evicted[0])
		}
		if c.cmps > bound {
			t.Fatalf("n=%d: new-key Put used %d H comparisons, bound %d", n, c.cmps, bound)
		}
		t.Logf("n=%d new-key Put (1 eviction): %d H comparisons (bound %d)", n, c.cmps, bound)

		c.cmps = 0
		mustPut(t, c, "k1", 1, 1)
		if c.cmps > bound {
			t.Fatalf("n=%d: overwrite Put used %d H comparisons, bound %d", n, c.cmps, bound)
		}
		t.Logf("n=%d overwrite Put: %d H comparisons (bound %d)", n, c.cmps, bound)

		c.cmps = 0
		if !mustGet(t, c, "k2") {
			t.Fatalf("n=%d: Get(k2) missed", n)
		}
		if c.cmps > bound {
			t.Fatalf("n=%d: Get hit used %d H comparisons, bound %d", n, c.cmps, bound)
		}
		t.Logf("n=%d Get hit: %d H comparisons (bound %d)", n, c.cmps, bound)
	}
}

// TestConcurrentUse hammers the cache from many goroutines. Run with
// -race; the byte-capacity invariant must hold throughout.
func TestConcurrentUse(t *testing.T) {
	const capacity = 256
	c := mustCache(t, capacity)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g) + 1))
			for i := 0; i < 2000; i++ {
				key := fmt.Sprintf("k%d", r.Intn(64))
				switch r.Intn(4) {
				case 0:
					if _, err := c.Put(key, 1+int64(r.Intn(8)), 1+int64(r.Intn(10))); err != nil {
						t.Errorf("Put: %v", err)
						return
					}
				case 1:
					if _, err := c.Get(key); err != nil {
						t.Errorf("Get: %v", err)
						return
					}
				case 2:
					if _, err := c.Peek(key); err != nil && !errors.Is(err, ErrNotFound) {
						t.Errorf("Peek: %v", err)
						return
					}
				default:
					if used := c.Used(); used > capacity {
						t.Errorf("Used = %d exceeds capacity %d", used, capacity)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	if used := c.Used(); used > capacity {
		t.Fatalf("Used = %d exceeds capacity %d after concurrent run", used, capacity)
	}
}
