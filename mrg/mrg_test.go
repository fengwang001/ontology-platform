package mrg

import (
	"reflect"
	"sort"
	"testing"

	"ontology/run"
)

func ceilLog2(m int) int {
	n := 0
	for (1 << n) < m {
		n++
	}
	return n
}

// TestHeapProbeBound builds m sorted runs and merges them; every
// cursor advance must inspect at most 2*ceil(log2 m)+2 run heads,
// proving a heap is used instead of a linear scan over all run heads.
func TestHeapProbeBound(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		runs := make([][]int, m)
		for i := range runs {
			runs[i] = []int{i, i + m, i + 2*m} // ascending inside
		}
		mg := newMerger(runs)
		bound := 2*ceilLog2(m) + 2
		prev, n := -1, 0
		for {
			k, ok := mg.next()
			if !ok {
				break
			}
			if k < prev {
				t.Fatalf("m=%d: output not ascending", m)
			}
			prev, n = k, n+1
			if mg.probes > bound {
				t.Fatalf("m=%d: probes %d exceeds bound %d", m, mg.probes, bound)
			}
		}
		if n != 3*m {
			t.Fatalf("m=%d: merged %d elements, want %d", m, n, 3*m)
		}
	}
}

func TestSortedMerge(t *testing.T) {
	cases := []struct {
		name  string
		runs  [][]int
		fanIn int
		want  []int
	}{
		{"no runs", nil, 2, nil},
		{"single run is the sequence", [][]int{{3, 1, 2}}, 2, []int{3, 1, 2}},
		{"tie breaks by smaller run id", [][]int{{1, 3}, {1, 2}}, 2, []int{1, 1, 2, 3}},
		{"cascade when runs exceed fanIn", [][]int{{1, 5}, {2, 6}, {3, 7}, {4, 8}}, 2,
			[]int{1, 2, 3, 4, 5, 6, 7, 8}},
		{"fanIn 3 cascade", [][]int{{4}, {1}, {9}, {2}, {7}}, 3, []int{1, 2, 4, 7, 9}},
	}
	for _, c := range cases {
		rs := make([]run.Run, len(c.runs))
		for i, keys := range c.runs {
			rs[i] = run.Run{ID: i, Keys: keys}
		}
		got, err := Sorted(rs, c.fanIn)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if _, err := Sorted(nil, 1); err != ErrBadFanIn {
		t.Fatalf("fanIn<2: got %v", err)
	}
}

func TestJoinCartesian(t *testing.T) {
	cases := []struct {
		name string
		r, s []int
		want []Pair
	}{
		{"equal runs cross-product", []int{3, 3}, []int{3, 3, 3},
			[]Pair{{3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}}},
		{"disjoint", []int{1, 3}, []int{2, 4}, nil},
		{"mixed", []int{1, 2, 2, 5}, []int{2, 3}, []Pair{{2, 2}, {2, 2}}},
		{"empty side", nil, []int{1}, nil},
	}
	for _, c := range cases {
		mk := func(keys []int) []run.Run {
			if keys == nil {
				return nil
			}
			cp := append([]int(nil), keys...)
			sort.Ints(cp)
			return []run.Run{{ID: 0, Keys: cp}}
		}
		j, err := New(mk(c.r), mk(c.s), 2)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := j.Join()
		if !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if _, err := New(nil, nil, 1); err != ErrBadFanIn {
		t.Fatalf("New fanIn<2: got %v", err)
	}
}
