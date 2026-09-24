package semi

import (
	"testing"

	"ontology/rel"
)

func sampleEdges() []rel.T {
	return []rel.T{
		{X: "a", Y: "b"}, {X: "b", Y: "c"}, {X: "c", Y: "d"},
		{X: "b", Y: "d"}, {X: "d", Y: "e"}, {X: "e", Y: "b"},
	}
}

func hasT(ts []rel.T, t rel.T) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

// TestSampleRoundTrace 逐格核验 NOTES 五行表（Delta/候选/新增/丢弃/path 大小）。
func TestSampleRoundTrace(t *testing.T) {
	e := New(sampleEdges())
	path := e.Eval()
	rows := e.Rows()
	want := []struct {
		cands, added, size int
		delta, dropped     []rel.T
	}{
		{0, 6, 6, []rel.T{{X: "a", Y: "b"}, {X: "b", Y: "c"}, {X: "c", Y: "d"}, {X: "b", Y: "d"}, {X: "d", Y: "e"}, {X: "e", Y: "b"}}, nil},
		{8, 7, 13, []rel.T{{X: "a", Y: "c"}, {X: "a", Y: "d"}, {X: "c", Y: "e"}, {X: "b", Y: "e"}, {X: "d", Y: "b"}, {X: "e", Y: "c"}, {X: "e", Y: "d"}},
			[]rel.T{{X: "b", Y: "d"}}},
		{8, 6, 19, []rel.T{{X: "a", Y: "e"}, {X: "c", Y: "b"}, {X: "b", Y: "b"}, {X: "d", Y: "c"}, {X: "d", Y: "d"}, {X: "e", Y: "e"}},
			[]rel.T{{X: "a", Y: "d"}, {X: "e", Y: "d"}}},
		{8, 1, 20, []rel.T{{X: "c", Y: "c"}},
			[]rel.T{{X: "a", Y: "b"}, {X: "b", Y: "c"}, {X: "b", Y: "d"}, {X: "c", Y: "d"}, {X: "d", Y: "d"}, {X: "d", Y: "e"}, {X: "e", Y: "b"}}},
		{1, 0, 20, nil, []rel.T{{X: "c", Y: "d"}}},
	}
	if len(rows) != len(want) || len(path) != 20 {
		t.Fatalf("rounds=%d path=%d, want %d rounds and 20 path", len(rows), len(path), len(want))
	}
	for i, w := range want {
		r := rows[i]
		if r.Cands != w.cands || r.Added != w.added || r.PathSize != w.size || len(r.Delta) != w.added {
			t.Errorf("R%d cands=%d added=%d size=%d delta=%d, want %d/%d/%d/%d",
				i, r.Cands, r.Added, r.PathSize, len(r.Delta), w.cands, w.added, w.size, w.added)
		}
		for _, d := range w.delta {
			if !hasT(r.Delta, d) {
				t.Errorf("R%d delta missing %v", i, d)
			}
		}
		for _, d := range w.dropped {
			if !hasT(r.Dropped, d) {
				t.Errorf("R%d dropped missing %v", i, d)
			}
		}
	}
}

// TestFixpointLastDeltaEmpty 钉住不变量 2：必然终止且最后一轮 Delta 为空。
func TestFixpointLastDeltaEmpty(t *testing.T) {
	cases := [][]rel.T{
		sampleEdges(),
		{{X: "a", Y: "b"}},
		{{X: "a", Y: "b"}, {X: "b", Y: "a"}}, // 纯二结点环
		nil,
	}
	for _, edges := range cases {
		e := New(edges)
		e.Eval()
		rows := e.Rows()
		if len(rows) == 0 || len(rows[len(rows)-1].Delta) != 0 {
			t.Fatalf("last round delta non-empty: %+v", rows)
		}
	}
}

// TestDeltaEnteredOnce 钉住不变量 3：每个导出元组恰在一轮进入 Delta，并集恰为 path。
func TestDeltaEnteredOnce(t *testing.T) {
	e := New(sampleEdges())
	path := e.Eval()
	seen := map[rel.T]int{}
	for _, r := range e.Rows() {
		for _, d := range r.Delta {
			if _, dup := seen[d]; dup {
				t.Fatalf("tuple %v entered delta twice", d)
			}
			seen[d] = r.Round
		}
	}
	if len(seen) != len(path) {
		t.Fatalf("union of deltas %d != path %d", len(seen), len(path))
	}
	for _, p := range path {
		if _, ok := seen[p]; !ok {
			t.Fatalf("path tuple %v never entered delta", p)
		}
	}
	if seen[rel.T{X: "a", Y: "d"}] != 1 || seen[rel.T{X: "c", Y: "c"}] != 3 {
		t.Fatalf("ad must enter at R1, cc at R3; got ad=%d cc=%d",
			seen[rel.T{X: "a", Y: "d"}], seen[rel.T{X: "c", Y: "c"}])
	}
}

// TestChainCandidateCount 钉住复杂度：链图候选总数恰为 m(m-1)/2（计数器不导出）。
func TestChainCandidateCount(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		if !VerifyChain(m) {
			t.Fatalf("m=%d: candidate total != m(m-1)/2 or closure wrong size", m)
		}
	}
}
