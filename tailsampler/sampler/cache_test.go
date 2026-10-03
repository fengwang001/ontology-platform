package sampler

import (
	"errors"
	"fmt"
	"testing"
)

// naive is a step-by-step reference model using only linear scans and map
// iteration. The randomized test feeds identical op sequences to the real
// Sampler and to naive and requires identical decisions at every step.
type naiveEntry struct {
	lastSeen uint64
	ids      map[string]struct{}
	seenErr  bool
	maxDur   uint64
}

type naiveCacheEnt struct {
	keep      bool
	decidedAt uint64
}

type naive struct {
	p       Params
	maxNow  uint64
	hasNow  bool
	buf     map[string]*naiveEntry
	cache   map[string]naiveCacheEnt
	lastWin uint64
	hasWin  bool
	used    uint64
}

func newNaive(p Params) *naive {
	return &naive{p: p, buf: map[string]*naiveEntry{}, cache: map[string]naiveCacheEnt{}}
}

// minKey returns the map key whose value is smallest under less.
func minKey[V any](m map[string]V, less func(a string, av V, b string, bv V) bool) string {
	best, first := "", true
	var bv V
	for k, v := range m {
		if first || less(k, v, best, bv) {
			best, bv, first = k, v, false
		}
	}
	return best
}

func (n *naive) cacheEnabled() bool { return n.p.Td > 0 && n.p.Cmax > 0 }

func TestCacheExpiryExact(t *testing.T) {
	p := baseParams()
	p.P = 0
	s := mustNew(t, p)
	mustIngest(t, s, 0, "t1", "a", 1, false)
	mustTick(t, s, 10) // t1 decided at 10, Td=50 -> alive while now < 60
	mustIngest(t, s, 59, "t1", "b", 1, false)
	if got := s.LateDropped(); got != 1 {
		t.Fatalf("LateDropped = %d, want 1 (10+50 > 59)", got)
	}
	mustIngest(t, s, 60, "t1", "c", 1, false) // 10+50 <= 60: expired
	if got := s.LateDropped(); got != 1 {
		t.Fatalf("LateDropped = %d, want 1 (expired at exactly Td)", got)
	}
	if len(s.buf) != 1 || s.buf["t1"].entry.Count() != 1 {
		t.Fatalf("t1 should be a fresh buffered trace, buf=%v", s.buf)
	}
}

func TestCacheCapacity(t *testing.T) {
	p := baseParams()
	p.P, p.Cmax, p.Td, p.W, p.Nmax = 0, 2, 1e9, 5, 10
	s := mustNew(t, p)
	mustIngest(t, s, 0, "t1", "a", 1, false)
	mustIngest(t, s, 1, "t2", "a", 1, false)
	mustIngest(t, s, 2, "t3", "a", 1, false)
	mustTick(t, s, 5) // t1 decided at 5
	mustTick(t, s, 6) // t2 decided at 6
	mustTick(t, s, 7) // t3 decided at 7, evicts t1 (smallest decidedAt)
	mustIngest(t, s, 10, "t1", "b", 1, false)
	if got := s.LateDropped(); got != 0 {
		t.Fatalf("LateDropped = %d, want 0: t1 was evicted from cache", got)
	}
	mustIngest(t, s, 11, "t2", "b", 1, false)
	mustIngest(t, s, 12, "t3", "b", 1, false)
	if got := s.LateDropped(); got != 2 {
		t.Fatalf("LateDropped = %d, want 2: t2,t3 still cached", got)
	}
}

func TestCacheCapacityTie(t *testing.T) {
	p := baseParams()
	p.P, p.Cmax, p.Td, p.W, p.Nmax = 0, 1, 1e9, 5, 10
	s := mustNew(t, p)
	mustIngest(t, s, 0, "a", "s1", 1, false)
	mustIngest(t, s, 0, "b", "s1", 1, false)
	mustTick(t, s, 5) // both decided at 5; tie -> smaller traceID "a" evicted
	mustIngest(t, s, 10, "a", "s2", 1, false)
	if got := s.LateDropped(); got != 0 {
		t.Fatalf("LateDropped = %d, want 0: a evicted on tie", got)
	}
	mustIngest(t, s, 11, "b", "s2", 1, false)
	if got := s.LateDropped(); got != 1 {
		t.Fatalf("LateDropped = %d, want 1: b still cached", got)
	}
}

func TestNoCache(t *testing.T) {
	for _, p := range []Params{
		{W: 5, Sc: 3, Nmax: 4, Td: 0, Cmax: 4, L: 100, P: 0, Wb: 10, Q: 1},
		{W: 5, Sc: 3, Nmax: 4, Td: 50, Cmax: 0, L: 100, P: 0, Wb: 10, Q: 1},
	} {
		s := mustNew(t, p)
		mustIngest(t, s, 0, "t1", "a", 1, false)
		mustTick(t, s, 5)
		mustIngest(t, s, 6, "t1", "b", 1, false) // no cache: new trace
		if s.LateDropped() != 0 || s.LateKept() != 0 {
			t.Fatalf("cache disabled but late counters moved: %+v", p)
		}
		if len(s.buf) != 1 {
			t.Fatalf("late span should buffer as new trace: %+v", p)
		}
	}
}

func TestDuplicateRejected(t *testing.T) {
	p := baseParams()
	p.P = 0
	s := mustNew(t, p)
	mustIngest(t, s, 5, "t1", "s1", 30, false)
	if _, err := s.Ingest(7, "t1", "s1", 40, false); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	// Rejected op must not move the clock: now=6 is still acceptable.
	mustIngest(t, s, 6, "t2", "s1", 1, false)
	// Rejected op must not move lastSeen: t1 still silences at 5+10=15.
	d := mustTick(t, s, 15)
	if len(d) != 1 || d[0].TraceID != "t1" || d[0].Spans != 1 {
		t.Fatalf("Tick(15) = %v, want t1 with 1 span (lastSeen untouched)", d)
	}
}

func TestTickExamined(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("buffer_%d", n), func(t *testing.T) {
			p := baseParams()
			p.Nmax, p.Sc, p.W, p.P = 1e5, 2, 10, 0
			s := mustNew(t, p)
			for i := 0; i < n; i++ {
				mustIngest(t, s, 0, fmt.Sprintf("t%05d", i), "s1", 1, false)
			}
			before := s.tickExamined
			d := mustTick(t, s, 9) // nothing silent yet
			if got := s.tickExamined - before; got > uint64(len(d))+1 {
				t.Fatalf("examined %d > decisions %d + 1", got, len(d))
			}
			before = s.tickExamined
			d = mustTick(t, s, 10) // all n decided
			if len(d) != n {
				t.Fatalf("decisions = %d, want %d", len(d), n)
			}
			if got := s.tickExamined - before; got > uint64(len(d))+1 {
				t.Fatalf("examined %d > decisions %d + 1", got, len(d))
			}
			t.Logf("n=%d: examined=%d decisions=%d", n, s.tickExamined, len(d))
		})
	}
}
