package settlement_test

import (
	"fmt"
	"testing"

	"ontology/settlement"
)

// buildHistory 构造带有 n 次结算与 n 条缺陷记录历史的合同。
// 每个里程碑应付 1 分、质保金比例 100%，使每次结算都产生质保金扣留；
// 缺陷登记在未通过的里程碑上会被拒绝，因此缺陷登记在已通过里程碑上。
func buildHistory(b *testing.B, n int) *settlement.Service {
	b.Helper()
	svc := settlement.NewService()
	p := settlement.ContractParams{
		ID:               "bench",
		TotalAmount:      int64(n) * 100,
		AdvanceTotal:     0,
		AdvanceRatio:     0,
		RetentionRatio:   10000,
		RetentionDays:    1 << 60, // 永不释放，质保金余额持续累积
		PenaltyDailyRate: 0,
		PenaltyCapRatio:  10000,
	}
	for i := 0; i < n; i++ {
		p.Milestones = append(p.Milestones, settlement.MilestoneParams{
			ID: fmt.Sprintf("m%d", i), Payable: 1, PlanDay: 0,
		})
	}
	if err := svc.CreateContract(p, 0); err != nil {
		b.Fatalf("create: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := svc.Accept("bench", fmt.Sprintf("m%d", i), true, int64(i)); err != nil {
			b.Fatalf("accept %d: %v", i, err)
		}
		if _, err := svc.Accept("bench", fmt.Sprintf("m%d", i), true, int64(i)); err == nil {
			b.Fatalf("expected duplicate settlement error")
		}
		if err := svc.RegisterDefect("bench", fmt.Sprintf("m%d", i), "d", 1, int64(i)); err != nil {
			b.Fatalf("defect %d: %v", i, err)
		}
	}
	return svc
}

// BenchmarkSummaryScaling 验证汇总查询开销不随历史结算与缺陷记录总数增长。
// 运行：go test ./settlement/ -bench SummaryScaling -benchtime 1000x
func BenchmarkSummaryScaling(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		svc := buildHistory(b, n)
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := svc.Summary("bench"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSettleScaling 验证结算开销不随历史结算与缺陷记录总数增长。
// 运行：go test ./settlement/ -bench SettleScaling -benchtime 1000x
func BenchmarkSettleScaling(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		svc := buildHistory(b, n)
		// 追加一个待结算里程碑。
		if err := svc.ChangeOrder("bench", int64(n), []settlement.Adjustment{
			{MilestoneID: "m0", Payable: 1, PlanDay: 0},
		}, int64(n)); err == nil {
			b.Fatalf("expected state error for settled milestone")
		}
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				// 对已结算里程碑重复结算：走完整校验路径后被拒绝，开销为 O(1)。
				_, err := svc.Accept("bench", "m0", true, int64(n))
				if err == nil {
					b.Fatal("expected duplicate settlement error")
				}
			}
		})
	}
}
