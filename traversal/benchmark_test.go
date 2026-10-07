package traversal

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkTraversalLargeGraph 大规模图上的遍历基准，配合
// TestResultSizeIndependentOfUnrelatedGraphGrowth 展示遍历开销
// 只与实际可达部分相关，祖先核对为 O(1)/跳。
func BenchmarkTraversalLargeGraph(b *testing.B) {
	g := buildBenchmarkGraph(100000, 200000)
	svc := NewService(g, nil)
	req := TraversalRequest{Start: "root", Directions: dirs(DirOutbound), MaxDepth: 12}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := svc.Traverse(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
		if res.Stats.AncestorProbes != res.Stats.CandidateEdges {
			b.Fatalf("probes %d != edges %d", res.Stats.AncestorProbes, res.Stats.CandidateEdges)
		}
	}
}

func buildBenchmarkGraph(nObjects, nLinks int) *Graph {
	g := NewGraph()
	mut := Mutation{AddLinkTypes: []LinkTypeID{tRel}, AddObjects: []ObjectID{"root"}}
	for i := 0; i < nObjects; i++ {
		mut.AddObjects = append(mut.AddObjects, ObjectID(fmt.Sprintf("o%d", i)))
	}
	// root 下挂一条树状可达子图（少量节点，用于遍历），其余链接全部
	// 位于与 root 不连通的巨型组件，用于度量无关规模的影响。
	for i := 0; i < 40; i++ {
		var parent ObjectID = "root"
		if i > 0 {
			parent = ObjectID(fmt.Sprintf("tree%d", i-1))
		}
		id := ObjectID(fmt.Sprintf("tree%d", i))
		mut.AddObjects = append(mut.AddObjects, id)
		mut.AddLinks = append(mut.AddLinks, Link{
			ID: LinkID(fmt.Sprintf("tree%d", i)), Type: tRel, Source: parent, Target: id,
		})
	}
	for i := 0; i < nLinks; i++ {
		from := i % nObjects
		to := (i*7 + 3) % nObjects
		mut.AddLinks = append(mut.AddLinks, Link{
			ID:     LinkID(fmt.Sprintf("l%d", i)),
			Type:   tRel,
			Source: ObjectID(fmt.Sprintf("o%d", from)),
			Target: ObjectID(fmt.Sprintf("o%d", to)),
		})
	}
	if _, err := g.Batch(mut); err != nil {
		panic(err)
	}
	return g
}
