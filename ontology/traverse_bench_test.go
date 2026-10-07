package ontology

import "testing"

// A single long simple path: the production implementation probes each
// candidate edge with one O(1) lookup, whereas the naive reference performs
// a linear scan whose cost grows with path depth.
func longPathSnapshot(n int) Snapshot {
	g := NewMemGraph()
	for i := 0; i < n; i++ {
		mustAdd(nil, g, ObjectID(benchID(i)))
	}
	g.DefineLinkType("t")
	for i := 0; i+1 < n; i++ {
		mustAddLink(nil, g, Link{
			ID:     LinkID(benchID(i)),
			Type:   "t",
			Source: ObjectID(benchID(i)),
			Target: ObjectID(benchID(i + 1)),
		})
	}
	return g.Snapshot()
}

func benchID(i int) string {
	return "n" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func BenchmarkTraverseHashAncestors(b *testing.B) {
	snap := longPathSnapshot(2000)
	req := TraverseRequest{Start: "n0", LinkTypes: outOnly("t"), MaxDepth: 100000}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := TraverseOnSnapshot(snap, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNaiveLinearAncestors(b *testing.B) {
	snap := longPathSnapshot(2000)
	req := TraverseRequest{Start: "n0", LinkTypes: outOnly("t"), MaxDepth: 100000}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := NaiveTraverse(snap, req); err != nil {
			b.Fatal(err)
		}
	}
}
