package claims

import (
	"fmt"
	"testing"
)

// TestRequestAssignIndependentOfFinalized 结构性证明：
// 已自动通过（终态）的案件从不进入待分配堆，已分配完毕的案件立即出堆，
// 因此堆中只存在“仍有未分配空位”的案件，RequestAssign 只读堆顶。
func TestRequestAssignIndependentOfFinalized(t *testing.T) {
	e, err := NewEngine(Config{LowThreshold: 2, HighThreshold: 5, ReviewTimeout: 1 << 40})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.SetRule("R", "F", 3); err != nil {
		t.Fatalf("SetRule: %v", err)
	}
	if err := e.RegisterReviewer("rv", "b0", LevelOne); err != nil {
		t.Fatalf("RegisterReviewer: %v", err)
	}
	// 大量案件受理即自动通过（终态），不占用待分配堆。
	const finalized = 20000
	for i := 0; i < finalized; i++ {
		if err := e.AcceptCase(fmt.Sprintf("done-%d", i), int64(i), nil, "b1", 1); err != nil {
			t.Fatalf("AcceptCase: %v", err)
		}
	}
	if got := e.PendingLen(); got != 0 {
		t.Fatalf("PendingLen = %d, want 0", got)
	}
	// 单人复核案件分配后立即出堆。
	if err := e.AcceptCase("solo", finalized, []string{"F"}, "b1", 1); err != nil {
		t.Fatalf("AcceptCase: %v", err)
	}
	if got := e.PendingLen(); got != 1 {
		t.Fatalf("PendingLen = %d, want 1", got)
	}
	id, slotIdx, err := e.RequestAssign("rv")
	if err != nil || id != "solo" || slotIdx != 0 {
		t.Fatalf("RequestAssign: %s %d %v", id, slotIdx, err)
	}
	if got := e.PendingLen(); got != 0 {
		t.Fatalf("PendingLen after assign = %d, want 0", got)
	}
}

// BenchmarkRequestAssign 验证申请分配的开销不随已分配/已终态案件数增长：
// 三组子基准分别预置 0 / 1k / 100k 个已终态案件，稳态下每轮执行一次
// 受理 + 申请分配 + 提交，单轮耗时应在三组间基本持平。
// 运行：go test ./claims -bench RequestAssign -benchmem
func BenchmarkRequestAssign(b *testing.B) {
	for _, finalized := range []int{0, 1_000, 100_000} {
		b.Run(fmt.Sprintf("finalized=%d", finalized), func(b *testing.B) {
			e, err := NewEngine(Config{LowThreshold: 2, HighThreshold: 5, ReviewTimeout: 1 << 40})
			if err != nil {
				b.Fatalf("NewEngine: %v", err)
			}
			if err := e.SetRule("R", "F", 3); err != nil {
				b.Fatalf("SetRule: %v", err)
			}
			if err := e.RegisterReviewer("rv", "b0", LevelOne); err != nil {
				b.Fatalf("RegisterReviewer: %v", err)
			}
			for i := 0; i < finalized; i++ {
				if err := e.AcceptCase(fmt.Sprintf("done-%d", i), int64(i), nil, "b1", 1); err != nil {
					b.Fatalf("AcceptCase: %v", err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				id := fmt.Sprintf("c-%d", i)
				if err := e.AcceptCase(id, int64(finalized+i), []string{"F"}, "b1", 1); err != nil {
					b.Fatalf("AcceptCase: %v", err)
				}
				caseID, slotIdx, err := e.RequestAssign("rv")
				if err != nil {
					b.Fatalf("RequestAssign: %v", err)
				}
				if err := e.Submit(caseID, slotIdx, "rv", ConclusionPass); err != nil {
					b.Fatalf("Submit: %v", err)
				}
			}
		})
	}
}
