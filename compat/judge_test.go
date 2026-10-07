package compat

import "testing"

var (
	v1 = v(1, 0, 0)
	v2 = v(2, 0, 0)
)

func genesisSchema() Schema {
	return Schema{
		"Order": ObjectType{
			"id":  Property{Type: strType(), Required: true},
			"qty": Property{Type: intType(FloatPtr(0), FloatPtr(100))},
		},
	}
}

// twoChain 构造 v1 -> v2 的两版本链。
func twoChain(changes []Change) []Format {
	return buildChain(genesisSchema(), []Version{v1, v2}, [][]Change{changes})
}

func fullRange() Range { return Range{Min: v(0, 0, 0), Max: v(9, 9, 9)} }

func snapAt(version Version, data map[string][]map[string]any) Snapshot {
	return Snapshot{Version: version, Data: data}
}

func TestAddOptionalProperty_ReadCompatible(t *testing.T) {
	chain := twoChain([]Change{{ObjectType: "Order", Property: "note", Kind: ChangeAddProperty, Type: strType()}})
	got := Judge(readProfile(v1, fullRange()), chain, snapAt(v2, nil))
	assertVerdict(t, got, LevelCompatible, CatNone)
	if len(got.Notes) == 0 {
		t.Error("expected a note about the ignored new property")
	}
}

func TestAddRequiredProperty_ReadDegraded(t *testing.T) {
	chain := twoChain([]Change{{ObjectType: "Order", Property: "note", Kind: ChangeAddProperty, Type: strType(), Required: true}})
	got := Judge(readProfile(v1, fullRange()), chain, snapAt(v2, nil))
	assertVerdict(t, got, LevelDegraded, CatNone)
}

func TestAddRequiredProperty_WriteForbidden(t *testing.T) {
	chain := twoChain([]Change{{ObjectType: "Order", Property: "note", Kind: ChangeAddProperty, Type: strType(), Required: true}})
	p := readProfile(v1, fullRange())
	p.Mode = ModeWrite
	got := Judge(p, chain, snapAt(v2, nil))
	assertVerdict(t, got, LevelIncompatible, CatRequiredMissing)
}

func TestRemoveProperty_DependedIncompatible(t *testing.T) {
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRemoveProperty,
		Type: intType(FloatPtr(0), FloatPtr(100))}})
	got := Judge(readProfile(v1, fullRange()), chain, snapAt(v2, nil))
	assertVerdict(t, got, LevelIncompatible, CatRequiredMissing)
}

func TestRemoveProperty_IndependentCompatible(t *testing.T) {
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRemoveProperty,
		Type: intType(FloatPtr(0), FloatPtr(100))}})
	p := readProfile(v1, fullRange())
	p.Independent = map[string]map[string]bool{"Order": {"qty": true}}
	got := Judge(p, chain, snapAt(v2, nil))
	assertVerdict(t, got, LevelCompatible, CatNone)
}

func TestRemoveProperty_ValueStillPresentIsInconsistent(t *testing.T) {
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRemoveProperty,
		Type: intType(FloatPtr(0), FloatPtr(100))}})
	p := readProfile(v1, fullRange())
	p.Independent = map[string]map[string]bool{"Order": {"qty": true}}
	snap := snapAt(v2, map[string][]map[string]any{
		"Order": {{"id": "o1", "qty": 3}}, // 已删除属性仍出现取值
	})
	got := Judge(p, chain, snap)
	assertVerdict(t, got, LevelIncompatible, CatDeletedPropertyPresent)
}

func TestPriority_RequiredMissingBeatsDeletedPresent(t *testing.T) {
	// 消费方仍依赖被删属性（RequiredMissing）且数据中仍出现取值（DeletedPropertyPresent）：
	// 必须命中优先级更高的 RequiredMissing。
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRemoveProperty,
		Type: intType(FloatPtr(0), FloatPtr(100))}})
	snap := snapAt(v2, map[string][]map[string]any{
		"Order": {{"id": "o1", "qty": 3}},
	})
	got := Judge(readProfile(v1, fullRange()), chain, snap)
	assertVerdict(t, got, LevelIncompatible, CatRequiredMissing)
}

func TestRetypeNarrow_DataProofOK(t *testing.T) {
	// qty: [0,100] 收紧为 [0,10]；消费方按 v2 编写读取 v1 历史数据，
	// 实际数据全部落在 [0,10] 内 -> 兼容。
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRetypeProperty,
		OldType: intType(FloatPtr(0), FloatPtr(100)), Type: intType(FloatPtr(0), FloatPtr(10))}})
	snap := snapAt(v1, map[string][]map[string]any{
		"Order": {{"id": "o1", "qty": 3}, {"id": "o2", "qty": 9}},
	})
	got := Judge(readProfile(v2, fullRange()), chain, snap)
	assertVerdict(t, got, LevelCompatible, CatNone)
}

func TestRetypeNarrow_DataProofFails(t *testing.T) {
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRetypeProperty,
		OldType: intType(FloatPtr(0), FloatPtr(100)), Type: intType(FloatPtr(0), FloatPtr(10))}})
	snap := snapAt(v1, map[string][]map[string]any{
		"Order": {{"id": "o1", "qty": 42}}, // 存在超出收紧后范围的既有取值
	})
	got := Judge(readProfile(v2, fullRange()), chain, snap)
	assertVerdict(t, got, LevelIncompatible, CatTypeIncompatible)
}

func TestRetypeWiden_ReadingOldDataCompatible(t *testing.T) {
	// 消费方按 v2（放宽后 [0,1000]）编写，读取 v1（[0,100]）的历史快照：必须兼容。
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRetypeProperty,
		OldType: intType(FloatPtr(0), FloatPtr(100)), Type: intType(FloatPtr(0), FloatPtr(1000))}})
	snap := snapAt(v1, map[string][]map[string]any{
		"Order": {{"id": "o1", "qty": 42}},
	})
	got := Judge(readProfile(v2, fullRange()), chain, snap)
	assertVerdict(t, got, LevelCompatible, CatNone)
}

func TestRequire_HistoricalDataComplete(t *testing.T) {
	// qty 由非必填改为必填；消费方按 v2 编写，读取 v1 历史数据且历史数据中始终存在 -> 兼容。
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRequireProperty}})
	snap := snapAt(v1, map[string][]map[string]any{
		"Order": {{"id": "o1", "qty": 1}, {"id": "o2", "qty": 2}},
	})
	got := Judge(readProfile(v2, fullRange()), chain, snap)
	assertVerdict(t, got, LevelCompatible, CatNone)
}

func TestRequire_HistoricalDataMissing(t *testing.T) {
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRequireProperty}})
	snap := snapAt(v1, map[string][]map[string]any{
		"Order": {{"id": "o1", "qty": 1}, {"id": "o2"}}, // o2 缺失该属性
	})
	got := Judge(readProfile(v2, fullRange()), chain, snap)
	assertVerdict(t, got, LevelIncompatible, CatRequiredMissing)
}

func TestUnrequire_AlwaysCompatible(t *testing.T) {
	// id 由必填改为非必填：两个方向的消费方都必须兼容。
	chain := twoChain([]Change{{ObjectType: "Order", Property: "id", Kind: ChangeUnrequireProperty}})
	snapNew := snapAt(v2, map[string][]map[string]any{"Order": {{"qty": 1}}})
	assertVerdict(t, Judge(readProfile(v1, fullRange()), chain, snapNew), LevelCompatible, CatNone)
	snapOld := snapAt(v1, map[string][]map[string]any{"Order": {{"id": "o1", "qty": 1}}})
	assertVerdict(t, Judge(readProfile(v2, fullRange()), chain, snapOld), LevelCompatible, CatNone)
}

func TestRequire_WriteModeForbidden(t *testing.T) {
	// 非必填改必填：写入场景下消费方无法保证产出该属性 -> 禁止。
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRequireProperty}})
	p := readProfile(v1, fullRange())
	p.Mode = ModeWrite
	got := Judge(p, chain, snapAt(v2, nil))
	assertVerdict(t, got, LevelIncompatible, CatRequiredMissing)
}

func TestPriority_RequiredMissingBeatsTypeIncompatible(t *testing.T) {
	chain := twoChain([]Change{
		{ObjectType: "Order", Property: "qty", Kind: ChangeRemoveProperty, Type: intType(FloatPtr(0), FloatPtr(100))},
		{ObjectType: "Order", Property: "id", Kind: ChangeRetypeProperty, OldType: strType(), Type: TypeSpec{Kind: KindEnum, Enum: []string{"x"}}},
	})
	snap := snapAt(v2, map[string][]map[string]any{
		"Order": {{"id": "not-x"}},
	})
	got := Judge(readProfile(v1, fullRange()), chain, snap)
	assertVerdict(t, got, LevelIncompatible, CatRequiredMissing)
}

func TestPriority_TypeIncompatibleBeatsDeletedPresent(t *testing.T) {
	// qty 放宽为 [0,1000]：消费方（v1）只能理解 [0,100]，且数据中存在 500 -> 类型不兼容；
	// note 被删除但数据中仍出现取值 -> 删除属性仍出现。前者优先级更高。
	chain := buildChain(Schema{
		"Order": ObjectType{
			"id":   Property{Type: strType(), Required: true},
			"qty":  Property{Type: intType(FloatPtr(0), FloatPtr(100))},
			"note": Property{Type: strType()},
		},
	}, []Version{v1, v2}, [][]Change{{
		{ObjectType: "Order", Property: "note", Kind: ChangeRemoveProperty, Type: strType()},
		{ObjectType: "Order", Property: "qty", Kind: ChangeRetypeProperty,
			OldType: intType(FloatPtr(0), FloatPtr(100)), Type: intType(FloatPtr(0), FloatPtr(1000))},
	}})
	p := readProfile(v1, fullRange())
	p.Independent = map[string]map[string]bool{"Order": {"note": true}}
	snap := snapAt(v2, map[string][]map[string]any{
		"Order": {{"id": "o1", "qty": 500, "note": "x"}}, // 同时触发类型不兼容与删除属性仍出现
	})
	got := Judge(p, chain, snap)
	assertVerdict(t, got, LevelIncompatible, CatTypeIncompatible)
}

func TestVersionOutOfRange_RejectsBeforePropertyChecks(t *testing.T) {
	// 变更本身会触发 RequiredMissing，但版本号超出可识别范围必须优先命中，
	// 且不产生任何属性级判定依据。
	chain := twoChain([]Change{{ObjectType: "Order", Property: "qty", Kind: ChangeRemoveProperty,
		Type: intType(FloatPtr(0), FloatPtr(100))}})
	p := readProfile(v1, Range{Min: v(0, 0, 0), Max: v(1, 9, 9)}) // v2 在范围外
	cv := JudgePath(p, chain, snapAt(v2, nil))
	assertVerdict(t, cv.Final, LevelIncompatible, CatVersionOutOfRange)
	if len(cv.Steps) != 0 {
		t.Errorf("expected no property-level steps, got %d", len(cv.Steps))
	}
}

func TestVersionExactlyOnRangeBoundary(t *testing.T) {
	chain := twoChain(nil)
	// 快照版本恰好等于上边界：进入属性级判定（无变化 -> 兼容）。
	p := readProfile(v1, Range{Min: v(1, 0, 0), Max: v(2, 0, 0)})
	assertVerdict(t, Judge(p, chain, snapAt(v2, nil)), LevelCompatible, CatNone)
	// 快照版本恰好等于下边界。
	p2 := readProfile(v2, Range{Min: v(1, 0, 0), Max: v(2, 0, 0)})
	assertVerdict(t, Judge(p2, chain, snapAt(v1, nil)), LevelCompatible, CatNone)
	// 刚好超出上边界一个补丁号：拒绝。
	chain2 := buildChain(genesisSchema(), []Version{v1, v(2, 0, 1)}, [][]Change{nil})
	p3 := readProfile(v1, Range{Min: v(1, 0, 0), Max: v(2, 0, 0)})
	assertVerdict(t, Judge(p3, chain2, snapAt(v(2, 0, 1), nil)), LevelIncompatible, CatVersionOutOfRange)
}
