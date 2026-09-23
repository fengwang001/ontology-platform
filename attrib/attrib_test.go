package attrib

import (
	"testing"

	"ontology/tree"
)

func TestNodeRanking(t *testing.T) {
	cases := []struct {
		name      string
		stacks    [][]string
		wantSelf  []string
		wantTotal []string
	}{
		{
			name:      "zero samples empty",
			stacks:    nil,
			wantSelf:  nil,
			wantTotal: nil,
		},
		{
			name:      "single sample",
			stacks:    [][]string{{"A", "B"}},
			wantSelf:  []string{"B", "A"},
			wantTotal: []string{"A", "B"},
		},
		{
			name: "mixed",
			stacks: [][]string{
				{"A", "B"}, {"A", "C"}, {"A", "B", "D"}, {"X"},
			},
			// B,C,D,X 各 self=1 按名称升序，A 的 self=0 垫底。
			wantSelf:  []string{"B", "C", "D", "X", "A"},
			wantTotal: []string{"A", "B", "C", "D", "X"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tree.New(0)
			for _, s := range tc.stacks {
				tr.InsertNames(s...)
			}
			gotSelf := namesOf(NodesBySelf(tr))
			gotTotal := namesOf(NodesByTotal(tr))
			if !eq(gotSelf, tc.wantSelf) || !eq(gotTotal, tc.wantTotal) {
				t.Fatalf("self=%v want %v; total=%v want %v",
					gotSelf, tc.wantSelf, gotTotal, tc.wantTotal)
			}
		})
	}
}

func TestFunctionAttribution(t *testing.T) {
	cases := []struct {
		name   string
		stacks [][]string
		want   map[string][2]int64 // func -> self,total
	}{
		{
			name:   "recursive F outer only",
			stacks: [][]string{{"A", "F", "G", "F", "H"}},
			want: map[string][2]int64{
				"A": {0, 1}, "F": {0, 1}, "G": {0, 1}, "H": {1, 1},
			},
		},
		{
			name: "two recursive branches outer totals",
			stacks: [][]string{
				{"F", "F"}, {"F", "G"},
			},
			want: map[string][2]int64{
				"F": {1, 2}, "G": {1, 1},
			},
		},
		{
			name: "self inside recursion sums across nodes",
			stacks: [][]string{
				{"F"}, {"F", "F"},
			},
			want: map[string][2]int64{
				"F": {2, 2},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tree.New(0)
			for _, s := range tc.stacks {
				tr.InsertNames(s...)
			}
			got := map[string][2]int64{}
			for _, f := range Functions(tr) {
				got[f.Name] = [2]int64{f.Self, f.Total}
			}
			for name, w := range tc.want {
				g := got[name]
				if g != w {
					t.Fatalf("func %s self/total=%d/%d want %d/%d",
						name, g[0], g[1], w[0], w[1])
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d funcs, want %d", len(got), len(tc.want))
			}
			if _, builds, _ := tr.Stats(); builds != 0 {
				t.Fatalf("tree builds=%d, want 0", builds)
			}
		})
	}
}

func TestExclude(t *testing.T) {
	cases := []struct {
		name     string
		stacks   [][]string
		excluded map[string]bool
		want     map[string]int64 // func -> adjusted self
		wantSum  int64
	}{
		{
			name:     "exclude leaf self pushed to parent",
			stacks:   [][]string{{"A", "B"}},
			excluded: map[string]bool{"B": true},
			want:     map[string]int64{"A": 1},
			wantSum:  1,
		},
		{
			name: "exclude mid frame bubbles up",
			stacks: [][]string{
				{"A", "B", "C"}, {"A", "B"},
			},
			excluded: map[string]bool{"B": true},
			want:     map[string]int64{"A": 1, "C": 1},
			wantSum:  2,
		},
		{
			name:     "exclude top level drops its self",
			stacks:   [][]string{{"A"}, {"B"}},
			excluded: map[string]bool{"A": true},
			want:     map[string]int64{"B": 1},
			wantSum:  1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tree.New(0)
			for _, s := range tc.stacks {
				tr.InsertNames(s...)
			}
			rows := Exclude(tr, tc.excluded)
			var sum int64
			got := map[string]int64{}
			for _, r := range rows {
				got[r.Name] = r.Self
				sum += r.Self
			}
			for name, w := range tc.want {
				if got[name] != w {
					t.Fatalf("%s: %s adjusted self=%d want %d", tc.name, name, got[name], w)
				}
			}
			for name := range got {
				if tc.excluded[name] {
					t.Fatalf("excluded %s must not appear", name)
				}
			}
			if sum != tc.wantSum {
				t.Fatalf("sum=%d want %d", sum, tc.wantSum)
			}
		})
	}
}

func namesOf(rows []NodeStat) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

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
