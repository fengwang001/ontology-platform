package subro_test

import (
	"fmt"
	"testing"

	"ontology/subro"
)

// BenchmarkRecoverAfterHistory measures the cost of one more accepted
// recovery after 1e2, 1e4 and 1e6 prior recoveries. Flat ns/op across
// the sub-benches demonstrates that settling does not depend on the
// recovery history length.
func BenchmarkRecoverAfterHistory(b *testing.B) {
	for _, history := range []int{100, 10_000, 1_000_000} {
		b.Run(fmt.Sprintf("history=%d", history), func(b *testing.B) {
			s := subro.NewSystem()
			if err := s.RegisterCase(0, "c", 1<<60, 1<<59, 1<<62, 10000); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < history; i++ {
				if _, err := s.Recover(int64(i)+1, "c", 10, 1); err != nil {
					b.Fatal(err)
				}
			}
			now := int64(history) + 1
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Recover(now+int64(i), "c", 10, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRecoverWithCases measures the cost of one accepted
// recovery on one case while the system holds 1, 1e3 or 1e5 cases.
func BenchmarkRecoverWithCases(b *testing.B) {
	for _, cases := range []int{1, 1_000, 100_000} {
		b.Run(fmt.Sprintf("cases=%d", cases), func(b *testing.B) {
			s := subro.NewSystem()
			for i := 0; i < cases; i++ {
				if err := s.RegisterCase(0, fmt.Sprintf("case-%d", i), 1000, 400, 1<<62, 10000); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Recover(int64(i)+1, "case-0", 10, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
