package ontology

import (
	"math"
	"testing"
)

// maxMinRatio 返回分布中最大值与最小值之比；最小值为 0 时返回 +Inf。
func maxMinRatio(dist map[string]int) float64 {
	lo, hi := math.MaxInt, 0
	for _, c := range dist {
		if c < lo {
			lo = c
		}
		if c > hi {
			hi = c
		}
	}
	if lo == 0 {
		return math.Inf(1)
	}
	return float64(hi) / float64(lo)
}

// TestBalanceWithVnodes 10 节点各 200 虚拟节点时，
// 十万个 key 中每个节点承担的比例必须落在 [0.5/10, 2.0/10]。
// key 生成方式固定，断言是确定性的。
func TestBalanceWithVnodes(t *testing.T) {
	keys := genKeys(numKeys)
	r := buildRing(t, nodeIDs(10), 200)
	dist := distribution(locateAll(t, r, keys))

	if len(dist) != 10 {
		t.Fatalf("应有 10 个节点承担 key，实际 %d", len(dist))
	}
	for id, c := range dist {
		share := float64(c) / float64(numKeys)
		t.Logf("%s: %d keys (%.4f)", id, c, share)
		if share < 0.5/10 || share > 2.0/10 {
			t.Fatalf("节点 %q 承担比例 %.4f 不在 [0.05, 0.20] 内", id, share)
		}
	}
}

// TestVnodesImproveBalance vnodes=1 时允许失衡；
// 断言其最大最小比值显著大于 vnodes=200 时，证明虚拟节点真的起作用。
func TestVnodesImproveBalance(t *testing.T) {
	keys := genKeys(numKeys)

	r1 := buildRing(t, nodeIDs(10), 1)
	ratio1 := maxMinRatio(distribution(locateAll(t, r1, keys)))

	r200 := buildRing(t, nodeIDs(10), 200)
	ratio200 := maxMinRatio(distribution(locateAll(t, r200, keys)))

	t.Logf("vnodes=1 最大/最小比: %.2f, vnodes=200: %.4f", ratio1, ratio200)
	if !(ratio1 > ratio200) {
		t.Fatalf("vnodes=1 的失衡度 %.2f 应大于 vnodes=200 的 %.4f", ratio1, ratio200)
	}
}
