package compensation

import "testing"

// BenchmarkCanCompensate 证明"分支是否可开始补偿"的判定开销
// 不随动作分支总数增长：10 与 10000 条分支时，单次门禁判定
// 耗时应在同一量级（一次原子读，O(1)）。
func BenchmarkCanCompensate(b *testing.B) {
	for _, n := range []int{10, 10000} {
		b.Run(branchCount(n), func(b *testing.B) {
			g := NewGraph()
			exec := NewExecutor(g, NewLockManager(), nil)
			specs := make([]BranchSpec, 0, n)
			for i := 0; i < n; i++ {
				name := benchName(i)
				var deps []string
				if i > 0 {
					deps = []string{benchName(i - 1)}
				}
				specs = append(specs, BranchSpec{
					Name:      name,
					DependsOn: deps,
					Steps:     []StepSpec{step(name+"-s", "k"+name, 1, false, false)},
				})
			}
			a, err := exec.Declare("bench", specs)
			if err != nil {
				b.Fatal(err)
			}
			target := benchName(n / 2)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = a.CanCompensate(target)
			}
		})
	}
}

func branchCount(n int) string {
	return "branches=" + itoa(n)
}

func benchName(i int) string {
	const base = 26
	if i < base {
		return string(rune('a' + i))
	}
	return benchName(i/base-1) + string(rune('a'+i%base))
}
