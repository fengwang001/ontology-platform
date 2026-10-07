package validate

import (
	"context"
	"fmt"
	"testing"
)

// TestSkippedCountIndependentOfHistory 用可验证的方式证明：
// “某次调用跳过了多少钩子”的计算只依赖该次快照中的钩子数量，
// 而不随系统历史上注册/注销总次数增长。
//
// 方法：两个注册表最终存活快照完全相同（同样的分组与 3 个钩子，
// 高优先级首钩拒绝）。注册表 B 在达到相同终态之前额外制造了大量
// 注册+注销“历史噪声”（churn）。若跳过计数依赖历史总量，B 的结果
// 会受影响；实际两者必须逐字节一致。
func TestSkippedCountIndependentOfHistory(t *testing.T) {
	build := func(churn int) *Report[target] {
		var calls []string
		r := mustRegistry(t,
			GroupSpec{Name: "high", Priority: 100, ShortCircuit: true},
			GroupSpec{Name: "low", Priority: 1, ShortCircuit: true},
		)
		// 历史噪声：反复注册并立即注销大量一次性钩子。
		// 这些钩子在终态快照中一个都不存在。
		for i := 0; i < churn; i++ {
			id := fmt.Sprintf("churn-%d", i)
			if err := r.Register(approveHook(id, "low", &calls)); err != nil {
				t.Fatal(err)
			}
			if !r.Unregister(id) {
				t.Fatalf("churn hook %s should unregister", id)
			}
		}
		if err := r.Register(rejectHook("rejector", "high", "no", &calls)); err != nil {
			t.Fatal(err)
		}
		if err := r.Register(approveHook("h1", "low", &calls)); err != nil {
			t.Fatal(err)
		}
		if err := r.Register(approveHook("h2", "low", &calls)); err != nil {
			t.Fatal(err)
		}
		rep, err := r.Validate(context.Background(), target{})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}

	clean := build(0)
	churned := build(5000)

	if got := clean.Snapshot.HookIDs(); len(got) != 3 {
		t.Fatalf("clean snapshot size = %d, want 3", len(got))
	}
	if got := churned.Snapshot.HookIDs(); len(got) != 3 {
		t.Fatalf("churned snapshot must contain only live hooks, got %d", len(got))
	}
	if clean.SkippedHooks() != 2 || churned.SkippedHooks() != 2 {
		t.Fatalf("skipped clean=%d churned=%d, both want 2",
			clean.SkippedHooks(), churned.SkippedHooks())
	}
	if clean.Snapshot.Version != churned.Snapshot.Version-uint64(10000) {
		t.Fatalf("version counters should differ by churn ops; clean=%d churned=%d",
			clean.Snapshot.Version, churned.Snapshot.Version)
	}
	if clean.Basis != churned.Basis {
		t.Fatalf("behavior must be identical regardless of history:\n%s\nvs\n%s",
			clean.Basis, churned.Basis)
	}
}

// BenchmarkValidateSkippedCounting 衡量一次调用的耗时随“快照内钩子数”
// 线性增长、与历史总量无关。用 -benchmem 观察分配：报告只分配与快照大小
// 成比例的记录，不存在随历史增长的墓碑结构。
func BenchmarkValidateApproved(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("hooks=%d", n), func(b *testing.B) {
			var calls []string
			r := mustRegistry(b,
				GroupSpec{Name: "g1", Priority: 100, ShortCircuit: false},
				GroupSpec{Name: "g2", Priority: 1, ShortCircuit: false},
			)
			for i := 0; i < n; i++ {
				group := "g1"
				if i%2 == 0 {
					group = "g2"
				}
				if err := r.Register(approveHook(fmt.Sprintf("h%d", i), group, &calls)); err != nil {
					b.Fatal(err)
				}
			}
			ctx := context.Background()
			tg := target{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rep, err := r.Validate(ctx, tg)
				if err != nil || rep.RanHooks() != n {
					b.Fatalf("unexpected: ran=%d err=%v", rep.RanHooks(), err)
				}
			}
		})
	}
}
