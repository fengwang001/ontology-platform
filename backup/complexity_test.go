package backup

import (
	"fmt"
	"testing"
)

// 以可验证方式证明计划引擎的线性复杂度：
// 无论链多深、备份多少，可恢复性解析与原因传播的步数
// 都恰好等于备份总数（每个备份只被访问一次），
// 不存在“对每个备份重复遍历整条依赖链”的平方行为。
func TestPlanLinearComplexity(t *testing.T) {
	for _, depth := range []int{1, 10, 1000, 20000} {
		recs := make(map[string]*rec, depth)
		order := make([]string, 0, depth)
		parent := ""
		for i := 0; i < depth; i++ {
			id := fmt.Sprintf("b%d", i)
			recs[id] = &rec{id: id, parent: parent, createdAt: int64(i)}
			order = append(order, id)
			parent = id
		}
		var st planStats
		computePlan(recs, order, Policy{Daily: 3, Weekly: 2, Monthly: 1}, int64(depth), &st)
		if st.recoverabilityVisits != depth {
			t.Errorf("depth=%d 可恢复性解析步数 = %d, want %d", depth, st.recoverabilityVisits, depth)
		}
		if st.propagationVisits != depth {
			t.Errorf("depth=%d 原因传播步数 = %d, want %d", depth, st.propagationVisits, depth)
		}
	}
}

// 混合拓扑（深链 + 大量全量备份）下步数仍与备份总数成线性关系。
func TestPlanLinearComplexityMixed(t *testing.T) {
	const chains, depth, fulls = 50, 200, 5000
	recs := make(map[string]*rec, chains*depth+fulls)
	var order []string
	now := int64(0)
	for c := 0; c < chains; c++ {
		parent := ""
		for d := 0; d < depth; d++ {
			id := fmt.Sprintf("c%d-%d", c, d)
			recs[id] = &rec{id: id, parent: parent, createdAt: now}
			order = append(order, id)
			parent = id
			now++
		}
	}
	for f := 0; f < fulls; f++ {
		id := fmt.Sprintf("f%d", f)
		recs[id] = &rec{id: id, createdAt: now}
		order = append(order, id)
		now++
	}
	total := len(recs)
	var st planStats
	computePlan(recs, order, Policy{Daily: 5, Weekly: 5, Monthly: 5}, now, &st)
	if st.recoverabilityVisits != total || st.propagationVisits != total {
		t.Errorf("步数 = (%d, %d), want (%d, %d)",
			st.recoverabilityVisits, st.propagationVisits, total, total)
	}
}

// 登记开销与已登记备份总数无关：哈希表插入 + 追加，摊还 O(1)。
// 该基准在 N 持续增长时每次登记耗时应保持平稳。
func BenchmarkRegisterBackup(b *testing.B) {
	s := NewService()
	ids := make([]string, b.N)
	for i := range ids {
		ids[i] = fmt.Sprintf("b%d", i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.RegisterBackup(int64(i), ids[i], Full, "", 1); err != nil {
			b.Fatal(err)
		}
	}
}

// 深链 + 大量备份下生成计划的开销（线性）。
func BenchmarkPlanCleanup(b *testing.B) {
	s := NewService()
	if err := s.SetPolicy(0, Policy{Daily: 7, Weekly: 4, Monthly: 12}); err != nil {
		b.Fatal(err)
	}
	now := int64(0)
	parent := ""
	// 一条 5000 深的链 + 45000 个全量备份。
	for i := 0; i < 5000; i++ {
		id := fmt.Sprintf("chain-%d", i)
		kind := Incremental
		if parent == "" {
			kind = Full
		}
		if err := s.RegisterBackup(now, id, kind, parent, 1); err != nil {
			b.Fatal(err)
		}
		parent = id
		now++
	}
	for i := 0; i < 45000; i++ {
		if err := s.RegisterBackup(now, fmt.Sprintf("full-%d", i), Full, "", 1); err != nil {
			b.Fatal(err)
		}
		now++
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.PlanCleanup(now); err != nil {
			b.Fatal(err)
		}
	}
}
