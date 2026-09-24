package ontology

import "testing"

// 10 节点各 200 虚拟节点时，十万个 key 下每个节点承担的比例
// 必须落在 [0.5/10, 2.0/10]。key 生成方式固定，结果确定。
func TestBalanceWithManyVnodes(t *testing.T) {
	keys := GenerateKeys(100000)
	r := buildRing(t, 10, 200)
	dist := distributionOf(t, r, keys)

	if len(dist) != 10 {
		t.Fatalf("got %d nodes in distribution, want 10", len(dist))
	}
	for i := 0; i < 10; i++ {
		ratio := float64(dist[nodeID(i)]) / float64(len(keys))
		if lo, hi := 0.5/10.0, 2.0/10.0; ratio < lo || ratio > hi {
			t.Fatalf("node %d ratio %v out of [%v, %v]", i, ratio, lo, hi)
		}
	}
	t.Logf("max/min ratio with 200 vnodes = %v", maxMinRatio(dist))
}

// vnodes=1 时允许失衡；断言其最大/最小比值显著大于 vnodes=200 时，
// 以此证明虚拟节点确实改善了均衡度。
func TestVnodesActuallyImproveBalance(t *testing.T) {
	keys := GenerateKeys(100000)

	sparse := buildRing(t, 10, 1)
	sparseRatio := maxMinRatio(distributionOf(t, sparse, keys))

	dense := buildRing(t, 10, 200)
	denseRatio := maxMinRatio(distributionOf(t, dense, keys))

	t.Logf("max/min ratio: vnodes=1 -> %v, vnodes=200 -> %v", sparseRatio, denseRatio)
	if sparseRatio <= denseRatio {
		t.Fatalf("vnodes=1 max/min %v not worse than vnodes=200 %v", sparseRatio, denseRatio)
	}
}
