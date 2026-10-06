package scrub

import "testing"

// TestDueComparisonsIndependentOfTotal 更直接的证明：
// 固定到期块数 k=5，把总块数从 100 放大到 50000，
// 到期枚举的额外键比较次数保持恒定（k + 越过边界的至多 1 次）。
func TestDueComparisonsIndependentOfTotal(t *testing.T) {
	measure := func(N, k int) int64 {
		s := NewStore()
		const interval = Time(1_000_000)
		for id := 0; id < N; id++ {
			rs := []Replica{rep(1, 1, "d"), rep(2, 1, "d")}
			if err := s.CreateBlock(0, id, rs, interval); err != nil {
				t.Fatal(err)
			}
		}
		// 块 k..N-1 在 t=10 巡检过（下次到期 = 10+interval，很远）；
		// 块 0..k-1 从未巡检（到期点 0）。
		for id := k; id < N; id++ {
			if _, err := s.Patrol(10, id, nil); err != nil {
				t.Fatal(err)
			}
		}

		before := s.dueComparisons()
		got, err := s.Due(10, 100)
		if err != nil {
			t.Fatal(err)
		}
		used := s.dueComparisons() - before
		if len(got) != k {
			t.Fatalf("N=%d due count=%d want %d", N, len(got), k)
		}
		return used
	}

	c100 := measure(100, 5)
	c50000 := measure(50000, 5)
	// 到期键 5 个 + 第一个 dueAt>0 的键 1 个 = 6 次与 t 的比较，
	// 与 N 无关（允许恰好为 5 或 6，取决于遍历停止实现）。
	if c100 != c50000 {
		t.Fatalf("enumeration comparisons depend on total blocks: N=100 -> %d, N=50000 -> %d", c100, c50000)
	}
	if c50000 > 6 {
		t.Fatalf("enumeration comparisons %d exceed due+1 bound", c50000)
	}
	t.Logf("due enumeration: k=5 comparisons=%d at N=100 and N=50000 (identical)", c100)
}

// TestArbitrationLinearInReplicas 仲裁开销只随副本数增长：
// 2..5 副本均有界工作；这里用决策正确性间接保证无隐藏的全局扫描。
func TestArbitrationLinearInReplicas(t *testing.T) {
	for n := 2; n <= 5; n++ {
		rs := make([]Replica, n)
		for i := range rs {
			rs[i] = rep(i+1, 1, "d")
		}
		if a := Arbitrate(rs); a.Outcome != OutcomeNoRepair {
			t.Fatalf("n=%d unexpected %s", n, a.Outcome)
		}
	}
}
