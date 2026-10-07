package ontology_test

import (
	"fmt"
	"testing"

	"ontology/tests/naive"
)

// BenchmarkNaiveResolve 对照朴素模型的全量扫描：其开销随钩子总数线性增长。
func BenchmarkNaiveResolve100Hooks(b *testing.B)    { benchmarkNaiveResolve(b, 50) }
func BenchmarkNaiveResolve10000Hooks(b *testing.B)  { benchmarkNaiveResolve(b, 5000) }
func BenchmarkNaiveResolve100000Hooks(b *testing.B) { benchmarkNaiveResolve(b, 50000) }

func benchmarkNaiveResolve(b *testing.B, edges int) {
	var hooks []naive.HookSpec
	for j := 0; j < edges; j++ {
		hooks = append(hooks,
			naive.HookSpec{ID: fmt.Sprintf("te%d", j), Kind: naive.KindTransition,
				From: fmt.Sprintf("x%d", j), Stage: fmt.Sprintf("y%d", j)},
			naive.HookSpec{ID: fmt.Sprintf("ee%d", j), Kind: naive.KindEntry,
				Stage: fmt.Sprintf("y%d", j)},
		)
	}
	hooks = append(hooks, naive.HookSpec{ID: "target", Kind: naive.KindTransition, From: "s0", Stage: "s1"})
	decl := &naive.TypeDecl{Hooks: hooks}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = decl.ResolveBench("s0", "s1")
	}
}
