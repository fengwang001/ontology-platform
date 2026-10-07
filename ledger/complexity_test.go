package ledger

import (
	"fmt"
	"testing"
)

// buildHistory 构造含 historical 个已过期历史批次与 3 个可发批次的药品，
// 并通过一次热身领用把历史批次全部迁出可发堆（一次性摊还成本）。
func buildHistory(t *testing.T, historical int) *System {
	t.Helper()
	s := NewSystem()
	mustOK(t, s.GrantAuth(0, "r1", 0, 1_000_000_000))
	mustOK(t, s.GrantAuth(0, "r2", 0, 1_000_000_000))
	for i := 0; i < historical; i++ {
		mustOK(t, s.Inbound(0, "d", fmt.Sprintf("old%d", i), 1, 50))
	}
	mustOK(t, s.Inbound(0, "d", "a1", 100, 1_000_000_000))
	mustOK(t, s.Inbound(0, "d", "a2", 100, 1_000_000_000))
	mustOK(t, s.Inbound(0, "d", "a3", 100, 1_000_000_000))
	// 热身：迁移全部过期批次（每个批次生命周期内只迁移一次）。
	_, err := s.Withdraw(100, "warm", "k", "app", "d", 1, []string{"r1", "r2"})
	mustOK(t, err)
	return s
}

// 可验证（非计时）证明：领用选批的堆操作次数只与实际分出的批次数有关，
// 与该药品历史批次总数无关。
func TestSelectionOpsIndependentOfHistory(t *testing.T) {
	for _, n := range []int{100, 100_000} {
		s := buildHistory(t, n)
		before := s.Stats()
		// a1=99（热身取走 1）、a2=100、a3=100，领 250 跨 3 个批次，取空 2 个
		lines, err := s.Withdraw(200, "m", "k", "app", "d", 250, []string{"r1", "r2"})
		mustOK(t, err)
		if len(lines) != 3 {
			t.Fatalf("N=%d: 分出 %d 个批次，期望 3", n, len(lines))
		}
		ops := s.Stats().BatchHeapOps - before.BatchHeapOps
		if ops != 2 {
			t.Fatalf("N=%d: 选批堆操作 %d 次，期望恰为 2（取空的批次数）", n, ops)
		}
	}
}

// 可验证（非计时）证明：科室锁定判定不做任何堆操作（纯 O(1)  peek），
// 新领用只产生 1 次单据堆 push，与历史单据总数无关。
func TestLockCheckOpsIndependentOfHistory(t *testing.T) {
	for _, n := range []int{100, 100_000} {
		s := NewSystem()
		mustOK(t, s.GrantAuth(0, "r1", 0, 1_000_000_000))
		mustOK(t, s.GrantAuth(0, "r2", 0, 1_000_000_000))
		mustOK(t, s.Inbound(0, "d", "b", 1_000_000, 1_000_000_000))
		// n 张历史单据，全部即领即结
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("s%d", i)
			mustOK(t, withdraw(s, 1, id, "k", "d", 1))
			mustOK(t, s.Settle(1, id, 1, 0, 0))
		}
		before := s.Stats()
		for i := 0; i < 1000; i++ {
			if s.DeptLocked(1, "k") {
				t.Fatalf("N=%d: 全部结清后不应锁定", n)
			}
		}
		mid := s.Stats()
		if d := mid.SlipHeapOps - before.SlipHeapOps; d != 0 {
			t.Fatalf("N=%d: 1000 次锁定判定产生 %d 次堆操作，期望 0", n, d)
		}
		// 新领用只产生 1 次单据堆 push
		mustOK(t, withdraw(s, 2, "new", "k", "d", 1))
		if d := s.Stats().SlipHeapOps - mid.SlipHeapOps; d != 1 {
			t.Fatalf("N=%d: 领用产生 %d 次单据堆操作，期望 1", n, d)
		}
	}
}

// ---- 基准：小规模与大规模两档对照 ----

func benchmarkWithdrawSelection(b *testing.B, historical int) {
	s := NewSystem()
	_ = s.GrantAuth(0, "r1", 0, 1_000_000_000)
	_ = s.GrantAuth(0, "r2", 0, 1_000_000_000)
	for i := 0; i < historical; i++ {
		_ = s.Inbound(0, "d", fmt.Sprintf("old%d", i), 1, 50)
	}
	_ = s.Inbound(0, "d", "a1", 100, 1_000_000_000)
	_ = s.Inbound(0, "d", "a2", 100, 1_000_000_000)
	_ = s.Inbound(0, "d", "a3", 100, 1_000_000_000)
	// 热身：一次性迁移全部过期历史批次（摊还成本，不计入稳态）。
	if _, err := s.Withdraw(100, "warm", "k", "app", "d", 1, []string{"r1", "r2"}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now := int64(1000 + i)
		id := fmt.Sprintf("s%d", i)
		if _, err := s.Withdraw(now, id, "k", "app", "d", 250, []string{"r1", "r2"}); err != nil {
			b.Fatal(err)
		}
		// 全量退回，恢复库存以便持续测量稳态选批开销
		if err := s.Settle(now, id, 0, 250, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWithdrawSelectionSmall(b *testing.B) { benchmarkWithdrawSelection(b, 100) }
func BenchmarkWithdrawSelectionLarge(b *testing.B) { benchmarkWithdrawSelection(b, 100_000) }

func benchmarkDeptLockCheck(b *testing.B, historical int) {
	s := NewSystem()
	_ = s.GrantAuth(0, "r1", 0, 1_000_000_000)
	_ = s.GrantAuth(0, "r2", 0, 1_000_000_000)
	_ = s.Inbound(0, "d", "b", 1_000_000, 1_000_000_000)
	for i := 0; i < historical; i++ {
		id := fmt.Sprintf("s%d", i)
		if _, err := s.Withdraw(1, id, "k", "app", "d", 1, []string{"r1", "r2"}); err != nil {
			b.Fatal(err)
		}
		if err := s.Settle(1, id, 1, 0, 0); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if s.DeptLocked(1, "k") {
			b.Fatal("不应锁定")
		}
	}
}

func BenchmarkDeptLockCheckSmall(b *testing.B) { benchmarkDeptLockCheck(b, 100) }
func BenchmarkDeptLockCheckLarge(b *testing.B) { benchmarkDeptLockCheck(b, 100_000) }
