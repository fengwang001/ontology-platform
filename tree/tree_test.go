package tree

import (
	"testing"

	"ontology/stack"
)

func TestAggregates(t *testing.T) {
	cases := []struct {
		name       string
		stacks     [][]string
		maxDepth   int
		wantSelf   int64
		wantTotal  int64
		wantNodeST map[string][2]int64 // 单条路径下按节点名查 self/total
	}{
		{
			name:       "single depth1 self equals total",
			stacks:     [][]string{{"A"}},
			maxDepth:   0,
			wantSelf:   1,
			wantTotal:  1,
			wantNodeST: map[string][2]int64{"A": {1, 1}},
		},
		{
			name:       "same stack twice",
			stacks:     [][]string{{"A", "B"}, {"A", "B"}},
			maxDepth:   0,
			wantSelf:   2,
			wantTotal:  4,
			wantNodeST: map[string][2]int64{"A": {0, 2}, "B": {2, 2}},
		},
		{
			name:      "branching totals",
			stacks:    [][]string{{"A", "B"}, {"A", "C"}, {"A", "B", "D"}},
			maxDepth:  0,
			wantSelf:  3,
			wantTotal: 7,
			wantNodeST: map[string][2]int64{
				"A": {0, 3}, "B": {1, 2}, "C": {1, 1}, "D": {1, 1},
			},
		},
		{
			name:      "recursion keeps two F nodes",
			stacks:    [][]string{{"A", "F", "G", "F", "H"}},
			maxDepth:  0,
			wantSelf:  1,
			wantTotal: 5,
			wantNodeST: map[string][2]int64{
				"A": {0, 1}, "F": {0, 1}, "G": {0, 1}, "H": {1, 1},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := New(tc.maxDepth)
			for _, s := range tc.stacks {
				tr.InsertNames(s...)
			}
			if tr.Root.SelfSum() != tc.wantSelf {
				t.Fatalf("self sum = %d, want %d", tr.Root.SelfSum(), tc.wantSelf)
			}
			if tr.Root.TotalSum() != tc.wantTotal {
				t.Fatalf("total sum = %d, want %d", tr.Root.TotalSum(), tc.wantTotal)
			}
			if tr.Samples != tc.wantSelf {
				t.Fatalf("samples = %d, want %d", tr.Samples, tc.wantSelf)
			}
			for name, want := range tc.wantNodeST {
				nodes := findNodes(tr.Root, name)
				for _, n := range nodes {
					if n.Self != want[0] || n.Total != want[1] {
						t.Fatalf("node %s self/total = %d/%d, want %d/%d",
							name, n.Self, n.Total, want[0], want[1])
					}
				}
			}
		})
	}
}

func TestBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		insert     []string
		maxDepth   int
		invalid    int64
		truncated  int64
		leafMarked bool
		leafName   string
	}{
		{"empty rejected", nil, 3, 1, 0, false, ""},
		{"depth equal limit not truncated", []string{"A", "B", "C"}, 3, 0, 0, false, "C"},
		{"depth over limit truncated", []string{"A", "B", "C", "D"}, 3, 0, 1, true, "C"},
		{"depth one", []string{""}, 3, 0, 0, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := New(tc.maxDepth)
			tr.InsertNames(tc.insert...)
			if tr.InvalidSamples != tc.invalid || tr.TruncatedSamples != tc.truncated {
				t.Fatalf("invalid=%d trunc=%d, want %d/%d",
					tr.InvalidSamples, tr.TruncatedSamples, tc.invalid, tc.truncated)
			}
			if tc.leafName != "" || tc.insert != nil {
				leaf := findNodes(tr.Root, tc.leafName)
				if len(leaf) == 0 || leaf[len(leaf)-1].Truncated != tc.leafMarked {
					t.Fatalf("leaf mark mismatch: %+v", leaf)
				}
			}
		})
	}
	tr := New(3)
	tr.Insert(stack.FromNames("A", "B", "C", "D"))
	if findNodes(tr.Root, "D") != nil {
		t.Fatal("frame beyond limit must be dropped")
	}
}

func TestInsertCost(t *testing.T) {
	const n, depth, c = 100000, 20, 4
	tr := New(0)
	names := make([]string, depth)
	for i := range names {
		names[i] = "f" + itoa(i)
	}
	for i := 0; i < n; i++ {
		tr.InsertNames(names...)
	}
	compares, builds, _ := tr.Stats()
	bound := int64(n * depth * c)
	if compares > bound {
		t.Fatalf("compares=%d > bound=%d", compares, bound)
	}
	if builds != 0 {
		t.Fatalf("tree builds=%d, want 0", builds)
	}
	if tr.Root.SelfSum() != n {
		t.Fatalf("self sum=%d, want %d", tr.Root.SelfSum(), n)
	}
}

func findNodes(n *Node, name string) []*Node {
	var out []*Node
	if n.Name == name {
		out = append(out, n)
	}
	for _, c := range n.Children() {
		out = append(out, findNodes(c, name)...)
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
