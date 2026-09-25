package replay

import (
	"fmt"
	"testing"

	"ontology/buf"
)

// spillSet pushes m distinct Set changes through a limit-L buffer and then a
// Del sentinel: the sentinel trips the final spill, so all m keys live in
// disk blocks and the memory tail holds only the (non-matching) sentinel.
func spillSet(t *testing.T, m, L int) *Store {
	t.Helper()
	b := buf.NewBuffer(L)
	st := New()
	for i := 0; i < m; i++ {
		if blk := b.Append(buf.Change{Op: buf.Set, Key: fmt.Sprintf("k%06d", i), Val: "v"}); blk != nil {
			st.Spill(blk)
		}
	}
	if blk := b.Append(buf.Change{Op: buf.Del, Key: "__sentinel__"}); blk != nil {
		st.Spill(blk)
	}
	if got := st.Blocks(); got != m/L {
		t.Fatalf("m=%d L=%d: blocks=%d want %d (all keys must be on disk)", m, L, got, m/L)
	}
	st.Replay(b.Pending())
	return st
}

// TestProbeBound proves hash positioning: with the view holding m existing
// keys (all spilled to disk blocks), replaying inspects at most probeBound
// keys per change regardless of m. probeMax is read in-package only; no
// exported method exposes its numeric value.
func TestProbeBound(t *testing.T) {
	cases := []struct{ m, L int }{
		{100, 10}, {1000, 100}, {10000, 1000},
	}
	first := -1
	for _, c := range cases {
		st := spillSet(t, c.m, c.L)
		if st.probeMax <= 0 || st.probeMax > probeBound {
			t.Fatalf("m=%d: probes per change = %d, want in [1,%d]", c.m, st.probeMax, probeBound)
		}
		if first == -1 {
			first = st.probeMax
		} else if st.probeMax != first { // must not grow with m
			t.Fatalf("m=%d: probes=%d grew from m=100 value %d", c.m, st.probeMax, first)
		}
	}
}

// TestReplayTable is a table-driven check of Set overwrite, Del removal and
// block-then-tail FIFO against a naive per-change map.
func TestReplayTable(t *testing.T) {
	cases := []struct {
		name string
		L    int
		in   []buf.Change
		want map[string]string
	}{
		{"set overwrite", 2, []buf.Change{
			{Op: buf.Set, Key: "a", Val: "1"}, {Op: buf.Set, Key: "a", Val: "2"},
		}, map[string]string{"a": "2"}},
		{"del removes", 2, []buf.Change{
			{Op: buf.Set, Key: "a", Val: "1"}, {Op: buf.Del, Key: "a"},
		}, map[string]string{}},
		{"del survives a spill", 2, []buf.Change{
			{Op: buf.Set, Key: "a", Val: "1"}, {Op: buf.Set, Key: "b", Val: "2"},
			{Op: buf.Set, Key: "c", Val: "3"}, {Op: buf.Del, Key: "a"},
		}, map[string]string{"b": "2", "c": "3"}},
		{"fifo across blocks and tail", 3, []buf.Change{
			{Op: buf.Set, Key: "a", Val: "1"}, {Op: buf.Set, Key: "b", Val: "2"},
			{Op: buf.Set, Key: "c", Val: "3"}, {Op: buf.Del, Key: "b"},
			{Op: buf.Set, Key: "d", Val: "4"}, {Op: buf.Set, Key: "a", Val: "9"},
			{Op: buf.Del, Key: "c"}, {Op: buf.Set, Key: "e", Val: "5"},
		}, map[string]string{"a": "9", "d": "4", "e": "5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf.NewBuffer(tc.L)
			st := New()
			for _, c := range tc.in {
				if blk := b.Append(c); blk != nil {
					st.Spill(blk)
				}
			}
			got := map[string]string{}
			for _, kv := range st.Replay(b.Pending()) {
				got[kv.Key] = kv.Val
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("key %s: got %q want %q (full %v)", k, got[k], v, got)
				}
			}
		})
	}
}
