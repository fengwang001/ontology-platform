package recall

import (
	"fmt"
	"testing"
	"time"
)

func nanotime() int64 { return time.Now().UnixNano() }

// 两档规模对照，验证两个复杂度承诺：
//  1. 批次有效等级只与"覆盖该药品的未解除召回数"有关：
//     已解除召回数与其他药品召回数放大约 100 倍时，判定耗时基本不变；
//  2. 追回清单只与该召回药品的发放记录有关：
//     其他药品发放记录放大约 100 倍时，清单耗时基本不变。
//
// 运行：go test ./recall/ -run TestScaleComparison -v
// 或标准基准：go test -bench=. -benchmem ./recall/

type scaleFixture struct {
	sys            *System
	targetDrug     string
	targetBatch    string
	targetRecallID string
	otherDrug      string
	// 记录各档规模，便于日志核对。
	released              int
	others                int
	dispensesPerOtherDrug int
	targetBatches         int
	targetDispenses       int
}

func buildScale(t testing.TB, released, otherDrugs, otherBatches, otherDispPerBatch, targetBatches, targetDispPerBatch int) *scaleFixture {
	t.Helper()
	s := New()
	now := int64(0)
	step := func() int64 { now++; return now }

	const targetDrug = "目标药品"
	const otherDrugBase = "其他药品-"
	const targetRecall = "目标召回"

	// 目标药品：多个批次，每个批次若干发放。
	for i := 0; i < targetBatches; i++ {
		batch := fmt.Sprintf("T%05d", i)
		if err := s.Inbound(InboundReq{Now: step(), DrugID: targetDrug,
			BatchID: batch, Quantity: 1_000_000}); err != nil {
			t.Fatal(err)
		}
		for k := 0; k < targetDispPerBatch; k++ {
			if err := s.Dispense(DispenseReq{Now: step(), DrugID: targetDrug, BatchID: batch,
				Location: Warehouse, Patient: fmt.Sprintf("TP%04d", k), Quantity: 1,
				Consent: true}); err != nil {
				t.Fatal(err)
			}
		}
	}

	// 固定 5 条覆盖目标药品的未解除召回（决定有效等级的工作量不变）。
	low := fmt.Sprintf("T%05d", 0)
	high := fmt.Sprintf("T%05d", targetBatches-1)
	for i := 0; i < 5; i++ {
		id := targetRecall
		if i == 0 {
			// 第一条用于追回清单，等级 2，始发时刻很早，覆盖全部目标发放。
			if err := s.RegisterRecall(RegisterRecallReq{Now: step(), RecallID: id,
				DrugID: targetDrug, LotLow: low, LotHigh: high,
				Level: 2, IssueAt: 0}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := s.RegisterRecall(RegisterRecallReq{Now: step(), RecallID: fmt.Sprintf("活跃-%d", i),
			DrugID: targetDrug, LotLow: low, LotHigh: high,
			Level: 3, IssueAt: 0}); err != nil {
			t.Fatal(err)
		}
	}

	// 已解除召回：登记后立刻解除。它们不进入 byDrug 覆盖索引。
	for i := 0; i < released; i++ {
		id := fmt.Sprintf("已解除-%06d", i)
		if err := s.RegisterRecall(RegisterRecallReq{Now: step(), RecallID: id,
			DrugID: targetDrug, LotLow: "A", LotHigh: "Z",
			Level: 1 + (i % 3), IssueAt: 0}); err != nil {
			t.Fatal(err)
		}
		if err := s.ReleaseRecall(ReleaseRecallReq{Now: step(), RecallID: id}); err != nil {
			t.Fatal(err)
		}
	}

	// 其他药品：先入库与发放（此时无召回），再登记它们自己的召回，
	// 产生目标药品查询不应触碰的召回与海量发放记录。
	for d := 0; d < otherDrugs; d++ {
		drug := fmt.Sprintf("%s%03d", otherDrugBase, d)
		for b := 0; b < otherBatches; b++ {
			batch := fmt.Sprintf("O%05d", b)
			if err := s.Inbound(InboundReq{Now: step(), DrugID: drug,
				BatchID: batch, Quantity: 1_000_000}); err != nil {
				t.Fatal(err)
			}
			for k := 0; k < otherDispPerBatch; k++ {
				if err := s.Dispense(DispenseReq{Now: step(), DrugID: drug, BatchID: batch,
					Location: Warehouse, Patient: fmt.Sprintf("OP%06d", k), Quantity: 1,
					Consent: true}); err != nil {
					t.Fatal(err)
				}
			}
		}
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("其他召回-%03d-%d", d, i)
			if err := s.RegisterRecall(RegisterRecallReq{Now: step(), RecallID: id,
				DrugID: drug, LotLow: "A", LotHigh: "Z",
				Level: 1 + (i % 3), IssueAt: 0}); err != nil {
				t.Fatal(err)
			}
		}
	}

	return &scaleFixture{
		sys: s, targetDrug: targetDrug, targetBatch: high,
		targetRecallID: targetRecall, otherDrug: otherDrugBase + "000",
		released: released, others: otherDrugs * otherBatches * otherDispPerBatch,
		dispensesPerOtherDrug: otherBatches * otherDispPerBatch,
		targetBatches:         targetBatches, targetDispenses: targetBatches * targetDispPerBatch,
	}
}

// TestScaleComparison 在小/大两档规模上做计时对照（用测试计时而非 benchmark，
// 保证 CI 中必然执行并打印对比）。
func TestScaleComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("规模对照测试在 -short 下跳过")
	}
	small := buildScale(t,
		20, // 已解除召回
		2,  // 其他药品数
		10, // 每药品批次数
		10, // 每批次发放数
		20, // 目标药品批次数
		10) // 目标每批次发放数
	large := buildScale(t,
		2000, // 已解除召回（100 倍）
		20,   // 其他药品数（10 倍）
		100,  // 每药品批次数（10 倍）
		100,  // 每批次发放数（10 倍）-> 其他发放记录 1000 倍
		20,
		10)

	const iterations = 2000

	measure := func(name string, f func()) float64 {
		// 预热
		for i := 0; i < 50; i++ {
			f()
		}
		start := nanotime()
		for i := 0; i < iterations; i++ {
			f()
		}
		cost := float64(nanotime()-start) / float64(iterations)
		t.Logf("%-28s 每次操作 %.1f ns", name, cost)
		return cost
	}

	t.Log("---- 小规模：已解除召回=20, 其他药品召回=6, 其他药品发放记录=",
		small.others, " 目标发放=", small.targetDispenses, "----")
	t.Log("---- 大规模：已解除召回=2000, 其他药品召回=60, 其他药品发放记录=",
		large.others, " 目标发放=", large.targetDispenses, "----")

	se := measure("小档 有效等级判定", func() {
		_, _ = small.sys.recs.effective(small.targetDrug, small.targetBatch)
	})
	le := measure("大档 有效等级判定", func() {
		_, _ = large.sys.recs.effective(large.targetDrug, large.targetBatch)
	})
	sr := measure("小档 追回清单", func() {
		_, _ = small.sys.RecoveryList(RecoveryReq{Now: 1 << 40, RecallID: small.targetRecallID})
	})
	lr := measure("大档 追回清单", func() {
		_, _ = large.sys.RecoveryList(RecoveryReq{Now: 1 << 40, RecallID: large.targetRecallID})
	})

	// 正确性兜底：两档追回清单条目数相同（目标药品工作量完全一致）。
	sl, err := small.sys.RecoveryList(RecoveryReq{Now: 1 << 40, RecallID: small.targetRecallID})
	if err != nil {
		t.Fatal(err)
	}
	ll, err := large.sys.RecoveryList(RecoveryReq{Now: 1 << 40, RecallID: large.targetRecallID})
	if err != nil {
		t.Fatal(err)
	}
	if len(sl.Items) != len(ll.Items) {
		t.Fatalf("两档清单条目应相同: %d vs %d", len(sl.Items), len(ll.Items))
	}

	// 判定耗时只与未解除召回数有关：放大 100 倍噪声，单次耗时不应超过 3 倍
	// （留出 GC 与机器抖动余量；期望接近 1.0）。
	ratio := le / se
	t.Logf("有效等级判定 大/小 耗时比 = %.2f（期望≈1，硬上限 3）", ratio)
	if ratio > 3.0 {
		t.Fatalf("有效等级判定疑似随已解除/其他药品召回增长: 比值 %.2f", ratio)
	}
	// 追回清单耗时只与目标药品发放有关：其他药品发放放大约千倍，
	// 单次耗时不应超过 3 倍。
	ratioR := lr / sr
	t.Logf("追回清单 大/小 耗时比 = %.2f（期望≈1，硬上限 3）", ratioR)
	if ratioR > 3.0 {
		t.Fatalf("追回清单疑似随其他药品发放增长: 比值 %.2f", ratioR)
	}
}

// ---- 标准 Go benchmark，可用 -bench 做更精细的两档对照 ----

func BenchmarkEffectiveLevelSmall(b *testing.B) {
	f := buildScale(b, 20, 2, 10, 10, 20, 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = f.sys.recs.effective(f.targetDrug, f.targetBatch)
	}
}

func BenchmarkEffectiveLevelLarge(b *testing.B) {
	f := buildScale(b, 2000, 20, 100, 100, 20, 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = f.sys.recs.effective(f.targetDrug, f.targetBatch)
	}
}

func BenchmarkRecoverySmall(b *testing.B) {
	f := buildScale(b, 20, 2, 10, 10, 20, 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = f.sys.RecoveryList(RecoveryReq{Now: 1 << 40, RecallID: f.targetRecallID})
	}
}

func BenchmarkRecoveryLarge(b *testing.B) {
	f := buildScale(b, 2000, 20, 100, 100, 20, 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = f.sys.RecoveryList(RecoveryReq{Now: 1 << 40, RecallID: f.targetRecallID})
	}
}
