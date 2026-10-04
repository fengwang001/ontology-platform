package replen

import (
	"fmt"
	"testing"
)

// touchedTrial 构造 nLoc 个同 SKU 库位，并让除目标外的库位各持有一条未完成任务，
// 随后在目标库位执行 Pick / Demand，读取增量维护路径的 touched。
func touchedTrial(t *testing.T, nLocs int) (pickTouched, demandTaskTouched, maxOpenTasks int) {
	t.Helper()
	e := New()
	target := "TGT"
	// 目标库位：min=10 max=1000 cap=2000 c=1，便于精细控制水位。
	if err := e.AddSlot(target, simSKU, 10, 1000, 2000, 1, 11); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nLocs-1; i++ {
		loc := fmt.Sprintf("B%05d", i)
		// min=10 onHand=11：建位不触发，之后 Pick 1 到临界建常规任务。
		if err := e.AddSlot(loc, simSKU, 10, 1000, 2000, 1, 11); err != nil {
			t.Fatal(err)
		}
	}
	// 单一储备池（全 SKU 共用），量足够大但不超过 1e12。
	for i := 0; i < 1000; i++ {
		if err := e.AddReserve(simSKU, 1_000_000_000); err != nil {
			t.Fatal(err)
		}
	}
	// 让每个背景库位持有一条未完成常规任务（同 SKU 总任务数 = nLocs-1）。
	for i := 0; i < nLocs-1; i++ {
		loc := fmt.Sprintf("B%05d", i)
		if err := e.Pick(loc, 1); err != nil {
			t.Fatal(err)
		}
	}

	// Pick 及其后的常规检查：目标降到 eff 恰等 min，新建一条任务。
	if err := e.Pick(target, 1); err != nil { // on 11->10，触发
		t.Fatal(err)
	}
	pickTouched = e.testTouched()

	// Demand：先升级该库位那条常规任务，再视缺口新建紧急任务。
	before := len(e.Tasks()) // 全局未完成任务总数（含所有背景库位）
	_ = before
	openAtTarget := 1
	up, nw, err := e.Demand(target, 1001)
	if err != nil {
		t.Fatal(err)
	}
	if len(up) != 1 || len(nw) != 1 {
		t.Fatalf("nLocs=%d Demand 升级=%v 新建=%v，期望 1/1", nLocs, up, nw)
	}
	demandTaskTouched = e.testTouched() - 1 // 减去库位记录本身，只算任务记录
	maxOpenTasks = openAtTarget
	return
}

// TestTouchedScaling 100 与 10000 两档对照：Pick 路径合计 ≤3，
// Demand 任务记录数 ≤ 该库位未完成任务数+1；且与库位总数、同 SKU 任务总数无关。
func TestTouchedScaling(t *testing.T) {
	pick100, dem100, open100 := touchedTrial(t, 100)
	pick10k, dem10k, open10k := touchedTrial(t, 10000)
	t.Logf("100 档：Pick touched=%d，Demand 任务记录=%d（目标库位未完成=%d）", pick100, dem100, open100)
	t.Logf("10000 档：Pick touched=%d，Demand 任务记录=%d（目标库位未完成=%d）", pick10k, dem10k, open10k)

	if pick100 > 3 || pick10k > 3 {
		t.Fatalf("Pick touched 超限：%d / %d", pick100, pick10k)
	}
	if pick100 != pick10k {
		t.Fatalf("Pick touched 随库位总数变化：%d vs %d（必须为 O(1) 增量维护）", pick100, pick10k)
	}
	if dem100 > open100+1 || dem10k > open10k+1 {
		t.Fatalf("Demand 触碰任务记录超限：%d>%d 或 %d>%d", dem100, open100+1, dem10k, open10k+1)
	}
	if dem100 != dem10k {
		t.Fatalf("Demand 任务记录触碰随库位总数变化：%d vs %d", dem100, dem10k)
	}
}

// TestTouchedNoCreate Pick 后无需新建任务时只触碰库位 1 条。
func TestTouchedNoCreate(t *testing.T) {
	e := New()
	if err := e.AddSlot("K", simSKU, 10, 1000, 2000, 1, 12); err != nil {
		t.Fatal(err)
	}
	if err := e.Pick("K", 1); err != nil { // on=11, eff=11>10 不触发
		t.Fatal(err)
	}
	if got := e.testTouched(); got != 1 {
		t.Fatalf("无新建时 touched=%d 期望 1", got)
	}
}
