package lru

import (
	"strconv"
	"testing"
)

func TestTouchOrderAndAtime(t *testing.T) {
	cases := []struct {
		name   string
		touch  []string
		keys   []string
		atimes map[string]int64
	}{
		{"one", []string{"A"}, []string{"A"}, map[string]int64{"A": 1}},
		{"insert-order", []string{"A", "B"}, []string{"B", "A"},
			map[string]int64{"A": 1, "B": 2}},
		{"get-refreshes-MRU", []string{"A", "B", "A"}, []string{"A", "B"},
			map[string]int64{"A": 3, "B": 2}},
		{"no-ties", []string{"A", "B", "C", "B", "A"}, []string{"A", "B", "C"},
			map[string]int64{"A": 5, "B": 4, "C": 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := New()
			for _, k := range tc.touch {
				l.Touch(k)
			}
			if got := l.Keys(); !eq(got, tc.keys) {
				t.Fatalf("Keys=%v want %v", got, tc.keys)
			}
			for k, want := range tc.atimes {
				if got, ok := l.Atime(k); !ok || got != want {
					t.Fatalf("Atime(%s)=%d ok=%v want %d", k, got, ok, want)
				}
			}
		})
	}
}

func TestEvictPicksLRU(t *testing.T) {
	cases := []struct {
		touch []string
		want  string
		left  []string
	}{
		{[]string{"A", "B", "C"}, "A", []string{"C", "B"}},
		{[]string{"A", "B", "A"}, "B", []string{"A"}},
		{[]string{"A", "B", "C", "B"}, "A", []string{"B", "C"}},
	}
	for _, tc := range cases {
		l := New()
		for _, k := range tc.touch {
			l.Touch(k)
		}
		got, ok := l.Evict()
		if !ok || got != tc.want {
			t.Fatalf("Evict=%q ok=%v want %q", got, ok, tc.want)
		}
		if rest := l.Keys(); !eq(rest, tc.left) {
			t.Fatalf("after evict Keys=%v want %v", rest, tc.left)
		}
		if _, present := l.Atime(tc.want); present {
			t.Fatalf("evicted key %s still has an atime", tc.want)
		}
	}
}

func TestEvictEmpty(t *testing.T) {
	l := New()
	if _, ok := l.Evict(); ok {
		t.Fatal("Evict on empty must report no candidate")
	}
}

// TestEvictionScanDoesNotGrowWithM proves the LRU candidate is located
// via the ordered list (one back node), not a minimum-scan over the
// whole hot set: examined count stays at an m-independent constant.
func TestEvictionScanDoesNotGrowWithM(t *testing.T) {
	ms := []int{100, 1000, 10000}
	const bound = 1 // constant independent of m
	var first = -1
	for _, m := range ms {
		l := New()
		for i := 0; i < m; i++ {
			l.Touch(keyN(i))
		}
		k, ok := l.Evict()
		if !ok || k != keyN(0) {
			t.Fatalf("m=%d Evict=%q ok=%v, want %q", m, k, ok, keyN(0))
		}
		if l.lastEvictScans > bound {
			t.Fatalf("m=%d scanned %d hot keys, bound %d (linear scan?)",
				m, l.lastEvictScans, bound)
		}
		if first < 0 {
			first = l.lastEvictScans
		} else if l.lastEvictScans != first {
			t.Fatalf("scan count grew with m: %d then %d", first, l.lastEvictScans)
		}
		if l.Len() != m-1 {
			t.Fatalf("m=%d Len after evict=%d want %d", m, l.Len(), m-1)
		}
	}
}

func keyN(i int) string { return "k" + strconv.Itoa(i) }

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
