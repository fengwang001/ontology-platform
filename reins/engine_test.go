package reins

import (
	"fmt"
	"sync"
	"testing"
)

// baseTreaty 成数 20%，溢额自留 1000、3 线（能力 3000），
// 超赔自留点 500、层限额 1000、恢复 1 次（总能力 2000）。
func baseTreaty() Treaty {
	return Treaty{QuotaSharePercent: 20, SurplusRetention: 1000, SurplusLines: 3,
		XLRetention: 500, XLLimit: 1000, Reinstatements: 1}
}

// netTreaty 构造使赔款净自留恰好等于赔款金额的合约：
// 保额 99、成数 1% 时成数分出为 0，溢额自留额极大故溢额分出为 0。
func netTreaty(xlRet, xlLimit int64, reinst int) Treaty {
	return Treaty{QuotaSharePercent: 1, SurplusRetention: 1 << 40, SurplusLines: 1,
		XLRetention: xlRet, XLLimit: xlLimit, Reinstatements: reinst}
}

func mustEngine(t *testing.T, tr Treaty) *Engine {
	t.Helper()
	e, err := NewEngine(tr)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustRegister(t *testing.T, e *Engine, no string, sumInsured int64, start, end int) Cession {
	t.Helper()
	ces, err := e.RegisterPolicy(no, sumInsured, start, end)
	if err != nil {
		t.Fatalf("RegisterPolicy(%s): %v", no, err)
	}
	return ces
}

func mustAdd(t *testing.T, e *Engine, no, pol, ev string, ts, amt int64) Split {
	t.Helper()
	s, err := e.AddClaim(no, pol, ev, ts, amt)
	if err != nil {
		t.Fatalf("AddClaim(%s): %v", no, err)
	}
	return s
}

func mustCode(t *testing.T, err error, want Code) {
	t.Helper()
	if got := CodeOf(err); got != want {
		t.Fatalf("错误类别 = %d (%v), 期望 %d", got, err, want)
	}
}

func mustSplit(t *testing.T, e *Engine, no string, want Split) {
	t.Helper()
	got, err := e.ClaimResult(no)
	if err != nil {
		t.Fatalf("ClaimResult(%s): %v", no, err)
	}
	if got != want {
		t.Fatalf("ClaimResult(%s) = %+v, 期望 %+v", no, got, want)
	}
}

// 成数后剩余恰等于自留额时不分溢额。
func TestSurplusNotCededWhenRemainderEqualsRetention(t *testing.T) {
	e := mustEngine(t, baseTreaty())
	ces := mustRegister(t, e, "P1", 1250, 0, 10)
	want := Cession{QuotaShare: 250, Surplus: 0, Net: 1000}
	if ces != want {
		t.Fatalf("分出结构 = %+v, 期望 %+v", ces, want)
	}
}

// 溢额需求恰等于最大承保能力可登记，大 1 拒绝。
func TestSurplusCapacityBoundary(t *testing.T) {
	e := mustEngine(t, baseTreaty())
	ces := mustRegister(t, e, "P1", 5000, 0, 10)
	want := Cession{QuotaShare: 1000, Surplus: 3000, Net: 1000}
	if ces != want {
		t.Fatalf("分出结构 = %+v, 期望 %+v", ces, want)
	}
	if _, err := e.RegisterPolicy("P2", 5001, 0, 10); true {
		mustCode(t, err, CodeCapacityExceeded)
	}
	// 被拒绝的保单不得登记。
	_, err := e.AddClaim("C1", "P2", "E1", 100, 100)
	mustCode(t, err, CodePolicyNotFound)
}

// 切分按分向下取整，尾差归净自留。
func TestSplitRemainderGoesToNet(t *testing.T) {
	e := mustEngine(t, baseTreaty())
	mustRegister(t, e, "P1", 5000, 0, 10) // 1000/3000/1000
	mustAdd(t, e, "C1", "P1", "E1", 100, 1001)
	// 1001*1000/5000=200(余), 1001*3000/5000=600(余), 净自留=1001-200-600=201
	mustSplit(t, e, "C1", Split{QuotaShare: 200, Surplus: 600, XLRecover: 0, FinalNet: 201})
}

// 同一事故多张保单净自留聚合：恰等于自留点不触发，大 1 触发。
func TestEventAggregationAtAndAboveRetention(t *testing.T) {
	newEngine := func() *Engine {
		e := mustEngine(t, netTreaty(50, 1000, 0))
		mustRegister(t, e, "P1", 99, 0, 10)
		mustRegister(t, e, "P2", 99, 0, 10)
		return e
	}
	e1 := newEngine()
	mustAdd(t, e1, "C1", "P1", "E1", 100, 30)
	mustAdd(t, e1, "C2", "P2", "E1", 100, 20) // 聚合 50，恰等于自留点
	mustSplit(t, e1, "C1", Split{XLRecover: 0, FinalNet: 30})
	mustSplit(t, e1, "C2", Split{XLRecover: 0, FinalNet: 20})

	e2 := newEngine()
	mustAdd(t, e2, "C1", "P1", "E1", 100, 30)
	mustAdd(t, e2, "C2", "P2", "E1", 100, 21) // 聚合 51，大 1
	// 层承担 1 分，比例分摊后尾差归赔款号字典序最小者 C1。
	mustSplit(t, e2, "C1", Split{XLRecover: 1, FinalNet: 29})
	mustSplit(t, e2, "C2", Split{XLRecover: 0, FinalNet: 21})
}

// 层能力在某事故中间耗尽时，该事故只获部分承担。
func TestLayerCapacityExhaustedMidEvent(t *testing.T) {
	e := mustEngine(t, netTreaty(0, 100, 0)) // 总能力 100
	mustRegister(t, e, "P1", 99, 0, 10)
	mustAdd(t, e, "C1", "P1", "E1", 100, 60)
	mustAdd(t, e, "C2", "P1", "E2", 200, 70)
	mustAdd(t, e, "C3", "P1", "E3", 300, 50)
	mustSplit(t, e, "C1", Split{XLRecover: 60, FinalNet: 0})
	mustSplit(t, e, "C2", Split{XLRecover: 40, FinalNet: 30}) // 能力只剩 40
	mustSplit(t, e, "C3", Split{XLRecover: 0, FinalNet: 50})  // 能力耗尽
}

// 事故时刻相同按事故编号字典序消耗能力。
func TestSameTimeEventsOrderedByID(t *testing.T) {
	e := mustEngine(t, netTreaty(0, 100, 0))
	mustRegister(t, e, "P1", 99, 0, 10)
	mustAdd(t, e, "C1", "P1", "B", 100, 60) // 先到达但编号靠后
	mustAdd(t, e, "C2", "P1", "A", 100, 60)
	mustSplit(t, e, "C2", Split{XLRecover: 60, FinalNet: 0}) // A 先获赔
	mustSplit(t, e, "C1", Split{XLRecover: 40, FinalNet: 20})
}

// 晚报的早事故插入后，此前已获赔的事故失去层承担。
func TestLateInsertDisplacesEarlierPaidEvent(t *testing.T) {
	e := mustEngine(t, netTreaty(0, 90, 0)) // 总能力 90
	mustRegister(t, e, "P1", 99, 0, 10)
	mustAdd(t, e, "C2", "P1", "E2", 200, 90)
	mustSplit(t, e, "C2", Split{XLRecover: 90, FinalNet: 0})
	mustAdd(t, e, "C1", "P1", "E1", 100, 90) // 晚报的更早事故
	mustSplit(t, e, "C1", Split{XLRecover: 90, FinalNet: 0})
	mustSplit(t, e, "C2", Split{XLRecover: 0, FinalNet: 90}) // 失去层承担
}

// 同一事故追加赔款使其跨过自留点。
func TestAdditionalClaimCrossesRetention(t *testing.T) {
	e := mustEngine(t, netTreaty(100, 1000, 0))
	mustRegister(t, e, "P1", 99, 0, 10)
	mustAdd(t, e, "C1", "P1", "E1", 100, 99) // 99 <= 100 不触发
	mustSplit(t, e, "C1", Split{XLRecover: 0, FinalNet: 99})
	mustAdd(t, e, "C2", "P1", "E1", 100, 2) // 聚合 101，触发 1
	// 层承担 1 分，尾差归赔款号字典序最小者 C1。
	mustSplit(t, e, "C1", Split{XLRecover: 1, FinalNet: 98})
	mustSplit(t, e, "C2", Split{XLRecover: 0, FinalNet: 2})
}

// 撤销赔款后的结果与该赔款从未存在时一致。
func TestRemoveEquivalenceToNeverExisted(t *testing.T) {
	tr := netTreaty(10, 50, 1) // 总能力 100
	build := func(withRemoved bool) *Engine {
		e := mustEngine(t, tr)
		mustRegister(t, e, "P1", 99, 0, 10)
		mustRegister(t, e, "P2", 99, 0, 10)
		mustAdd(t, e, "C1", "P1", "E1", 100, 50)
		if withRemoved {
			mustAdd(t, e, "C2", "P2", "E1", 100, 30)
		}
		mustAdd(t, e, "C3", "P1", "E2", 200, 40)
		if withRemoved {
			mustAdd(t, e, "C4", "P2", "E3", 50, 60)
		}
		mustAdd(t, e, "C5", "P2", "E2", 200, 10)
		return e
	}
	a := build(true)
	if err := a.RemoveClaim("C2"); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveClaim("C4"); err != nil {
		t.Fatal(err)
	}
	b := build(false)
	for _, no := range []string{"C1", "C3", "C5"} {
		ga, err := a.ClaimResult(no)
		if err != nil {
			t.Fatal(err)
		}
		gb, err := b.ClaimResult(no)
		if err != nil {
			t.Fatal(err)
		}
		if ga != gb {
			t.Fatalf("%s: 撤销后 %+v != 从未存在 %+v", no, ga, gb)
		}
	}
	if _, err := a.ClaimResult("C2"); true {
		mustCode(t, err, CodeClaimNotFound)
	}
	if err := a.RemoveClaim("C2"); true {
		mustCode(t, err, CodeClaimNotFound)
	}
}

// 拒绝次序逐对验证：参数非法 > 保单不存在 > 保单重复 > 超出承保能力 >
// 赔款已存在 > 赔款不存在 > 事故未承保；被拒绝的操作不得改变任何状态。
func TestRejectionOrder(t *testing.T) {
	for _, tr := range []Treaty{
		{QuotaSharePercent: 0, SurplusLines: 1},
		{QuotaSharePercent: 100, SurplusLines: 1},
		{QuotaSharePercent: 50, SurplusLines: 0},
		{QuotaSharePercent: 50, SurplusLines: 1, Reinstatements: -1},
	} {
		if _, err := NewEngine(tr); true {
			mustCode(t, err, CodeInvalidParam)
		}
	}

	e := mustEngine(t, baseTreaty())
	mustRegister(t, e, "P1", 5000, 0, 10)

	// 参数非法 > 保单不存在：赔款号存在与否、保单存在与否都让位于参数非法。
	_, err := e.AddClaim("X1", "NOPE", "E1", -1, 100)
	mustCode(t, err, CodeInvalidParam)
	_, err = e.AddClaim("X1", "NOPE", "E1", 100, 0)
	mustCode(t, err, CodeInvalidParam)
	// 保单不存在 > 赔款已存在。
	mustAdd(t, e, "C1", "P1", "E1", 100, 100)
	_, err = e.AddClaim("C1", "NOPE", "E1", 100, 100)
	mustCode(t, err, CodePolicyNotFound)
	// 参数非法（金额超保额）> 赔款已存在。
	_, err = e.AddClaim("C1", "P1", "E1", 100, 5001)
	mustCode(t, err, CodeInvalidParam)
	// 赔款已存在 > 事故未承保。
	_, err = e.AddClaim("C1", "P1", "E1", 864000*10, 100)
	mustCode(t, err, CodeClaimExists)
	// 事故未承保：承保区间右端不包含。
	_, err = e.AddClaim("C2", "P1", "E1", 864000*10, 100)
	mustCode(t, err, CodeAccidentNotCovered)
	mustAdd(t, e, "C2", "P1", "E1", 863999, 100) // 区间最后一天内
	// 参数非法 > 保单重复。
	_, err = e.RegisterPolicy("P1", 0, 0, 10)
	mustCode(t, err, CodeInvalidParam)
	// 保单重复 > 超出承保能力。
	_, err = e.RegisterPolicy("P1", 5001, 0, 10)
	mustCode(t, err, CodePolicyDuplicate)
	// 超出承保能力；被拒绝的保单不得登记。
	_, err = e.RegisterPolicy("P2", 5001, 0, 10)
	mustCode(t, err, CodeCapacityExceeded)
	_, err = e.AddClaim("Z1", "P2", "E1", 100, 100)
	mustCode(t, err, CodePolicyNotFound)
	// 参数非法：承保区间右端不大于左端。
	_, err = e.RegisterPolicy("P3", 100, 10, 5)
	mustCode(t, err, CodeInvalidParam)
	// 赔款不存在。
	mustCode(t, e.RemoveClaim("NOPE"), CodeClaimNotFound)
	_, err = e.ClaimResult("NOPE")
	mustCode(t, err, CodeClaimNotFound)
	// 被拒绝的操作不得改变既有归属。
	mustSplit(t, e, "C1", Split{QuotaShare: 20, Surplus: 60, XLRecover: 0, FinalNet: 20})
}

// 晚报插入引起的重算范围限于事故时刻不早于插入点的事故。
func TestRecomputeScopeLimitedToInsertionPoint(t *testing.T) {
	e := mustEngine(t, netTreaty(0, 1000, 10))
	mustRegister(t, e, "P1", 99, 0, 10)
	for i, ts := range []int64{100, 200, 300, 400, 500} {
		mustAdd(t, e, fmt.Sprintf("C%d", i), "P1", fmt.Sprintf("E%d", i), ts, 10)
	}
	assertDelta := func(want int64, op func()) {
		t.Helper()
		before := e.Stats().RecomputedEvents
		op()
		if got := e.Stats().RecomputedEvents - before; got != want {
			t.Fatalf("重算事故数 = %d, 期望 %d", got, want)
		}
	}
	// 晚报早事故插到最前：重算全部 6 个事故。
	assertDelta(6, func() { mustAdd(t, e, "L1", "P1", "EL", 50, 10) })
	// 追加到最前的事故：从事故下标 0 起重算 6 个。
	assertDelta(6, func() { mustAdd(t, e, "L2", "P1", "EL", 50, 10) })
	// 末尾新事故：只重算它自己。
	assertDelta(1, func() { mustAdd(t, e, "L3", "P1", "EZ", 900, 10) })
	// 撤销中间事故 E2（下标 3，共 7 个事故）的唯一赔款：
	// 事故消失，只重算其后的 3 个事故。
	assertDelta(3, func() {
		if err := e.RemoveClaim("C2"); err != nil {
			t.Fatal(err)
		}
	})
}

// 并发录入与某串行顺序等价：最终结果只取决于赔款集合。
func TestConcurrentAddsEqualSerial(t *testing.T) {
	tr := netTreaty(10, 100, 3)
	e := mustEngine(t, tr)
	for i := 0; i < 4; i++ {
		mustRegister(t, e, fmt.Sprintf("P%d", i), 99, 0, 365)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				n := g*25 + i
				no := fmt.Sprintf("C%d", n)
				_, err := e.AddClaim(no, fmt.Sprintf("P%d", n%4), fmt.Sprintf("E%d", n%10),
					int64(1000+n), int64(1+n%50))
				if err != nil {
					t.Errorf("AddClaim(%s): %v", no, err)
				}
			}
		}(g)
	}
	wg.Wait()
	s := mustEngine(t, tr)
	for i := 0; i < 4; i++ {
		mustRegister(t, s, fmt.Sprintf("P%d", i), 99, 0, 365)
	}
	for n := 0; n < 200; n++ {
		mustAdd(t, s, fmt.Sprintf("C%d", n), fmt.Sprintf("P%d", n%4), fmt.Sprintf("E%d", n%10),
			int64(1000+n), int64(1+n%50))
	}
	for n := 0; n < 200; n++ {
		no := fmt.Sprintf("C%d", n)
		gc, err := e.ClaimResult(no)
		if err != nil {
			t.Fatal(err)
		}
		gs, err := s.ClaimResult(no)
		if err != nil {
			t.Fatal(err)
		}
		if gc != gs {
			t.Fatalf("%s: 并发 %+v != 串行 %+v", no, gc, gs)
		}
	}
}

// 登记保单的开销不随已登记保单总数增长（map 插入）。
func BenchmarkRegisterPolicy(b *testing.B) {
	e, err := NewEngine(baseTreaty())
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		if _, err := e.RegisterPolicy(fmt.Sprintf("P%d", i), 5000, 0, 10); err != nil {
			b.Fatal(err)
		}
	}
}

// 切分一笔赔款的成数与溢额部分的开销不随已登记保单总数增长：
// 预登记 10000 张保单后，逐笔追加时刻递增的赔款（重算范围恒为 1）。
func BenchmarkAddClaimSplit(b *testing.B) {
	e, err := NewEngine(baseTreaty())
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		if _, err := e.RegisterPolicy(fmt.Sprintf("P%d", i), 5000, 0, 100000); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.AddClaim(fmt.Sprintf("C%d", i), "P9999", fmt.Sprintf("E%d", i), int64(i), 100); err != nil {
			b.Fatal(err)
		}
	}
}
