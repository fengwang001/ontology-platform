package ontology

import (
	"errors"
	"fmt"
	"testing"
)

// TestPendingQueueExcludesHistory 结构性证明：等待队列中只含等待分配的
// 案件，已分配完毕与已终态案件即出堆，不随历史增长。
func TestPendingQueueExcludesHistory(t *testing.T) {
	e := mustEngine(t, 10, 20, 1000)
	mustRule(t, e, "RA", "FA", 10)
	mustRule(t, e, "RB", "FB", 10)
	mustReviewer(t, e, "r1", "B2", LevelOne)
	mustReviewer(t, e, "r2", "B2", LevelTwo)

	// 1000 个自动通过的终态案件，从不进入等待队列。
	for i := 0; i < 1000; i++ {
		mustAccept(t, e, fmt.Sprintf("auto%d", i), int64(i), nil, "B1", 1)
	}
	if n := e.PendingCount(); n != 0 {
		t.Fatalf("自动通过案件不应入队，pending=%d", n)
	}

	// 500 个单人复核案件：分配并提交一半，撤回应余下的一半。
	for i := 0; i < 500; i++ {
		mustAccept(t, e, fmt.Sprintf("single%d", i), int64(i), []string{"FA"}, "B1", 1)
	}
	if n := e.PendingCount(); n != 500 {
		t.Fatalf("等待中案件应全部在队，pending=%d", n)
	}
	for i := 0; i < 250; i++ {
		id := fmt.Sprintf("single%d", i)
		mustAssign(t, e, id, 0, "r1")
		mustSubmit(t, e, id, "r1", VerdictPass)
	}
	if n := e.PendingCount(); n != 250 {
		t.Fatalf("已终态案件应出队，pending=%d", n)
	}
	for i := 250; i < 500; i++ {
		if err := e.Withdraw(fmt.Sprintf("single%d", i)); err != nil {
			t.Fatalf("Withdraw: %v", err)
		}
	}
	if n := e.PendingCount(); n != 0 {
		t.Fatalf("撤回案件应出队，pending=%d", n)
	}

	// 双人复核：分配完两个空位的案件即出队，进入仲裁时重新入队。
	mustAccept(t, e, "dual", 9999, []string{"FA", "FB"}, "B1", 1)
	if n := e.PendingCount(); n != 1 {
		t.Fatalf("双人案件等待中应在队，pending=%d", n)
	}
	mustAssign(t, e, "dual", 0, "r1")
	if n := e.PendingCount(); n != 1 {
		t.Fatalf("双人案件尚有空位应仍在队，pending=%d", n)
	}
	mustAssign(t, e, "dual", 1, "r2")
	if n := e.PendingCount(); n != 0 {
		t.Fatalf("空位分配完毕应出队，pending=%d", n)
	}
	mustSubmit(t, e, "dual", "r1", VerdictPass)
	mustSubmit(t, e, "dual", "r2", VerdictReject) // 不一致，进入仲裁重新入队
	if n := e.PendingCount(); n != 1 {
		t.Fatalf("仲裁等待中应重新入队，pending=%d", n)
	}
}

// BenchmarkApplyAssign 验证申请分配的开销不随已分配/已终态案件数增长：
// 改变历史案件数量级，每次申请分配（命中队首但利益冲突被拒，不消耗队列）
// 的耗时应保持平坦。运行：go test -bench ApplyAssign -benchtime=100000x
func BenchmarkApplyAssign(b *testing.B) {
	for _, history := range []int{0, 10000, 100000} {
		b.Run(fmt.Sprintf("history=%d", history), func(b *testing.B) {
			e, err := NewEngine(10, 20, 1<<40)
			if err != nil {
				b.Fatalf("NewEngine: %v", err)
			}
			if err := e.UpsertRule(Rule{ID: "RA", Code: "FA", Score: 15}); err != nil {
				b.Fatalf("UpsertRule: %v", err)
			}
			// 历史包袱：大量已终态（自动通过）案件。
			for i := 0; i < history; i++ {
				if err := e.Accept(fmt.Sprintf("H%d", i), int64(i), nil, "BZ", 1); err != nil {
					b.Fatalf("Accept: %v", err)
				}
			}
			// 一个常驻等待案件与一个永远冲突的申请人。
			if err := e.Accept("W", 0, []string{"FA"}, "B1", 1); err != nil {
				b.Fatalf("Accept: %v", err)
			}
			if err := e.RegisterReviewer("rv", "B1", LevelOne); err != nil {
				b.Fatalf("RegisterReviewer: %v", err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := e.ApplyAssign("rv"); !errors.Is(err, ErrConflict) {
					b.Fatalf("期望利益冲突，实际 %v", err)
				}
			}
		})
	}
}

// BenchmarkAcceptAssignCycle 验证在持续累积已终态历史的情况下，
// 受理 + 申请分配的完整循环开销保持平坦。
func BenchmarkAcceptAssignCycle(b *testing.B) {
	e, err := NewEngine(10, 20, 1<<40)
	if err != nil {
		b.Fatalf("NewEngine: %v", err)
	}
	if err := e.UpsertRule(Rule{ID: "RA", Code: "FA", Score: 15}); err != nil {
		b.Fatalf("UpsertRule: %v", err)
	}
	for i := 0; i < 100000; i++ {
		if err := e.Accept(fmt.Sprintf("H%d", i), int64(i), nil, "BZ", 1); err != nil {
			b.Fatalf("Accept: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rev := fmt.Sprintf("rv%d", i)
		if err := e.RegisterReviewer(rev, "B2", LevelOne); err != nil {
			b.Fatalf("RegisterReviewer: %v", err)
		}
		if err := e.Accept(fmt.Sprintf("C%d", i), int64(i), []string{"FA"}, "B1", 1); err != nil {
			b.Fatalf("Accept: %v", err)
		}
		if _, _, err := e.ApplyAssign(rev); err != nil {
			b.Fatalf("ApplyAssign: %v", err)
		}
	}
}
