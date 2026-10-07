package compat

import (
	"fmt"
	"testing"
)

// padSchema 向 Schema 注入 n 个永不变更的属性，模拟属性总数增长。
func padSchema(s Schema, n int) Schema {
	out := cloneSchema(s)
	ot := ObjectType{}
	for i := 0; i < n; i++ {
		ot[fmt.Sprintf("pad%05d", i)] = Property{Type: strType()}
	}
	out["Padding"] = ot
	return out
}

// TestCostScalesWithChangesNotTotalProperties 复核判定开销只与变化的属性相关：
// 同样的变更记录，属性总数从 10 增长到 5000，检查的变更条数必须保持不变。
func TestCostScalesWithChangesNotTotalProperties(t *testing.T) {
	changes := [][]Change{{
		{ObjectType: "Order", Property: "note", Kind: ChangeAddProperty, Type: strType()},
		{ObjectType: "Order", Property: "qty", Kind: ChangeRequireProperty},
	}}

	var stats []Stats
	for _, pad := range []int{10, 5000} {
		chain := buildChain(padSchema(genesisSchema(), pad), []Version{v1, v2}, changes)
		v := Judge(readProfile(v1, fullRange()), chain, snapAt(v2, nil))
		stats = append(stats, v.Stats)
	}
	if stats[0] != stats[1] {
		t.Errorf("inspected count grew with total properties: %+v vs %+v", stats[0], stats[1])
	}
	if stats[0].PropertiesInspected != 2 {
		t.Errorf("inspected = %d, want 2 (only the changed properties)", stats[0].PropertiesInspected)
	}

	// 对照：朴素模型的开销随属性总数增长。
	small := buildChain(padSchema(genesisSchema(), 10), []Version{v1, v2}, changes)
	large := buildChain(padSchema(genesisSchema(), 5000), []Version{v1, v2}, changes)
	nSmall := NaiveJudge(readProfile(v1, fullRange()), small[0], small[1], snapAt(v2, nil))
	nLarge := NaiveJudge(readProfile(v1, fullRange()), large[0], large[1], snapAt(v2, nil))
	if nLarge.Stats.PropertiesInspected <= nSmall.Stats.PropertiesInspected {
		t.Errorf("naive model should inspect more properties when schema grows: %d vs %d",
			nSmall.Stats.PropertiesInspected, nLarge.Stats.PropertiesInspected)
	}
}
