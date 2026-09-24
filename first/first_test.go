package first

import (
	"math/rand"
	"testing"

	"ontology/evt"
)

// naiveFirst scans every active occurrence, the oracle for invariant 1.
func naiveFirst(cnt map[evt.Event]int) (evt.Event, bool) {
	var mn evt.Event
	have := false
	for e, c := range cnt {
		if c > 0 && (!have || evt.Less(e, mn)) {
			mn, have = e, true
		}
	}
	return mn, have
}

func TestFirstMatchesNaiveRescan(t *testing.T) {
	cases := []struct {
		seed int64
		ops  int
	}{
		{1, 200}, {2, 500}, {3, 1000}, {42, 2000},
	}
	for _, tc := range cases {
		rng := rand.New(rand.NewSource(tc.seed))
		s := NewSet()
		ref := map[evt.Event]int{}
		for i := 0; i < tc.ops; i++ {
			e := evt.Event{Key: string(rune('a' + rng.Intn(5))), TS: int64(rng.Intn(21) - 10)}
			if rng.Intn(2) == 0 || ref[e] == 0 {
				s.Add(e)
				ref[e]++
			} else if !s.Remove(e) {
				t.Fatalf("seed %d step %d: Remove(%v) failed on live occurrence", tc.seed, i, e)
			} else {
				ref[e]--
			}
			got, ok1 := s.First()
			want, ok2 := naiveFirst(ref)
			if ok1 != ok2 || (ok1 && got != want) {
				t.Fatalf("seed %d step %d: First=%v,%v want %v,%v", tc.seed, i, got, ok1, want, ok2)
			}
			if s.Count() != lenCounts(ref) {
				t.Fatalf("seed %d step %d: Count drift", tc.seed, i)
			}
		}
	}
}

func lenCounts(cnt map[evt.Event]int) int {
	n := 0
	for _, c := range cnt {
		n += c
	}
	return n
}

func TestRemovePromotesNext(t *testing.T) {
	type op struct {
		add     bool
		k       string
		ts      int64
		wantKey string
		wantTS  int64
		wantOK  bool
	}
	steps := []op{
		{true, "a", 5, "a", 5, true},
		{true, "b", 5, "a", 5, true}, // TS tie -> lexicographic key
		{true, "a", 3, "a", 3, true},
		{false, "a", 3, "a", 5, true}, // first withdrawn -> promote a@5
		{false, "a", 5, "b", 5, true}, // promote b@5
		{false, "b", 5, "", 0, false}, // empty
	}
	s := NewSet()
	for i, st := range steps {
		e := evt.Event{Key: st.k, TS: st.ts}
		if st.add {
			s.Add(e)
		} else if !s.Remove(e) {
			t.Fatalf("step %d: Remove failed", i+1)
		}
		gk, gts, ok := func() (string, int64, bool) {
			g, o := s.First()
			return g.Key, g.TS, o
		}()
		if ok != st.wantOK || gk != st.wantKey || gts != st.wantTS {
			t.Fatalf("step %d: got (%s,%d,%v) want (%s,%d,%v)", i+1, gk, gts, ok, st.wantKey, st.wantTS, st.wantOK)
		}
	}
}

func TestMultisetSemantics(t *testing.T) {
	for _, k := range []int{1, 2, 5, 10} {
		s := NewSet()
		base := evt.Event{Key: "z", TS: 9}
		dup := evt.Event{Key: "a", TS: 1}
		s.Add(base)
		for i := 0; i < k; i++ {
			s.Add(dup)
		}
		if s.Count() != k+1 {
			t.Fatalf("k=%d: Count=%d want %d", k, s.Count(), k+1)
		}
		if g, ok := s.First(); !ok || g != dup {
			t.Fatalf("k=%d: First=%v,%v want a@1", k, g, ok)
		}
		for i := 0; i < k-1; i++ {
			if !s.Remove(dup) {
				t.Fatalf("k=%d: Remove %d failed", k, i)
			}
		}
		if g, ok := s.First(); !ok || g != dup {
			t.Fatalf("k=%d: last duplicate must survive", k)
		}
		if !s.Remove(dup) || !s.Remove(base) {
			t.Fatalf("k=%d: final removes failed", k)
		}
		if s.Count() != 0 {
			t.Fatalf("k=%d: K adds then K removes must restore prior state", k)
		}
	}
}

func TestHeapComparisonLogarithmic(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := NewSet()
		for i := 0; i < m; i++ {
			s.Add(evt.Event{Key: logKey(i), TS: int64(i)}) // distinct TS and identity
		}
		s.Add(evt.Event{Key: logKey(m), TS: -1}) // new minimum: full sift-up
		bound := 2*ceilLog2(m+1) + 2
		if s.lastCmp <= 0 || s.lastCmp > bound {
			t.Fatalf("m=%d: lastCmp=%d not within log bound %d", m, s.lastCmp, bound)
		}
		if s.lastCmp >= m/4 {
			t.Fatalf("m=%d: lastCmp=%d looks linear", m, s.lastCmp)
		}
	}
}
