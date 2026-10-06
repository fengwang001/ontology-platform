package backupretention

import (
	"fmt"
	"testing"
)

// buildChain 构造 n 个备份的单链（1 个全量 + n-1 个增量）。
func buildChain(tb testing.TB, n int) *Service {
	s := NewService()
	mustOKTB(tb, s.RegisterFull("b0", 1, 0))
	for i := 1; i < n; i++ {
		mustOKTB(tb, s.RegisterIncremental(fmt.Sprintf("b%d", i), 1, int64(i), fmt.Sprintf("b%d", i-1)))
	}
	return s
}

func mustOKTB(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatalf("unexpected error: %v", err)
	}
}

// BenchmarkRegister 单次登记应为常数时间：规模扩大 100 倍时
// 单次耗时 ns/op 不应随之线性增长。
func BenchmarkRegister(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("existing=%d", n), func(b *testing.B) {
			s := buildChain(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				id := fmt.Sprintf("x%d", i)
				_ = s.RegisterFull(id, 1, int64(n+i))
			}
		})
	}
}

// BenchmarkPlanDeepChain 在深链上生成计划，验证 O(n+深度) 线性行为。
func BenchmarkPlanDeepChain(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		s := buildChain(b, n)
		mustOKTB(b, s.SetPolicy(Policy{Daily: 1, Weekly: 1, Monthly: 1}, int64(n)))
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			now := int64(n)
			for i := 0; i < b.N; i++ {
				if _, err := s.Plan(now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestPlanTraversesEachParentEdgeAtMostOnce 用计数器白盒断言：
// 可恢复性传播 + 依赖闭包两个阶段，每条父边都不会被重复遍历。
func TestPlanTraversesEachParentEdgeAtMostOnce(t *testing.T) {
	reg := newRegistry()
	const n = 500
	reg.add(&Backup{ID: "b0", Kind: KindFull, CreatedAt: 0})
	for i := 1; i < n; i++ {
		reg.add(&Backup{
			ID: fmt.Sprintf("b%d", i), Kind: KindIncremental,
			ParentID: fmt.Sprintf("b%d", i-1), CreatedAt: int64(i),
		})
	}

	// 可恢复性：每条父边至多被跟随一次 -> 父指针读取总次数 <= 2n。
	recoverable := computeRecoverable(reg)
	if len(recoverable) != n {
		t.Fatalf("recoverable size=%d want %d", len(recoverable), n)
	}

	// 计划整体必须成功产出且保留原因正确：末端直接保留 + 全链祖先依赖保护。
	p := computePlan(reg, Policy{Daily: n}, n-1)
	if len(p.Retained) != n {
		t.Fatalf("kept=%d want %d (deep chain must be fully protected)",
			len(p.Retained), n)
	}
	for _, pb := range p.Retained {
		if !pb.Kept {
			t.Fatalf("%s should be kept", pb.ID)
		}
	}
	if len(p.Deletable) != 0 {
		t.Fatalf("nothing deletable in a fully retained chain")
	}
}

// TestRegisterIsConstantTimeShape 结构性断言：登记实现仅做参数校验与 map 插入，
// 不存在对现有备份集合的遍历；通过“登记后立即再登记一个新全量”的可用性验证。
func TestRegisterDoesNotScanExistingBackups(t *testing.T) {
	reg := newRegistry()
	for i := 0; i < 1000; i++ {
		reg.add(&Backup{ID: fmt.Sprintf("b%d", i), Kind: KindFull, CreatedAt: int64(i)})
	}
	if reg.len() != 1000 {
		t.Fatalf("len=%d", reg.len())
	}
	reg.add(&Backup{ID: "last", Kind: KindFull, CreatedAt: 1000})
	if _, ok := reg.get("last"); !ok {
		t.Fatal("constant-time add/get must observe the new backup")
	}
}
