package lww

import (
	"reflect"
	"testing"

	"ontology/seq"
)

func st(key string, sn, ver, val int64) seq.Stored {
	return seq.Stored{Change: seq.Change{Key: key, Ver: ver, Val: val}, SN: sn}
}

// TestApplyWinner pins invariant 3: greatest Ver wins; equal Ver goes to the
// greatest SN (later arrival); an unknown key reports no winner.
func TestApplyWinner(t *testing.T) {
	cases := []struct {
		name string
		in   []seq.Stored
		key  string
		ver  int64
		val  int64
		ok   bool
	}{
		{"first", []seq.Stored{st("a", 1, 10, 100)}, "a", 10, 100, true},
		{"higher ver replaces", []seq.Stored{st("a", 1, 10, 100), st("a", 2, 12, 120)}, "a", 12, 120, true},
		{"lower ver ignored", []seq.Stored{st("a", 1, 10, 100), st("a", 2, 5, 50)}, "a", 10, 100, true},
		{"equal ver later sn wins", []seq.Stored{st("a", 1, 10, 100), st("a", 3, 10, 200)}, "a", 10, 200, true},
		{"equal ver earlier sn kept", []seq.Stored{st("a", 3, 10, 200), st("a", 1, 10, 100)}, "a", 10, 200, true},
		{"unknown key", nil, "z", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := NewTable()
			for _, c := range tc.in {
				tab.Apply(c)
			}
			ver, val, ok := tab.Winner(tc.key)
			if ok != tc.ok || ver != tc.ver || val != tc.val {
				t.Fatalf("Winner(%q)=(%d,%d,%v), want (%d,%d,%v)", tc.key, ver, val, ok, tc.ver, tc.val, tc.ok)
			}
		})
	}
}

// TestSnapshotSorted pins Key-ascending output regardless of feed/map order.
func TestSnapshotSorted(t *testing.T) {
	tab := NewTable()
	for _, c := range []seq.Stored{st("m", 1, 1, 1), st("a", 2, 3, 3), st("z", 3, 2, 2), st("b", 4, 9, 9)} {
		tab.Apply(c)
	}
	want := []Record{{Key: "a", Val: 3}, {Key: "b", Val: 9}, {Key: "m", Val: 1}, {Key: "z", Val: 2}}
	if got := tab.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot=%v, want %v", got, want)
	}
}

// TestCheckedCounterBoundedInM proves Replay reads cached winners instead of
// rescanning history: the unexported inspected-entry count must stay within a
// small constant as one key's history grows across m = 100..10000, for three
// version patterns (monotonic, all-tie, decreasing).
func TestCheckedCounterBoundedInM(t *testing.T) {
	const bound = 2
	modes := map[string]func(i, m int) int64{
		"rising":  func(i, m int) int64 { return int64(i) },
		"equal":   func(i, m int) int64 { return 7 },
		"falling": func(i, m int) int64 { return int64(m - i) },
	}
	for _, m := range []int{100, 1000, 10000} {
		for name, ver := range modes {
			tab := NewTable()
			for i := 0; i < m; i++ {
				tab.Apply(st("k", int64(i+1), ver(i, m), int64(i)))
			}
			tab.Snapshot()
			tab.mu.RLock()
			n := tab.checked
			tab.mu.RUnlock()
			if n > bound {
				t.Fatalf("mode=%s m=%d: checked=%d > bound=%d (history rescanned)", name, m, n, bound)
			}
		}
	}
}
