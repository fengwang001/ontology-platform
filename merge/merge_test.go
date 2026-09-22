package merge

import (
	"fmt"
	"sort"
	"testing"

	"ontology/record"
)

type sliceSource struct {
	recs []record.Record
	pos  int
}

func (s *sliceSource) Next() (record.Record, bool) {
	if s.pos >= len(s.recs) {
		return record.Record{}, false
	}
	r := s.recs[s.pos]
	s.pos++
	return r, true
}

func mergeRuns(t *testing.T, m *Merger, runs [][]record.Record) []record.Record {
	t.Helper()
	sources := make([]Source, len(runs))
	for i, run := range runs {
		sources[i] = &sliceSource{recs: run}
	}
	var out []record.Record
	if err := m.Merge(sources, func(r record.Record) error {
		out = append(out, r)
		return nil
	}); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	return out
}

func TestMergeOutputsSorted(t *testing.T) {
	cases := []struct {
		name string
		runs [][]record.Record
	}{
		{"no runs", nil},
		{"empty runs", [][]record.Record{nil, nil}},
		{"single run", [][]record.Record{{{Key: "a", Seq: 0}, {Key: "b", Seq: 1}}}},
		{"interleaved", [][]record.Record{
			{{Key: "a", Seq: 0}, {Key: "c", Seq: 2}},
			{{Key: "b", Seq: 1}, {Key: "d", Seq: 3}},
		}},
		{"equal keys keep seq order", [][]record.Record{
			{{Key: "x", Seq: 0}, {Key: "x", Seq: 2}},
			{{Key: "x", Seq: 1}, {Key: "x", Seq: 3}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var want []record.Record
			for _, run := range tc.runs {
				want = append(want, run...)
			}
			sort.Slice(want, func(i, j int) bool { return record.Less(want[i], want[j]) })
			got := mergeRuns(t, NewMerger(), tc.runs)
			if len(got) != len(want) {
				t.Fatalf("got %d records, want %d", len(got), len(want))
			}
			for i := range got {
				if got[i].Key != want[i].Key || got[i].Seq != want[i].Seq {
					t.Fatalf("position %d: got %+v want %+v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestComparesBound(t *testing.T) {
	cases := []struct{ n, k int64 }{
		{1, 1}, {100, 2}, {1000, 7}, {5000, 50}, {4096, 100},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("n=%d k=%d", tc.n, tc.k), func(t *testing.T) {
			runs := make([][]record.Record, tc.k)
			for i := int64(0); i < tc.n; i++ {
				key := fmt.Sprintf("k%06d", i)
				runs[i%tc.k] = append(runs[i%tc.k], record.Record{Key: key, Seq: uint64(i)})
			}
			for _, run := range runs {
				sort.Slice(run, func(a, b int) bool { return record.Less(run[a], run[b]) })
			}
			m := NewMerger()
			out := mergeRuns(t, m, runs)
			if int64(len(out)) != tc.n {
				t.Fatalf("got %d records, want %d", len(out), tc.n)
			}
			if bound := Bound(tc.n, tc.k); m.Compares() > bound {
				t.Fatalf("compares=%d exceeds bound 4*N*ceil(log2(K+1))=%d", m.Compares(), bound)
			}
		})
	}
}

// 同一个键被切进三个 run，每个 run 内局部位置都从 0 开始；
// 输出必须按全局到达序号（Seq）排列，而不是局部位置。
func TestEqualKeyAcrossRunsArrivalOrder(t *testing.T) {
	runs := [][]record.Record{
		{{Key: "x", Seq: 0}, {Key: "x", Seq: 3}, {Key: "x", Seq: 6}},
		{{Key: "x", Seq: 1}, {Key: "x", Seq: 4}, {Key: "x", Seq: 7}},
		{{Key: "x", Seq: 2}, {Key: "x", Seq: 5}, {Key: "x", Seq: 8}},
	}
	out := mergeRuns(t, NewMerger(), runs)
	for i, rec := range out {
		if rec.Seq != uint64(i) {
			t.Fatalf("position %d: seq=%d, want arrival order %d", i, rec.Seq, i)
		}
	}
}
