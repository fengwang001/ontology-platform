package traversal

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
)

// TestRandomDifferentialAgainstNaive 在大量随机图（含自环、平行链接、
// 不同链接类型、不同方向集合、不同深度）上逐条路径对照优化实现与
// 独立朴素实现；两者必须产出完全相同的路径集合与终态分类。
func TestRandomDifferentialAgainstNaive(t *testing.T) {
	const iterations = 400
	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		g, types, start := buildRandomGraph(t, rng, seed)
		snap := g.loadSnapshot()

		// 随机选取链接类型子集与各自方向。
		dirs := map[LinkTypeID]Direction{}
		for _, ty := range types {
			if rng.Intn(2) == 0 {
				dirs[ty] = Direction(1 + rng.Intn(3))
			}
		}
		if len(dirs) == 0 {
			dirs[types[0]] = Direction(1 + rng.Intn(3))
		}
		maxDepth := 1 + rng.Intn(6)
		req := TraversalRequest{Start: start, Directions: dirs, MaxDepth: maxDepth}

		got, err := TraverseSnapshot(snap, req)
		if err != nil {
			t.Fatalf("seed=%d: %v", seed, err)
		}
		want, err := NaiveTraverse(snap, req)
		if err != nil {
			t.Fatalf("seed=%d naive: %v", seed, err)
		}
		if !resultsEquivalent(got, want) {
			t.Fatalf("seed=%d mismatch:\n got %#v\nwant %#v", seed, shapes(got), shapes(want))
		}
	}
}

// resultsEquivalent 比较两条结果的逐路径内容（节点、链接、方向、终态、
// 环路闭合信息），顺序也必须一致（两套实现采用相同的确定候选顺序）。
func resultsEquivalent(a, b *TraversalResult) bool {
	if len(a.Paths) != len(b.Paths) {
		return false
	}
	for i := range a.Paths {
		pa, pb := a.Paths[i], b.Paths[i]
		if pa.Status != pb.Status || pa.Depth != pb.Depth {
			return false
		}
		if len(pa.Nodes) != len(pb.Nodes) || len(pa.Links) != len(pb.Links) {
			return false
		}
		for j := range pa.Nodes {
			if pa.Nodes[j] != pb.Nodes[j] {
				return false
			}
		}
		for j := range pa.Links {
			la, lb := pa.Links[j], pb.Links[j]
			if la != lb {
				return false
			}
		}
		if (pa.Cycle == nil) != (pb.Cycle == nil) {
			return false
		}
		if pa.Cycle != nil {
			if pa.Cycle.RepeatedObject != pb.Cycle.RepeatedObject ||
				pa.Cycle.AncestorIndex != pb.Cycle.AncestorIndex {
				return false
			}
			if len(pa.Cycle.AncestorSequence) != len(pb.Cycle.AncestorSequence) {
				return false
			}
			for j := range pa.Cycle.AncestorSequence {
				if pa.Cycle.AncestorSequence[j] != pb.Cycle.AncestorSequence[j] {
					return false
				}
			}
		}
	}
	return true
}

// buildRandomGraph 随机构造含自环与平行链接的图，确保至少存在 1 个
// 链接类型，并返回全部类型 ID 与一个保证存在的起点。
func buildRandomGraph(t *testing.T, rng *rand.Rand, seed int64) (*Graph, []LinkTypeID, ObjectID) {
	t.Helper()
	g := NewGraph()
	nNodes := 2 + rng.Intn(14)
	nTypes := 1 + rng.Intn(3)

	types := make([]LinkTypeID, nTypes)
	mut := Mutation{}
	for i := 0; i < nTypes; i++ {
		types[i] = LinkTypeID(fmt.Sprintf("t%d", i))
		mut.AddLinkTypes = append(mut.AddLinkTypes, types[i])
	}
	for i := 0; i < nNodes; i++ {
		mut.AddObjects = append(mut.AddObjects, ObjectID(fmt.Sprintf("o%d", i)))
	}

	linkSeq := 0
	addLink := func(from, to int, ty LinkTypeID) {
		mut.AddLinks = append(mut.AddLinks, Link{
			ID:     LinkID(fmt.Sprintf("s%d-l%d", seed, linkSeq)),
			Type:   ty,
			Source: ObjectID(fmt.Sprintf("o%d", from)),
			Target: ObjectID(fmt.Sprintf("o%d", to)),
		})
		linkSeq++
	}

	// 稀疏到较密的随机边；约 15% 为自环，约 10% 刻意重复同一
	// (类型, 源, 目标) 以制造平行链接。
	nEdges := 1 + rng.Intn(nNodes*3)
	for i := 0; i < nEdges; i++ {
		from := rng.Intn(nNodes)
		to := rng.Intn(nNodes)
		if rng.Intn(100) < 15 {
			to = from // 自环
		}
		ty := types[rng.Intn(nTypes)]
		addLink(from, to, ty)
		if rng.Intn(100) < 10 {
			addLink(from, to, ty) // 平行链接（同类型）
		}
		if rng.Intn(100) < 10 && nTypes > 1 {
			addLink(from, to, types[(indexOfType(types, ty)+1)%nTypes]) // 跨类型平行
		}
	}

	if _, err := g.Batch(mut); err != nil {
		t.Fatalf("seed=%d batch: %v", seed, err)
	}
	return g, types, "o0"
}

func indexOfType(types []LinkTypeID, t LinkTypeID) int {
	for i := range types {
		if types[i] == t {
			return i
		}
	}
	return -1
}

// TestServiceMatchesSnapshot 服务层结果与直接快照遍历一致（包装层无偏移）。
func TestServiceMatchesSnapshot(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	g, types, start := buildRandomGraph(t, rng, 42)
	svc := NewService(g, nil)
	req := TraversalRequest{
		Start:      start,
		Directions: map[LinkTypeID]Direction{types[0]: DirBoth},
		MaxDepth:   4,
	}
	res, err := svc.Traverse(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := TraverseSnapshot(g.loadSnapshot(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !resultsEquivalent(res, pinned) {
		t.Fatalf("service result diverges from snapshot traversal")
	}
}
