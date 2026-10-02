package ontology

import (
	"fmt"
	"math"
	"math/bits"
	"sort"
)

// naiveModel is an independent, deliberately straightforward implementation of
// the specification. It keeps the full uncompacted history and recomputes
// compaction exactly as the prose describes, one key at a time.
type naiveModel struct {
	deeper map[string]int64
	recs   []Record
	maxSeq int64
	holds  map[int64]int
}

func newNaive(deeper map[string]int64) *naiveModel {
	m := &naiveModel{holds: map[int64]int{}}
	if len(deeper) > 0 {
		m.deeper = map[string]int64{}
		for k, v := range deeper {
			m.deeper[k] = v
		}
	}
	return m
}

func (m *naiveModel) add(k string, kind Kind, v int64) int64 {
	m.maxSeq++
	m.recs = append(m.recs, Record{Key: k, Seq: m.maxSeq, Kind: kind, Val: v})
	return m.maxSeq
}

func (m *naiveModel) snapshot() int64 {
	m.holds[m.maxSeq]++
	return m.maxSeq
}

func (m *naiveModel) release(s int64) bool {
	if m.holds[s] == 0 {
		return false
	}
	m.holds[s]--
	if m.holds[s] == 0 {
		delete(m.holds, s)
	}
	return true
}

func (m *naiveModel) get(k string, s uint64) (int64, bool) {
	var sum int64
	merged := false
	for i := len(m.recs) - 1; i >= 0; i-- {
		e := m.recs[i]
		if e.Key != k || uint64(e.Seq) > s {
			continue
		}
		switch e.Kind {
		case KindPut:
			return sum + e.Val, true
		case KindDelete:
			return sum, merged
		case KindMerge:
			sum += e.Val
			merged = true
		}
	}
	if base, ok := m.deeper[k]; ok {
		return sum + base, true
	}
	return sum, merged
}

// naiveCompact rewrites the naive run set following the spec literally
// (linear stripe lookup instead of binary search).
func (m *naiveModel) naiveCompact() ([]Record, Stats) {
	in := len(m.recs)
	var bounds []int64
	for s := range m.holds {
		bounds = append(bounds, s)
	}
	sort.Slice(bounds, func(i, j int) bool { return bounds[i] < bounds[j] })
	bounds = append(bounds, math.MaxInt64)

	stripeOf := func(q int64) int64 {
		for _, b := range bounds {
			if b >= q {
				return b
			}
		}
		return bounds[len(bounds)-1]
	}

	byKey := map[string][]Record{}
	for _, e := range m.recs {
		byKey[e.Key] = append(byKey[e.Key], e)
	}

	var allOut []Record
	var shadowed, folded, tombs, m2p int

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		recs := byKey[k]
		groups := map[int64][]Record{}
		var order []int64
		for i := len(recs) - 1; i >= 0; i-- {
			e := recs[i]
			st := stripeOf(e.Seq)
			if _, ok := groups[st]; !ok {
				order = append(order, st)
			}
			groups[st] = append(groups[st], e)
		}

		var produced []Record
		for _, st := range order {
			g := groups[st]
			e1 := g[0]
			if e1.Kind == KindPut || e1.Kind == KindDelete {
				produced = append(produced, Record{Key: k, Seq: e1.Seq, Kind: e1.Kind, Val: e1.Val})
				shadowed += len(g) - 1
				continue
			}
			var sum int64
			j := 0
			for j < len(g) && g[j].Kind == KindMerge {
				sum += g[j].Val
				j++
			}
			if j == len(g) {
				produced = append(produced, Record{Key: k, Seq: e1.Seq, Kind: KindMerge, Val: sum})
				folded += j - 1
			} else {
				b := g[j]
				if b.Kind == KindPut {
					sum += b.Val
				}
				produced = append(produced, Record{Key: k, Seq: e1.Seq, Kind: KindPut, Val: sum})
				folded += j
				shadowed += len(g) - j - 1
			}
		}

		_, inDeeper := m.deeper[k]
		for len(produced) > 0 {
			oldest := produced[len(produced)-1]
			if oldest.Kind != KindDelete || inDeeper {
				break
			}
			produced = produced[:len(produced)-1]
			tombs++
		}
		if len(produced) > 0 {
			oldest := &produced[len(produced)-1]
			if oldest.Kind == KindMerge && !inDeeper {
				oldest.Kind = KindPut
				m2p++
			}
		}
		allOut = append(allOut, produced...)
	}

	sort.Slice(allOut, func(i, j int) bool { return allOut[i].Seq < allOut[j].Seq })
	m.recs = append([]Record(nil), allOut...)

	st := Stats{
		In:          in,
		Out:         len(allOut),
		Shadowed:    shadowed,
		Folded:      folded,
		TombDropped: tombs,
		MergeToPut:  m2p,
	}
	return allOut, st
}

// heldSeqs returns the distinct held sequence numbers, ascending.
func (m *naiveModel) heldSeqs() []int64 {
	var out []int64
	for s, n := range m.holds {
		if n > 0 {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// probeBudget is the spec bound: In*(floor(log2(|S|+1))+1).
func probeBudget(in, live int) int {
	return in * (bits.Len64(uint64(live+1)) - 1 + 1)
}

func recsString(rs []Record) string {
	if len(rs) == 0 {
		return "[]"
	}
	s := "["
	for i, r := range rs {
		if i > 0 {
			s += ", "
		}
		kind := map[Kind]string{KindPut: "Put", KindMerge: "Merge", KindDelete: "Del"}[r.Kind]
		if r.Kind == KindDelete {
			s += fmt.Sprintf("%s(%s)@%d", kind, r.Key, r.Seq)
		} else {
			s += fmt.Sprintf("%s(%s,%d)@%d", kind, r.Key, r.Val, r.Seq)
		}
	}
	return s + "]"
}
