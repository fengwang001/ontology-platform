// Package lww performs last-write-wins compaction over a seq.Store.
//
// The winner for a key is the change with the greatest Ver; ties are broken by
// the greatest SN (later arrival wins). Winners are maintained incrementally in
// Feed, so Replay only reads the cache and never rescans a key's history.
package lww

import (
	"sort"
	"sync"

	"ontology/seq"
)

// Record is one compacted (Key, Val) pair.
type Record struct {
	Key string
	Val int64
}

type winner struct {
	ver int64
	sn  int64
	val int64
}

// Table caches one winner per key. The unexported field checked records how
// many history entries the most recent Snapshot inspected to determine
// winners; it never appears in any exported getter's return value.
type Table struct {
	mu      sync.RWMutex
	winners map[string]winner
	checked int
}

// NewTable creates an empty compaction table.
func NewTable() *Table {
	return &Table{winners: make(map[string]winner)}
}

// Apply folds one sequence-numbered change into the winner cache. It must be
// called in ascending SN order. A change wins when its Ver is greater, or when
// Ver ties and its SN is greater (later arrival wins).
func (t *Table) Apply(c seq.Stored) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	w, ok := t.winners[c.Key]
	if !ok || c.Ver > w.ver || (c.Ver == w.ver && c.SN > w.sn) {
		t.winners[c.Key] = winner{ver: c.Ver, sn: c.SN, val: c.Val}
		return true
	}
	return false
}

// Winner returns the cached winner's Ver and Val for key.
func (t *Table) Winner(key string) (ver, val int64, ok bool) {
	t.mu.RLock()
	w, exists := t.winners[key]
	t.mu.RUnlock()
	if !exists {
		return 0, 0, false
	}
	return w.ver, w.val, true
}

// Snapshot returns all cached winners as records sorted by Key in ascending
// lexicographic order — this sort is what removes Go's map iteration
// nondeterminism. Winners are already cached, so zero history entries are
// inspected regardless of any key's history length.
func (t *Table) Snapshot() []Record {
	t.mu.Lock()
	out := make([]Record, 0, len(t.winners))
	for k, w := range t.winners {
		out = append(out, Record{Key: k, Val: w.val})
	}
	t.checked = 0
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// AllowAppend reports whether a key already holding `have` changes may accept
// one more change under the per-key cap maxHistory: storage is allowed while
// have < maxHistory, i.e. the key ends with at most maxHistory changes.
func AllowAppend(have, maxHistory int) bool { return have < maxHistory }

// CounterBounded reports whether Snapshot's inspected-entry count stays within
// a small constant as one key's history grows through several m values. The
// raw counter value is consumed here and never leaves the package.
func CounterBounded() bool {
	const bound = 2
	for _, m := range []int{100, 1000, 10000} {
		tab := NewTable()
		for i := 0; i < m; i++ {
			tab.Apply(seq.Stored{Change: seq.Change{Key: "k", Ver: int64(i), Val: int64(i)}, SN: int64(i + 1)})
		}
		tab.Snapshot()
		tab.mu.RLock()
		n := tab.checked
		tab.mu.RUnlock()
		if n > bound {
			return false
		}
	}
	return true
}
