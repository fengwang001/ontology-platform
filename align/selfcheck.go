package align

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
)

// CutReference computes the naive batch cut for snapshot n, summed by key.
func CutReference(items []Item, n int64) map[string]int64 {
	var seen [2]int64
	sum := map[string]int64{}
	for _, it := range items {
		if it.Kind == Barrier {
			seen[it.Ch]++
		} else if seen[it.Ch] < n {
			sum[it.Key] += it.Val
		}
	}
	return sum
}

func recs(items []Item) []Item {
	return slices.DeleteFunc(slices.Clone(items), func(it Item) bool { return it.Kind != Record })
}

func barCount(seq []Item) int64 {
	var c [2]int64
	for _, it := range seq {
		if it.Kind == Barrier {
			c[it.Ch]++
		}
	}
	return min(c[0], c[1])
}

// genSeqs builds deterministic pseudo-random valid sequences with
// runs of consecutive barriers.
func genSeqs() [][]Item {
	r := rand.New(rand.NewSource(279))
	var seqs [][]Item
	for _, ln := range []int{10, 50, 200, 1000} {
		seq := []Item{}
		var bar [2]int64
		for range ln {
			ch := r.Intn(2)
			if r.Intn(10) < 3 {
				bar[ch]++
				seq = append(seq, Item{Ch: ch, Kind: Barrier, ID: bar[ch]})
			} else {
				seq = append(seq, Item{Ch: ch, Kind: Record, Key: string(rune('a' + r.Intn(4))), Val: int64(r.Intn(9) + 1)})
			}
		}
		seqs = append(seqs, seq)
	}
	return seqs
}

var tenStep = []Item{ // canonical sequence from the spec
	{Ch: 0, Kind: Record, Key: "x", Val: 1}, {Ch: 1, Kind: Record, Key: "y", Val: 2}, {Ch: 0, Kind: Barrier, ID: 1},
	{Ch: 0, Kind: Record, Key: "x", Val: 10}, {Ch: 1, Kind: Record, Key: "x", Val: 3}, {Ch: 0, Kind: Record, Key: "y", Val: 20},
	{Ch: 1, Kind: Record, Key: "y", Val: 4}, {Ch: 1, Kind: Barrier, ID: 1}, {Ch: 1, Kind: Record, Key: "x", Val: 5}, {Ch: 0, Kind: Record, Key: "x", Val: 100},
}

// SelfCheck verifies the four invariants on built-in input sequences.
func SelfCheck() error {
	a := New(1 << 20)
	if _, err := a.Push(tenStep); err != nil {
		return err
	}
	s1, ok := a.Snapshot(1)
	if !ok || !maps.Equal(s1, map[string]int64{"x": 4, "y": 6}) ||
		!maps.Equal(a.State(), map[string]int64{"x": 119, "y": 26}) {
		return fmt.Errorf("selfcheck: snap1=%v state=%v", s1, a.State())
	}
	for i, seq := range genSeqs() {
		if err := checkSeq(seq); err != nil {
			return fmt.Errorf("selfcheck seq %d: %w", i, err)
		}
	}
	return checkScaling()
}

// checkSeq verifies invariants 1-4 on one generated sequence.
func checkSeq(seq []Item) error {
	a := New(1 << 20)
	if _, err := a.Push(seq); err != nil {
		return err
	}
	for n := int64(1); n <= barCount(seq); n++ {
		if s, ok := a.Snapshot(n); !ok || !maps.Equal(s, CutReference(seq, n)) {
			return fmt.Errorf("snapshot %d mismatch", n)
		}
	}
	if _, ok := a.Snapshot(barCount(seq) + 1); ok {
		return fmt.Errorf("unexpected extra snapshot")
	}
	sumOut := map[string]int64{}
	var outRec, want [2][]Item
	for _, o := range recs(a.st.out) {
		outRec[o.Ch] = append(outRec[o.Ch], o)
		sumOut[o.Key] += o.Val
	}
	for _, it := range recs(seq) {
		want[it.Ch] = append(want[it.Ch], it)
	}
	for c := range 2 {
		if got := append(outRec[c], recs(a.st.ch[c].Items())...); !slices.Equal(got, want[c]) {
			return fmt.Errorf("channel %d: order/loss/dup", c)
		}
	}
	if !maps.Equal(a.State(), sumOut) {
		return fmt.Errorf("state != sum(output records)")
	}
	trace := func() string {
		return fmt.Sprint(a.State(), a.st.out, a.st.ch[0].Items(), a.st.ch[1].Items(), len(a.st.snap))
	}
	before := trace()
	if _, err := a.Push([]Item{{Ch: 9, Kind: Record, Key: "z"}}); err != ErrBadElem {
		return fmt.Errorf("bad push: %v", err)
	}
	if before != trace() {
		return fmt.Errorf("rejected batch left a trace")
	}
	return nil
}

// checkScaling verifies block/alignment decisions examine O(1) buffer elements.
func checkScaling() error {
	for _, m := range []int{100, 1000, 10000} {
		a := New(4 * m)
		batch := make([]Item, 0, m+1)
		batch = append(batch, Item{Ch: 0, Kind: Barrier, ID: 1})
		for range m {
			batch = append(batch, Item{Ch: 0, Kind: Record, Key: "k", Val: 1})
		}
		if _, err := a.Push(batch); err != nil {
			return err
		}
		steps := []Item{{Ch: 0, Kind: Record, Key: "k"}, {Ch: 1, Kind: Record, Key: "k"}, {Ch: 1, Kind: Barrier, ID: 1}}
		limits := []int{2, 2, m + 2}
		for i, it := range steps {
			if _, err := a.Push([]Item{it}); err != nil || a.st.checked > limits[i] {
				return fmt.Errorf("step %d: examined %d > %d (m=%d)", i, a.st.checked, limits[i], m)
			}
		}
	}
	return nil
}
