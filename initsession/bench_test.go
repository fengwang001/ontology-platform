package initsession

import (
	"fmt"
	"testing"
)

// BenchmarkSharedClosure measures solving when one function's closure
// is consumed by b.N units; per-referencer expansion would make this
// quadratic, shared closures keep it near linear.
func BenchmarkSharedClosure(b *testing.B) {
	for _, n := range []int{1000, 4000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for rep := 0; rep < b.N; rep++ {
				// Rebuilding the session is part of the benchmark setup
				// cost only; stop/start the timer around it.
				b.StopTimer()
				s, _ := New()
				s.AddVariableUnit([]string{"root"}, nil)
				s.AddFunction("big", []string{"root", "nested"})
				s.AddFunction("nested", []string{"root"})
				for i := 0; i < n; i++ {
					s.AddVariableUnit([]string{fmt.Sprintf("v%d", i)}, []string{"big"})
				}
				b.StartTimer()
				if _, _, err := s.Solve(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkReverseChain stresses the scheduler: one unit becomes ready
// per round. Per-round rescanning would be O(n^2).
func BenchmarkReverseChain(b *testing.B) {
	for _, n := range []int{1000, 4000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.StopTimer()
			sessions := make([]*Session, b.N)
			for rep := range sessions {
				s, _ := New()
				for i := 0; i < n; i++ {
					var refs []string
					if i > 0 {
						refs = []string{fmt.Sprintf("v%d", i-1)}
					}
					s.AddVariableUnit([]string{fmt.Sprintf("v%d", i)}, refs)
				}
				sessions[rep] = s
			}
			b.StartTimer()
			for rep := 0; rep < b.N; rep++ {
				if _, _, err := sessions[rep].Solve(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
