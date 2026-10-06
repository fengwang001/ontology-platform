package narledger

import (
	"fmt"
	"testing"
	"time"
)

// complexityReport 供 TestComplexityProof 收集可验证的开销数据。
type complexityReport struct {
	scale           int // 历史批次/单据总数
	dispenseTouched int64
	dispenseNS      int64
	naiveScanNS     int64
	lockNS          int64
}

// setupComplexity 构造规模为 scale 的历史：
//   - 同一药品 scale 个历史批次（大部分已耗尽或已过期，留在批次总账）；
//   - 同一科室 scale 张已结清的历史单据；
//
// 末尾再补一张"活跃"批次与一张"未结清"单据供测量使用。
func setupComplexity(b *testing.T, scale int) *Ledger {
	l := New()
	mustOK(b, l.RegisterGrant("r1", 0, 1_000_000_000), "g1")
	mustOK(b, l.RegisterGrant("r2", 0, 1_000_000_000), "g2")
	// 全部历史批次先在 now=0 入库（效期很近），随后在 now=1 逐批领空：
	// 被领空的批次从 FEFO 堆移除，只留在批次总账中。
	for i := 0; i < scale; i++ {
		mustOK(b, l.Receive(0, "M", fmt.Sprintf("old%06d", i), 2, 800_000_000), "old receive")
	}
	for i := 0; i < scale; i++ {
		mustOK(b, l.Receive(0, "M2", fmt.Sprintf("hd%06d", i), 1, 900_000_000+int64(i)), "hist receive")
	}
	// M2 全部批次按 FEFO 领出到同一科室 BIGDEPT 并结清：该科室堆积 scale 张历史单据
	for i := 0; i < scale; i++ {
		id := fmt.Sprintf("big-%06d", i)
		now1 := int64(10 + i*2)
		mustOK(b, l.Dispense(now1, id, "BIGDEPT", "doc", "M2", 1, "r1", "r2"), "big disp")
		mustOK(b, l.Settle(now1+1, id, 1, 0, 0), "big settle")
	}
	// M 历史批次在 now=3 领空，分散到不同历史科室，每张立即结清
	for i := 0; i < scale; i++ {
		id := fmt.Sprintf("old-o-%06d", i)
		now1 := int64(10 + scale*2 + i*2)
		mustOK(b, l.Dispense(now1, id, fmt.Sprintf("olddept%06d", i), "doc", "M", 2, "r1", "r2"), "old disp")
		mustOK(b, l.Settle(now1+1, id, 2, 0, 0), "old settle")
	}
	// 当前唯一活跃批次（充足库存、远期效期）
	freshAt := int64(10 + scale*4)
	mustOK(b, l.Receive(freshAt, "M", "fresh", int64(scale)+10, 1_000_000_000), "fresh")
	mustOK(b, l.Receive(freshAt, "M2", "fresh2", int64(scale)+10, 1_000_000_000), "fresh2")
	return l
}

// TestComplexityProof 在小/大两档数据上对照并证明：
//  1. 选批触及节点数不随历史批次数增长（恒为实际分出批次数）；
//  2. 科室锁定判定耗时不随该科室历史单据数增长（堆元素全部已弹出）。
//
// 运行：go test ./narledger/ -run TestComplexityProof -v
func TestComplexityProof(t *testing.T) {
	if testing.Short() {
		t.Skip("复杂度对照")
	}
	scales := []int{1_000, 100_000}
	var reports []complexityReport
	for _, scale := range scales {
		l := setupComplexity(t, scale)
		probeNow := int64(10 + scale*4 + 100)

		// --- 选批开销：领 5 支，实际只从 1 个活跃批次分出 ---
		h := l.stock.heapFor("M")
		heapSize := h.size()
		start := time.Now()
		mustOK(t, l.Dispense(probeNow, "probe", "NEWDEPT", "doc", "M", 5, "r1", "r2"), "probe dispense")
		dispNS := time.Since(start).Nanoseconds()
		touched := l.DispenseTouched()
		if touched != 1 {
			t.Fatalf("选批触及节点=%d，期望恰为实际分出批次数 1（历史批次=%d）", touched, scale)
		}

		// --- 朴素全量扫描选批的对照基线（直接对全部批次排序选可发） ---
		start = time.Now()
		var avail int64
		for _, b := range l.stock.batches {
			if b.drug == "M" && b.qty > 0 && probeNow < b.expireAt {
				avail += b.qty
			}
		}
		naiveNS := time.Since(start).Nanoseconds()
		if avail < 5 {
			t.Fatalf("朴素扫描可用量异常 %d", avail)
		}

		// --- 科室锁定判定：BIGDEPT 有 scale 张已结清历史单，无逾期/差额 ---
		start = time.Now()
		locked, err := l.DeptLocked(probeNow, "BIGDEPT")
		lockNS := time.Since(start).Nanoseconds()
		mustOK(t, err, "lock query")
		if locked {
			t.Fatal("BIGDEPT 不应锁定")
		}
		d := l.depts.get("BIGDEPT")
		if d.dueHeap.Len() != 0 {
			t.Fatalf("期限堆应已清空，残留 %d", d.dueHeap.Len())
		}

		reports = append(reports, complexityReport{
			scale: scale, dispenseTouched: touched, dispenseNS: dispNS,
			naiveScanNS: naiveNS, lockNS: lockNS,
		})
		t.Logf("规模=%6d 历史批次=%6d 活跃堆节点=%d | 选批触及=%d 选批耗时=%dns 朴素全量扫描=%dns | 锁定判定=%dns",
			scale, scale, heapSize, touched, dispNS, naiveNS, lockNS)
	}

	small, big := reports[0], reports[1]
	// 选批触及数与规模无关
	if small.dispenseTouched != big.dispenseTouched {
		t.Fatalf("选批触及数随规模增长: %d -> %d", small.dispenseTouched, big.dispenseTouched)
	}
	// 朴素扫描应明显随规模增长（约两个数量级）
	ratio := float64(big.naiveScanNS) / float64(small.naiveScanNS+1)
	if ratio < 20 {
		t.Fatalf("朴素扫描耗时未随规模增长（ratio=%.1f），对照无效", ratio)
	}
	t.Logf("结论：历史批次扩大 %d 倍，朴素扫描耗时扩大 %.1f 倍，而优化实现选批触及恒为 %d；锁定判定小=%dns 大=%dns",
		big.scale/small.scale, ratio, big.dispenseTouched, small.lockNS, big.lockNS)
}
