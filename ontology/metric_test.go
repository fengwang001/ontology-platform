package ontology

import (
	"fmt"
	"testing"
)

// TestMetricsIndependentOfGraphSize：在遍历可达邻域保持不变的前提下，
// 把图中与本次遍历无关的规模放大 1000 倍，访问度量必须保持不变。
func TestMetricsIndependentOfGraphSize(t *testing.T) {
	const (
		depth  = 3
		fanout = 3
	)
	build := func(noise int) (*Page, error) {
		st := NewStore(AllowAllPolicy{})
		// 固定的小树形邻域：s -> a_i -> b_ij -> b_ijk
		st.AddObject(Object{ID: "s"})
		for i := 0; i < fanout; i++ {
			a := ObjectID(fmt.Sprintf("a%d", i))
			st.AddObject(Object{ID: a})
			st.AddLink(Link{Type: "edge", From: "s", To: a})
			for j := 0; j < fanout; j++ {
				b := ObjectID(fmt.Sprintf("b%d-%d", i, j))
				st.AddObject(Object{ID: b})
				st.AddLink(Link{Type: "edge", From: a, To: b})
				for k := 0; k < fanout; k++ {
					c := ObjectID(fmt.Sprintf("c%d-%d-%d", i, j, k))
					st.AddObject(Object{ID: c})
					st.AddLink(Link{Type: "edge", From: b, To: c})
				}
			}
		}
		// 噪声：与 s 邻域完全无关的巨型连通分量，含高度数节点。
		for n := 0; n < noise; n++ {
			id := ObjectID(fmt.Sprintf("noise%d", n))
			st.AddObject(Object{ID: id})
			st.AddLink(Link{Type: "edge", From: "hub", To: id})
		}
		if noise > 0 {
			st.AddObject(Object{ID: "hub"})
		}
		return NewTraverser(st).Traverse(nil, TraverseParams{
			Start: "s", MaxDepth: depth, MaxFanout: fanout, PageSize: 1000,
		})
	}

	small, err := build(0)
	if err != nil {
		t.Fatal(err)
	}
	big, err := build(3000)
	if err != nil {
		t.Fatal(err)
	}
	if small.ResultMetrics != big.ResultMetrics {
		t.Fatalf("metrics grew with graph size: small=%+v big=%+v", small.ResultMetrics, big.ResultMetrics)
	}

	// 上界论证：层数 <= depth+1，每层对象数 <= fanout^层；
	// 每个被访问对象最多检查 fanout+1 条边（+1 用于确认扇出截断）。
	visitedBound := 0
	pow := 1
	for d := 0; d <= depth; d++ {
		visitedBound += pow
		pow *= fanout
	}
	linkBound := (visitedBound - 1) * (fanout + 1)
	if big.ResultMetrics.ObjectsVisited > visitedBound {
		t.Fatalf("visited %d exceeds bound %d", big.ResultMetrics.ObjectsVisited, visitedBound)
	}
	if big.ResultMetrics.LinksExamined > linkBound {
		t.Fatalf("links examined %d exceeds bound %d", big.ResultMetrics.LinksExamined, linkBound)
	}
	t.Logf("metrics at 3000-noise graph: %+v (visited bound=%d, link bound=%d)",
		big.ResultMetrics, visitedBound, linkBound)
}
