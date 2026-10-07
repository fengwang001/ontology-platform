package snapshot

import (
	"fmt"
	"testing"
)

// 9. 规模无关性：查找一条引用的探测次数不随目标块记录数线性增长。
// 通过 countingIndex 暴露的真实探测计数在多个数量级上验证，
// 这是可检查、可复现的证据，而非仅声称“用了 map”。
func TestReferenceLookupScaleIndependent(t *testing.T) {
	sizes := []int{100, 1_000, 10_000, 100_000, 1_000_000}
	type row struct {
		n           int
		avgProbes   float64
		maxProbes   int
		linearBound int // 朴素线性扫描最坏需比较次数（对照）
	}
	var rows []row
	for _, n := range sizes {
		ids := make([]string, n)
		for i := 0; i < n; i++ {
			ids[i] = fmt.Sprintf("id-%07d", i)
		}
		idx := newCountingIndex(ids)
		for _, id := range ids {
			idx.insert(id)
		}
		// 命中与未命中各查 2000 个。
		queries := 2000
		for q := 0; q < queries; q++ {
			idx.Contains(ids[q%n])
			idx.Contains(fmt.Sprintf("missing-%d", q))
		}
		total := idx.TotalProbes()
		avg := float64(total) / float64(queries*2)
		r := row{n: n, avgProbes: avg, maxProbes: idx.MaxProbes(), linearBound: n}
		rows = append(rows, r)
		t.Logf("scale N=%7d avg_probes=%.3f max_probes=%d (naive linear worst=%d)",
			n, avg, idx.MaxProbes(), n)
		// 硬性断言：负载因子 0.5 的开放寻址表期望探测 < 3；
		// 最大探测在百万级数据上也必须远小于 N。
		if avg >= 3.0 {
			t.Fatalf("average probes %.3f not O(1) at N=%d", avg, n)
		}
		if idx.MaxProbes() > 100 {
			t.Fatalf("max probes %d unexpectedly large at N=%d", idx.MaxProbes(), n)
		}
	}
	// 单调性之外的关键断言：N 扩大 1 万倍，平均探测基本不变。
	first, last := rows[0].avgProbes, rows[len(rows)-1].avgProbes
	diff := last - first
	if diff < 0 {
		diff = -diff
	}
	if diff > 1.0 {
		t.Fatalf("avg probes drifted by %.3f across 10^4 scale; lookup cost is scale-dependent", diff)
	}
}
