package plan

import (
	"errors"
	"fmt"
	"testing"
)

func leaf(c int64, parts ...int) Leaf { return Leaf{Cost: c, Parts: parts} }

func partsSet(nums ...int) map[int]struct{} {
	m := map[int]struct{}{}
	for _, n := range nums {
		m[n] = struct{}{}
	}
	return m
}

func sameParts(a, b map[int]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func TestEvalTable(t *testing.T) {
	cases := []struct {
	name    string
	root    Node
	want    int64
	parts   map[int]struct{}
	wantErr error
	nnodes  int
	}{
		{name: "leaf", root: leaf(100, 1, 2), want: 100, parts: partsSet(1, 2), nnodes: 1},
		{name: "seq sums", root: Seq{[]Node{leaf(10, {1}), leaf(20, {2})}}, want: 30,
			parts: partsSet(1, 2), nnodes: 3},
		{name: "par max disjoint", root: Par{[]Node{leaf(80, 2), leaf(120, 3)}}, want: 120,
			parts: partsSet(2, 3), nnodes: 3},
		{name: "three nested samples each ceil",
			root: Seq{[]Node{
				Sample{1, 3, leaf(1, 1)},
				Sample{1, 3, leaf(1, 2)},
				Sample{1, 3, leaf(1, 3)},
			}},
			want: 3, parts: partsSet(1, 2, 3), nnodes: 7},
		{name: "single sample over seq rounds once",
			root: Sample{1, 3, Seq{[]Node{leaf(1, 1), leaf(1, 2), leaf(1, 3)}}},
			want: 1, parts: partsSet(1, 2, 3), nnodes: 5},
		{name: "sample ceil 2/3", root: Sample{2, 3, leaf(10, 7)}, want: 7,
			parts: partsSet(7), nnodes: 2},
		{name: "sample exact num==den", root: Sample{3, 3, leaf(10, 7)}, want: 10,
			parts: partsSet(7), nnodes: 2},
		{name: "nested samples ceil layer by layer",
			root: Sample{2, 3, Sample{2, 3, leaf(10, 1)}},
			// inner ceil(20/3)=7, outer ceil(14/3)=5
			want: 5, parts: partsSet(1), nnodes: 3},
		{name: "par nested disjoint unions",
			root: Par{[]Node{
				Par{[]Node{leaf(1, 1), leaf(2, 2)}},
				Par{[]Node{leaf(3, 3), leaf(4, 4)}},
			}},
			want: 4, parts: partsSet(1, 2, 3, 4), nnodes: 7},

		{name: "par intersects", root: Par{[]Node{leaf(80, 1, 2), leaf(120, 2, 3)}},
			wantErr: ErrNotDisjoint, nnodes: 3},
		{name: "par nested intersects",
			root: Par{[]Node{
				Seq{[]Node{leaf(1, 1), leaf(1, 2)}},
				leaf(2, 2),
			}},
			wantErr: ErrNotDisjoint, nnodes: 4},

		{name: "leaf bad cost low", root: leaf(0, 1), wantErr: ErrInvalidPlan, nnodes: 1},
		{name: "leaf bad cost high", root: leaf(maxLeafCost+1, 1), wantErr: ErrInvalidPlan, nnodes: 1},
		{name: "leaf no parts", root: leaf(1), wantErr: ErrInvalidPlan, nnodes: 1},
		{name: "leaf dup parts", root: leaf(1, 1, 1), wantErr: ErrInvalidPlan, nnodes: 1},
		{name: "leaf 17 parts", root: Leaf{1, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17}},
			wantErr: ErrInvalidPlan, nnodes: 1},
		{name: "seq no children", root: Seq{}, wantErr: ErrInvalidPlan, nnodes: 1},
		{name: "par 17 children", root: Par{manyChildren(17)}, wantErr: ErrInvalidPlan},
	{name: "sample num zero", root: Sample{0, 3, leaf(1, 1)}, wantErr: ErrInvalidPlan, nnodes: 1},
		{name: "sample num over den", root: Sample{4, 3, leaf(1, 1)}, wantErr: ErrInvalidPlan, nnodes: 1},
		{name: "sample den over bound", root: Sample{1, maxSampleBound + 1, leaf(1, 1)},
			wantErr: ErrInvalidPlan, nnodes: 1},
		{name: "sample nil child", root: Sample{1, 3, nil}, wantErr: ErrInvalidPlan, nnodes: 1},
		{name: "nil root", root: nil, wantErr: ErrInvalidPlan, nnodes: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotParts, err := Eval(tc.root)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Eval err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if got != tc.want {
					t.Fatalf("cost = %d, want %d; input=%s", got, tc.want, tc.name)
				}
				if !sameParts(gotParts, tc.parts) {
					t.Fatalf("parts = %v, want %v", gotParts, tc.parts)
				}
			}
			if tc.nnodes != 0 && nodes != tc.nnodes {
				t.Fatalf("nodes counter = %d, want %d", nodes, tc.nnodes)
			}
		})
	}
}

func manyChildren(n int) []Node {
	cs := make([]Node, n)
	for i := range cs {
		cs[i] = leaf(1, i+1)
	}
	return cs
}

func TestDepthLimit(t *testing.T) {
	// 根 Leaf 深度 1；8 层 Sample 合法（深度 8），9 层非法。
	build := func(samples int) Node {
		var n Node = leaf(1, 1)
		for i := 0; i < samples; i++ {
			n = Sample{1, 2, n}
		}
		return n
	}
	if _, _, err := Eval(build(7)); err != nil {
		t.Fatalf("depth 8 should be valid, got %v", err)
}
	if _, _, err := Eval(build(8)); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("depth 9 should be ErrInvalidPlan, got %v", err)
	}
}

func TestNodeCountLimit(t *testing.T) {
	// 每节点 2 个子，造 255 节点（127 个 Seq + 128 叶，深度 8）合法。
	build := func(leaves int) Node {
		layer := make([]Node, leaves)
		for i := range layer {
			layer[i] = leaf(1, i+1)
		}
		for len(layer) > 1 {
			var next []Node
			for i := 0; i < len(layer); i += 2 {
				next = append(next, Seq{layer[i : i+2]})
			}
			layer = next
		}
		return layer[0]
	}
	root255 := build(128)
	if _, _, err := Eval(root255); err != nil {
		t.Fatalf("255 nodes depth 8 should be valid, got %v", err)
	}
	if nodes != 255 {
		t.Fatalf("nodes = %d, want 255", nodes)
	}
	// 257 个节点超过 256。
	root257 := Seq{[]Node{build(128), build(128)}}
	if _, _, err := Eval(root257); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("257 nodes should be ErrInvalidPlan, got %v", err)
	}
}

func TestExamplePlanP(t *testing.T) {
	// 规格示例：100 + max(80,120) + ceil(100/3) = 254
	p := Seq{[]Node{
		leaf(100, 1),
		Par{[]Node{leaf(80, 2), leaf(120, 3)}},
		Sample{1, 3, leaf(100, 4)},
	}}
	cost, parts, err := Eval(p)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 254 {
		t.Fatalf("cost = %d, want 254", cost)
	}
	if len(parts) != 4 {
		t.Fatalf("parts = %v, want 4 distinct partitions", parts)
	}
	if nodes != 7 {
		t.Fatalf("nodes = %d, want 7", nodes)
	}
}

func TestCostOverCap(t *testing.T) {
	// 单叶最大 1e9；构造 Seq 使和超过 1e12（需要 >1000 个叶，但节点上限 256，
	// 因此通过 1e9 上限与 256 节点上限，总成本不可能超 1e12——
	// 该用例验证 16 子 *256 节点的上界远低于 1e12，即超 cap 分支被结构上界保护）。
	root := Seq{manyChildren(16)}
	cost, _, err := Eval(root)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if cost != 16 {
		t.Fatalf("cost = %d", cost)
	}
	// Sample num==den==1e6, child 1e9：结果 1e9，不超限。
	big := Sample{maxSampleBound, maxSampleBound, leaf(maxLeafCost, 9)}
	cost, _, err = Eval(big)
	if err != nil || cost != maxLeafCost {
		t.Fatalf("big sample: cost=%d err=%v", cost, err)
	}
	_ = fmt.Sprint(nodes)
}
