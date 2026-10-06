package loto_test

import (
	"fmt"
	"testing"

	"ontology/loto"
)

type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

func mustOKT(t fataler, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", ctx, err)
	}
}

// buildScaleWorld：
//   - history 张"已完成"历史票（即使涉及 hot 设备也已离开 activeByDevice，且锁全部摘除）；
//   - 固定保留 1 张生效中的活跃票 ACT 占用 hot 设备并挂 2 个作业人员的锁。
//
// 这样历史票数两档（5k/50k）下，送电与批准冲突判定面对的"活跃规模"完全相同。
func buildScaleWorld(tb fataler, history int) (*loto.System, int64) {
	s := loto.New()
	mustOKT(tb, s.RegisterPerson("ap", loto.RoleApplicant), "ap")
	mustOKT(tb, s.RegisterPerson("a1", loto.RoleApprover), "a1")
	mustOKT(tb, s.RegisterPerson("a2", loto.RoleApprover), "a2")
	mustOKT(tb, s.RegisterPerson("w1", loto.RoleWorker), "w1")
	mustOKT(tb, s.RegisterPerson("w2", loto.RoleWorker), "w2")
	mustOKT(tb, s.RegisterPerson("v"), "v")
	mustOKT(tb, s.RegisterDevice("hot", []string{"hp1", "hp2"}), "hot")
	mustOKT(tb, s.RegisterDevice("cold", []string{"cp1"}), "cold")

	var now int64
	for i := 0; i < history; i++ {
		id := fmt.Sprintf("H%06d", i)
		// 历史票用各自极早且互不相交的窗口，普通票一人批准。
		st := now
		en := st + 2
		mustOKT(tb, s.Apply(id, "ap", []string{"cold"}, loto.WorkNormal, st, en, []string{"w1"}), "hist apply")
		mustOKT(tb, s.Approve(id, "a1", st), "hist approve")
		mustOKT(tb, s.PlaceLock(id, "w1", "cp1", st), "hist lock")
		mustOKT(tb, s.Verify(id, "v", st), "hist verify")
		mustOKT(tb, s.StartWork(id, "ap", st), "hist start")
		mustOKT(tb, s.Enter(id, "w1", st), "hist enter")
		mustOKT(tb, s.Leave(id, "w1", st+1), "hist leave")
		mustOKT(tb, s.Complete(id, "ap", st+1), "hist complete")
		mustOKT(tb, s.RemoveLock(id, "w1", "cp1", st+1), "hist unlock")
		now = en
	}

	// 活跃票：高风险（批准时做冲突判定），占用 hot，挂两把锁。
	mustOKT(tb, s.Apply("ACT", "ap", []string{"hot"}, loto.WorkHighRisk, now, now+1_000_000, []string{"w1", "w2"}), "act apply")
	mustOKT(tb, s.Approve("ACT", "a1", now), "act approve 1")
	mustOKT(tb, s.Approve("ACT", "a2", now), "act approve 2")
	mustOKT(tb, s.PlaceLock("ACT", "w1", "hp1", now), "act lock w1 hp1")
	mustOKT(tb, s.PlaceLock("ACT", "w1", "hp2", now), "act lock w1 hp2")
	mustOKT(tb, s.PlaceLock("ACT", "w2", "hp1", now), "act lock w2 hp1")
	mustOKT(tb, s.PlaceLock("ACT", "w2", "hp2", now), "act lock w2 hp2")

	// 准备一张待批的高风险票，用来测量"使票生效那次批准"的冲突判定成本。
	// 与 ACT 窗口相交、设备相交：使票生效的第二次批准必然被判冲突（且可重复，被拒不占名额）。
	mustOKT(tb, s.Apply("PEND", "ap", []string{"hot"}, loto.WorkHighRisk, now, now+10, []string{"w1"}), "pend apply")
	mustOKT(tb, s.Approve("PEND", "a1", now), "pend first approval (does not become effective)")
	return s, now
}

// BenchmarkScaleCompare 以子基准对照两档历史规模：
//
//	CanEnergize 与"生效批准（冲突判定）"的单次开销应近似常数，不随历史票数增长。
//
// 用 -benchtime=10000x 固定迭代次数比较耗时；go test -bench 输出并排 ns/op。
func BenchmarkScaleCompare(b *testing.B) {
	for _, history := range []int{5_000, 50_000} {
		s, base := buildScaleWorld(b, history)
		b.Run(fmt.Sprintf("history=%d/CanEnergize", history), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				ok, _, err := s.CanEnergize("hot", base)
				if err != nil {
					b.Fatal(err)
				}
				if ok {
					b.Fatal("hot must remain non-energizable (active permit + locks)")
				}
			}
		})
		// 冲突判定：反复提交会生效的第二次批准——它一定被冲突拒绝，不消耗名额、不改时钟，
		// 因此可以在同一世界里重复测量，每次都走完整冲突检查。
		b.Run(fmt.Sprintf("history=%d/ApproveConflict", history), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				err := s.Approve("PEND", "a2", base)
				if err == nil {
					b.Fatal("expected conflict rejection (repeatable; rejected approval consumes nothing)")
				}
				if oe, ok := err.(*loto.OpError); !ok || oe.Code != loto.Conflict {
					b.Fatalf("want Conflict, got %v", err)
				}
			}
		})
	}
}

// TestScaleFlatness 用较少固定迭代断言：历史票扩大 10 倍，
// 平均单次判定耗时不应成倍增长（给 3 倍宽松上界，规避 CI 抖动）。
// 真正的"可验证证明"见 design.md 的复杂度分析与 BenchmarkScaleCompare 的两档 ns/op。
func TestScaleFlatness(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	measure := func(history int) (energize, conflict float64) {
		s, _ := buildScaleWorld(t, history)
		const n = 2000
		start := testing.AllocsPerRun(n, func() {
			_, _, _ = s.CanEnergize("hot", 0)
		})
		_ = start
		b1 := testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _, _ = s.CanEnergize("hot", 0)
			}
		})
		b2 := testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = s.Approve("PEND", "a2", 0)
			}
		})
		return float64(b1.NsPerOp()), float64(b2.NsPerOp())
	}
	eSmall, cSmall := measure(1_000)
	eLarge, cLarge := measure(10_000)
	t.Logf("CanEnergize ns/op  history=1k: %.1f  history=10k: %.1f (ratio %.2fx)", eSmall, eLarge, eLarge/eSmall)
	t.Logf("ApproveConflict ns/op history=1k: %.1f  history=10k: %.1f (ratio %.2fx)", cSmall, cLarge, cLarge/cSmall)
	if eLarge > 3*eSmall+1000 || cLarge > 3*cSmall+1000 {
		t.Fatalf("decision cost appears to grow with history size (ratios exceed tolerance)")
	}
}
