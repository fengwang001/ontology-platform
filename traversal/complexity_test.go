package traversal

import (
	"context"
	"fmt"
	"testing"
)

// TestAncestorCheckConstantPerHop 无论图多大、路径多长，优化实现中
// 每一跳的祖先归属核对恰好触发 1 次探测（哈希集合 O(1)），
// 总探测次数等于候选边数；而朴素实现的探测次数随路径长度线性增长。
//
// 该计数器是确定性的、可在测试中复现核对的复杂度证据：
// 优化实现 AncestorProbes == CandidateEdges，且两者都不随无关图规模增长。
func TestAncestorCheckConstantPerHop(t *testing.T) {
	for _, bulk := range []int{0, 1000, 10000} {
		g := buildChainWithBulk(t, 50, bulk)
		snap := g.loadSnapshot()
		req := TraversalRequest{Start: "n0", Directions: dirs(DirOutbound), MaxDepth: 50}

		opt, err := TraverseSnapshot(snap, req)
		if err != nil {
			t.Fatal(err)
		}
		naive, err := NaiveTraverse(snap, req)
		if err != nil {
			t.Fatal(err)
		}

		// 优化实现：每次核对恰好 1 次探测，与 bulk 和路径长度无关。
		if opt.Stats.AncestorProbes != opt.Stats.CandidateEdges {
			t.Fatalf("bulk=%d: probes %d must equal candidate edges %d",
				bulk, opt.Stats.AncestorProbes, opt.Stats.CandidateEdges)
		}
		if opt.Stats.AncestorChecks != opt.Stats.AncestorProbes {
			t.Fatalf("bulk=%d: checks %d must equal probes %d",
				bulk, opt.Stats.AncestorChecks, opt.Stats.AncestorProbes)
		}

		// 朴素实现：同一条 50 跳链路上探测次数随深度线性累积
		// （第 k 跳需扫描 k+1 个祖先），必然显著大于优化实现。
		if naive.Stats.AncestorProbes <= opt.Stats.AncestorProbes {
			t.Fatalf("bulk=%d: naive probes %d should exceed optimized %d",
				bulk, naive.Stats.AncestorProbes, opt.Stats.AncestorProbes)
		}

		// 不断言具体比值，但线性扫描的朴素探测数应为深度的平方量级，
		// 而优化实现恒为线性于候选边数。
		if naive.Stats.AncestorProbes < 50*51/2 {
			t.Fatalf("bulk=%d: naive probes %d should reflect linear ancestor scans",
				bulk, naive.Stats.AncestorProbes)
		}
	}
}

// TestResultSizeIndependentOfUnrelatedGraphGrowth 加入大量与起点不连通的
// 对象与链接后，遍历结果与统计完全不变，证明开销不随总图规模增长。
func TestResultSizeIndependentOfUnrelatedGraphGrowth(t *testing.T) {
	base := buildChainWithBulk(t, 10, 0).loadSnapshot()
	big := buildChainWithBulk(t, 10, 20000).loadSnapshot()
	req := TraversalRequest{Start: "n0", Directions: dirs(DirOutbound), MaxDepth: 10}

	r1, err := TraverseSnapshot(base, req)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := TraverseSnapshot(big, req)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Stats != r2.Stats {
		t.Fatalf("stats must be independent of unrelated graph size:\n %#v\n %#v", r1.Stats, r2.Stats)
	}
	if len(r1.Paths) != len(r2.Paths) {
		t.Fatalf("path count must not depend on unrelated graph size")
	}
}

// buildChainWithBulk 构建 n0->n1->...->n(depth) 的链，
// 外加 bulk 个彼此全连通于独立组件中的无关对象/链接（允许自环和平行链接）。
func buildChainWithBulk(t *testing.T, depth, bulk int) *Graph {
	t.Helper()
	g := NewGraph()
	mut := Mutation{AddLinkTypes: []LinkTypeID{tRel}}
	for i := 0; i <= depth; i++ {
		mut.AddObjects = append(mut.AddObjects, ObjectID(fmt.Sprintf("n%d", i)))
	}
	for i := 0; i < depth; i++ {
		mut.AddLinks = append(mut.AddLinks, Link{
			ID:     LinkID(fmt.Sprintf("e%d", i)),
			Type:   tRel,
			Source: ObjectID(fmt.Sprintf("n%d", i)),
			Target: ObjectID(fmt.Sprintf("n%d", i+1)),
		})
	}
	for i := 0; i < bulk; i++ {
		id := ObjectID(fmt.Sprintf("bulk%d", i))
		mut.AddObjects = append(mut.AddObjects, id)
		mut.AddLinks = append(mut.AddLinks, Link{
			ID:     LinkID(fmt.Sprintf("bulkself%d", i)),
			Type:   tRel,
			Source: id,
			Target: id,
		})
		if i > 0 {
			prev := ObjectID(fmt.Sprintf("bulk%d", i-1))
			mut.AddLinks = append(mut.AddLinks,
				Link{ID: LinkID(fmt.Sprintf("bulkpa%d", i)), Type: tRel, Source: prev, Target: id},
				Link{ID: LinkID(fmt.Sprintf("bulkpb%d", i)), Type: tRel, Source: id, Target: prev},
			)
		}
	}
	if _, err := g.Batch(mut); err != nil {
		t.Fatalf("batch setup: %v", err)
	}
	return g
}

// TestProbeCounterPerEdgeContract 对若干手工场景直接断言
// AncestorProbes == CandidateEdges，作为计数器契约的细粒度验证。
func TestProbeCounterPerEdgeContract(t *testing.T) {
	g := NewGraph()
	mustAddObjects(t, g, "A", "B", "C")
	mustAddLinkTypes(t, g, tRel)
	mustAddLink(t, g, "self", tRel, "A", "A")
	mustAddLink(t, g, "p1", tRel, "A", "B")
	mustAddLink(t, g, "p2", tRel, "A", "B")
	mustAddLink(t, g, "bc", tRel, "B", "C")

	res, err := NewService(g, nil).Traverse(context.Background(), TraversalRequest{
		Start: "A", Directions: dirs(DirOutbound), MaxDepth: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	// A 的候选：self,p1,p2 = 3；B（经 p1 与 p2 各一次）每次候选 bc = 1。
	wantCandidates := 3 + 2
	if res.Stats.CandidateEdges != wantCandidates {
		t.Fatalf("candidate edges: got %d want %d", res.Stats.CandidateEdges, wantCandidates)
	}
	if res.Stats.AncestorProbes != wantCandidates || res.Stats.AncestorChecks != wantCandidates {
		t.Fatalf("checks/probes must both equal %d, got checks=%d probes=%d",
			wantCandidates, res.Stats.AncestorChecks, res.Stats.AncestorProbes)
	}
}
