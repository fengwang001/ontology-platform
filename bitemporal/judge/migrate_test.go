package judge

import (
	"testing"

	"ontology/bitemporal"
)

func TestMigrateSingleHops(t *testing.T) {
	eng := newTestEngine()
	rec := bitemporal.Record{ID: "r",
		Valid:       bi(p(1), p(10)),
		Transaction: bi(p(2), p(8))}

	t.Run("V2降级V1_丢弃有效轴保留事务轴", func(t *testing.T) {
		m, ok := eng.Migrate(rec, "V2", "V1")
		if !ok || m.Result.Verdict != VerdictLossy {
			t.Fatalf("迁移应可完成且为损失性迁移，ok=%v", ok)
		}
		if m.Record.Valid != nil {
			t.Fatal("降级后有效轴必须不存在")
		}
		if m.Record.Transaction == nil ||
			*m.Record.Transaction.Start != 2 || *m.Record.Transaction.End != 8 {
			t.Fatalf("事务轴必须原样保留，得到 %+v", m.Record.Transaction)
		}
		if len(m.LostAxes) != 1 || m.LostAxes[0] != bitemporal.ValidTime {
			t.Fatalf("损失轴必须具名为有效轴，得到 %v", m.LostAxes)
		}
	})

	t.Run("V1升级V2_固定填充有效轴为全时间轴", func(t *testing.T) {
		txOnly := bitemporal.Record{ID: "r", Transaction: bi(p(2), p(8))}
		m, ok := eng.Migrate(txOnly, "V1", "V2")
		if !ok || m.Result.Verdict != VerdictCompatible {
			t.Fatalf("升级应兼容完成，ok=%v verdict=%s", ok, m.Result.Verdict)
		}
		v := m.Record.Valid
		if v == nil || v.Start != nil || v.End != nil {
			t.Fatalf("有效轴必须填充为 (-∞,+∞)，得到 %+v", v)
		}
		if m.FilledAxes[bitemporal.ValidTime] != FillRuleEntireTimeline {
			t.Fatalf("必须记录固定填充规则，得到 %v", m.FilledAxes)
		}
	})

	t.Run("不兼容与被拒绝记录不产出迁移", func(t *testing.T) {
		if _, ok := eng.Migrate(rec, "V2", "V3"); ok {
			t.Fatal("边界语义不兼容时不得产出迁移结果")
		}
		bad := bitemporal.Record{ID: "bad", Valid: bi(p(9), p(1))}
		if _, ok := eng.Migrate(bad, "V2", "V1"); ok {
			t.Fatal("记录不自洽时不得产出迁移结果")
		}
	})

	t.Run("迁移不修改源记录", func(t *testing.T) {
		snap := *rec.Valid
		_, _ = eng.Migrate(rec, "V2", "V1")
		if *rec.Valid != snap || rec.Valid.StartClosed != bitemporal.Closed {
			t.Fatal("迁移过程修改了源记录")
		}
	})
}

// 连续多次迁移路径与直达路径的对照。
func TestPathVsDirect(t *testing.T) {
	eng := newTestEngine()
	rec := bitemporal.Record{ID: "r",
		Valid:       bi(p(1), p(10)),
		Transaction: bi(p(2), p(8))}

	t.Run("先升级再降级V1_V2_V4_与直达等价", func(t *testing.T) {
		// V1(事务) -> V2(双轴，填充有效轴) -> V4(只有效轴，丢事务轴)。
		txOnly := bitemporal.Record{ID: "r", Transaction: bi(p(2), p(8))}
		eq := eng.ComparePathWithDirect(txOnly, []string{"V1", "V2", "V4"})
		if !eq.Equivalent {
			t.Fatalf("单调经过更高轴集合后降级，应与直达等价；差异: %v", eq.Reasons)
		}
		direct, _ := eng.Migrate(txOnly, "V1", "V4")
		path := eng.MigratePath(txOnly, []string{"V1", "V2", "V4"})
		if path.Final.Transaction != nil {
			t.Fatal("最终事务轴应已丢失")
		}
		if !intervalEqualPtr(path.Final.Valid, direct.Record.Valid) {
			t.Fatalf("最终有效轴应与直达一致：path=%v direct=%v",
				path.Final.Valid, direct.Record.Valid)
		}
	})

	t.Run("轴集合与边界语义不变的往返V2_V5_V2_与直达完全一致", func(t *testing.T) {
		// V5 与 V2 记录相同轴集合、采用相同的 [) 边界约定，仅代表写法升级。
		eq := eng.ComparePathWithDirect(rec, []string{"V2", "V5", "V2"})
		if !eq.Equivalent {
			t.Fatalf("边界约定往返应与直达等价；差异: %v", eq.Reasons)
		}
	})

	t.Run("V2到V2O自迁移往返_同版本直达", func(t *testing.T) {
		eq := eng.ComparePathWithDirect(rec, []string{"V2", "V2"})
		if !eq.Equivalent {
			t.Fatalf("同版本往返应等价: %v", eq.Reasons)
		}
	})

	t.Run("先降级再升级V2_V1_V2_额外损失被明确报告而非静默恢复", func(t *testing.T) {
		// 直达 V2->V2 保留全部信息；逐跳物化 V1 会永久擦除有效轴。
		// 这种绕行损失在信息论上不可消除，组件必须显式报告，而不是假装等价，
		// 也不允许在升级跳“恢复出本不应保留的信息”。
		eq := eng.ComparePathWithDirect(rec, []string{"V2", "V1", "V2"})
		if eq.Equivalent {
			t.Fatal("经过不记录有效轴的中间版本必然产生额外损失，不得判为等价")
		}
		path := eng.MigratePath(rec, []string{"V2", "V1", "V2"})
		if !path.OK {
			t.Fatal("路径本身应可逐跳完成")
		}
		if path.Final.Valid == nil || path.Final.Valid.Start != nil {
			t.Fatalf("升级跳只能按固定规则填充全时间轴，不得恢复原 [1,10)，得到 %+v",
				path.Final.Valid)
		}
		if len(path.LostAxes) != 1 || path.LostAxes[0] != bitemporal.ValidTime {
			t.Fatalf("路径应累积报告有效轴损失，得到 %v", path.LostAxes)
		}
		if path.Final.Transaction == nil ||
			*path.Final.Transaction.Start != 2 {
			t.Fatal("事务轴在往返中必须无损保留")
		}
	})

	t.Run("边界不兼容跳阻断路径并定位失败跳", func(t *testing.T) {
		path := eng.MigratePath(rec, []string{"V2", "V3", "V2"})
		if path.OK || path.Failure == nil {
			t.Fatal("V2->V3 不兼容，路径必须在该跳被阻断")
		}
		if path.Failure.SourceVersion != "V2" ||
			path.Failure.TargetVersion != "V3" ||
			path.Failure.Verdict != VerdictIncompatible {
			t.Fatalf("失败跳定位错误: %+v", path.Failure)
		}
	})
}

// 对同一条记录反复执行同一条迁移路径，结果保持不变。
func TestPathRepeatedIdempotence(t *testing.T) {
	eng := newTestEngine()
	txOnly := bitemporal.Record{ID: "r", Transaction: bi(p(2), p(8))}
	chain := []string{"V1", "V2", "V4"}
	first := eng.MigratePath(txOnly, chain)
	for i := 0; i < 50; i++ {
		got := eng.MigratePath(txOnly, chain)
		if !got.OK || !intervalEqualPtr(got.Final.Valid, first.Final.Valid) ||
			!intervalEqualPtr(got.Final.Transaction, first.Final.Transaction) ||
			len(got.LostAxes) != len(first.LostAxes) {
			t.Fatalf("第 %d 次路径迁移结果不稳定", i)
		}
		eq := eng.ComparePathWithDirect(txOnly, chain)
		if !eq.Equivalent {
			t.Fatalf("第 %d 次路径对照不再等价: %v", i, eq.Reasons)
		}
	}
}
