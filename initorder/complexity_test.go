package initorder

import (
	"fmt"
	"testing"
)

// buildSharedFuncSession 构造“一个函数被大量单元引用”的场景：
// K 个函数串成链 f0 -> f1 -> ... -> f(K-1) -> x，N 个单元各自引用 f0。
func buildSharedFuncSession(t testing.TB, chainLen, numUnits int) *Session {
	t.Helper()
	s, err := NewSession(nil)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if err := s.RegisterVars([]string{"x"}, nil); err != nil {
		t.Fatalf("RegisterVars failed: %v", err)
	}
	for i := 0; i < chainLen; i++ {
		ref := "x"
		if i+1 < chainLen {
			ref = fmt.Sprintf("f%d", i+1)
		}
		if err := s.RegisterFunc(fmt.Sprintf("f%d", i), []string{ref}); err != nil {
			t.Fatalf("RegisterFunc failed: %v", err)
		}
	}
	for i := 0; i < numUnits; i++ {
		if err := s.RegisterVars([]string{fmt.Sprintf("u%d", i)}, []string{"f0"}); err != nil {
			t.Fatalf("RegisterVars failed: %v", err)
		}
	}
	return s
}

// TestSharedFuncExpandedOnce 验证被大量单元引用的函数链只展开一次：
// 函数体展开计数等于函数个数，与引用者数量无关。
func TestSharedFuncExpandedOnce(t *testing.T) {
	const chainLen = 64
	const numUnits = 4096
	s := buildSharedFuncSession(t, chainLen, numUnits)
	sol, stats, err := s.solveLocked()
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if stats.FuncExpansions != chainLen {
		t.Fatalf("FuncExpansions = %d, want %d (must not scale with referrers)", stats.FuncExpansions, chainLen)
	}
	if stats.UnitsCompleted != numUnits+1 {
		t.Fatalf("UnitsCompleted = %d, want %d", stats.UnitsCompleted, numUnits+1)
	}
	if len(sol.Order) != numUnits+1 {
		t.Fatalf("order length = %d, want %d", len(sol.Order), numUnits+1)
	}
	// 每个引用 f0 的单元传递依赖都只有 x。
	for _, u := range sol.Order {
		if u.Unit == 0 {
			continue
		}
		if len(u.Deps) != 1 || u.Deps[0] != "x" {
			t.Fatalf("unit %d deps = %v, want [x]", u.Unit, u.Deps)
		}
	}
}

// TestLargeSharedFuncSolve 较大规模下求解依然快速完成（若对每个引用者
// 重复展开函数链，20000 单元 × 2000 函数的朴素展开将无法在测试时限内完成）。
func TestLargeSharedFuncSolve(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large solve in short mode")
	}
	s := buildSharedFuncSession(t, 2000, 20000)
	sol, stats, err := s.solveLocked()
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if stats.FuncExpansions != 2000 {
		t.Fatalf("FuncExpansions = %d, want 2000", stats.FuncExpansions)
	}
	if len(sol.Order) != 20001 {
		t.Fatalf("order length = %d, want 20001", len(sol.Order))
	}
}

// BenchmarkSolveSharedFunc 衡量共享函数场景下求解随单元数的增长。
func BenchmarkSolveSharedFunc(b *testing.B) {
	for _, numUnits := range []int{1000, 2000, 4000, 8000} {
		b.Run(fmt.Sprintf("units=%d", numUnits), func(b *testing.B) {
			s := buildSharedFuncSession(b, 128, numUnits)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Solve(); err != nil {
					b.Fatalf("Solve failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkSolveChain 衡量长依赖链（每个单元依赖前一个单元）下的求解。
func BenchmarkSolveChain(b *testing.B) {
	for _, numUnits := range []int{1000, 2000, 4000, 8000} {
		b.Run(fmt.Sprintf("units=%d", numUnits), func(b *testing.B) {
			s, err := NewSession(nil)
			if err != nil {
				b.Fatalf("NewSession failed: %v", err)
			}
			prev := ""
			for i := 0; i < numUnits; i++ {
				name := fmt.Sprintf("v%d", i)
				var refs []string
				if prev != "" {
					refs = []string{prev}
				}
				if err := s.RegisterVars([]string{name}, refs); err != nil {
					b.Fatalf("RegisterVars failed: %v", err)
				}
				prev = name
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Solve(); err != nil {
					b.Fatalf("Solve failed: %v", err)
				}
			}
		})
	}
}
