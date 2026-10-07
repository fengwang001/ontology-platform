package ontology

import (
	"errors"
	"reflect"
	"testing"
)

// TestRuleVersionAcrossStream 规则版本调整跨越事件流时，
// 每个事件按其发生时刻生效的版本解释。
//
// 时间线：
//
//	v1@100: A wheels[0,100]; B(子) wheels[0,10]
//	t=110 创建为 B；t=120 赋值 wheels=5（v1 下 B [0,10] 合规）
//	t=130 演变 B->A（按 v1 的 A [0,100] 重校验，5 保留）
//	v2@200: B 的覆盖收窄为 wheels[0,3]
//	t=210 演变 A->B（按 v2 的 B [0,3] 重校验，5 越界被丢弃）
func TestRuleVersionAcrossStream(t *testing.T) {
	rules := NewRuleStore()
	v1 := RuleVersion{ValidFrom: 100, Types: map[string]ObjectType{
		"A": {ID: "A", Props: map[string]Range{"wheels": IntRange{Min: 0, Max: 100}}},
		"B": {ID: "B", Parent: "A", Props: map[string]Range{"wheels": IntRange{Min: 0, Max: 10}}},
	}}
	if err := rules.AddVersion(v1); err != nil {
		t.Fatal(err)
	}
	s := NewStore(rules)
	mustAppend(s, "x", Created(110, "B"))
	mustAppend(s, "x", Set(120, "wheels", Int(5)))
	mustAppend(s, "x", Evolve(130, "A"))

	// v2 尚未追加时的历史重建结果，作为后续“版本归属不可变”的基准。
	before125 := mustState(t, s, "x", 125)
	before150 := mustState(t, s, "x", 150)

	v2 := RuleVersion{ValidFrom: 200, Types: map[string]ObjectType{
		"A": {ID: "A", Props: map[string]Range{"wheels": IntRange{Min: 0, Max: 100}}},
		"B": {ID: "B", Parent: "A", Props: map[string]Range{"wheels": IntRange{Min: 0, Max: 3}}},
	}}
	if err := rules.AddVersion(v2); err != nil {
		t.Fatal(err)
	}
	mustAppend(s, "x", Evolve(210, "B"))

	// t=125：仍是 B，wheels=5 按 v1 解释（v2 的 [0,3] 不得回溯 reinterpret）。
	if st := mustState(t, s, "x", 125); !reflect.DeepEqual(st, before125) || st.Props["wheels"] != Int(5) {
		t.Fatalf("t=125 状态不符: %+v", st)
	}
	// t=150：已退回 A，wheels=5 保留。
	if st := mustState(t, s, "x", 150); !reflect.DeepEqual(st, before150) || st.Props["wheels"] != Int(5) {
		t.Fatalf("t=150 状态不符: %+v", st)
	}
	// t=300：t=210 的演变按 v2 重校验，wheels=5 越出 B 的 [0,3] 被丢弃。
	st := mustState(t, s, "x", 300)
	if st.TypeID != "B" || len(st.Props) != 0 {
		t.Fatalf("t=300 状态不符: %+v", st)
	}
}

// TestRuleVersionBoundary 版本切换时点归属新版本（左闭区间）。
func TestRuleVersionBoundary(t *testing.T) {
	rules := NewRuleStore()
	if err := rules.AddVersion(RuleVersion{ValidFrom: 100, Types: map[string]ObjectType{
		"A": {ID: "A", Props: map[string]Range{"n": IntRange{Min: 0, Max: 100}}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := rules.AddVersion(RuleVersion{ValidFrom: 200, Types: map[string]ObjectType{
		"A": {ID: "A", Props: map[string]Range{"n": IntRange{Min: 0, Max: 5}}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := NewStore(rules)
	mustAppend(s, "x", Created(150, "A"))
	mustAppend(s, "x", Set(199, "n", Int(50))) // v1 [0,100]：合规
	// t=200 恰为切换点：归属 v2，[0,5] 下 50 越界，追加被拒。
	if err := s.Append("x", Set(200, "n", Int(50))); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("切换点应归属新版本: 期望 ErrOutOfRange, 得到 %v", err)
	}
	mustAppend(s, "x", Set(200, "n", Int(5)))
	st := mustState(t, s, "x", 300)
	if st.Props["n"] != Int(5) {
		t.Fatalf("切换点后状态不符: %+v", st)
	}
}

// TestVersionAssignmentImmutable 历史时刻的版本归属不随后续版本追加而改变。
func TestVersionAssignmentImmutable(t *testing.T) {
	rules := standardRules()
	s := NewStore(rules)
	mustAppend(s, "x", Created(100, "Sedan"))
	mustAppend(s, "x", Set(110, "wheels", Int(4)))
	mustAppend(s, "x", Evolve(120, "Car"))

	snap := map[int64]State{}
	for _, cutoff := range []int64{105, 115, 125, 1000} {
		snap[cutoff] = mustState(t, s, "x", cutoff)
	}
	// 追加一个颠覆性新版本（删除 Sedan、改范围），历史重建结果必须不变。
	v2 := RuleVersion{ValidFrom: 5000, Types: map[string]ObjectType{
		"Vehicle": {ID: "Vehicle", Props: map[string]Range{"wheels": IntRange{Min: 0, Max: 2}}},
	}}
	if err := rules.AddVersion(v2); err != nil {
		t.Fatal(err)
	}
	for cutoff, want := range snap {
		if got := mustState(t, s, "x", cutoff); !reflect.DeepEqual(got, want) {
			t.Fatalf("cutoff=%d 的历史重建被新版本改变:\n got %+v\nwant %+v", cutoff, got, want)
		}
	}
}

// TestUndefinedTypeAtEventTime 目标类型在事件时刻的版本中尚未定义。
func TestUndefinedTypeAtEventTime(t *testing.T) {
	rules := NewRuleStore()
	if err := rules.AddVersion(RuleVersion{ValidFrom: 100, Types: map[string]ObjectType{
		"A": {ID: "A", Props: map[string]Range{}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := NewStore(rules)
	mustAppend(s, "x", Created(100, "A"))
	// 类型 B 到 v2（@200）才出现；t=150 的演变目标 B 在当时未定义。
	if err := rules.AddVersion(RuleVersion{ValidFrom: 200, Types: map[string]ObjectType{
		"A": {ID: "A", Props: map[string]Range{}},
		"B": {ID: "B", Parent: "A", Props: map[string]Range{}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append("x", Evolve(150, "B")); !errors.Is(err, ErrUndefinedTargetType) {
		t.Fatalf("期望 ErrUndefinedTargetType, 得到 %v", err)
	}
	mustAppend(s, "x", Evolve(250, "B")) // v2 下合法
	if st := mustState(t, s, "x", 300); st.TypeID != "B" {
		t.Fatalf("状态不符: %+v", st)
	}
}

// TestRuleStoreInvariants 规则库自身的不变量。
func TestRuleStoreInvariants(t *testing.T) {
	rules := NewRuleStore()
	// 生效时刻必须严格递增。
	v := RuleVersion{ValidFrom: 100, Types: map[string]ObjectType{"A": {ID: "A"}}}
	if err := rules.AddVersion(v); err != nil {
		t.Fatal(err)
	}
	if err := rules.AddVersion(RuleVersion{ValidFrom: 100, Types: map[string]ObjectType{"A": {ID: "A"}}}); err == nil {
		t.Fatal("相同生效时刻应被拒绝")
	}
	if err := rules.AddVersion(RuleVersion{ValidFrom: 50, Types: map[string]ObjectType{"A": {ID: "A"}}}); err == nil {
		t.Fatal("倒退的生效时刻应被拒绝")
	}
	// 环检测。
	if err := rules.AddVersion(RuleVersion{ValidFrom: 200, Types: map[string]ObjectType{
		"A": {ID: "A", Parent: "B"},
		"B": {ID: "B", Parent: "A"},
	}}); err == nil {
		t.Fatal("继承环应被拒绝")
	}
	// 父类型缺失。
	if err := rules.AddVersion(RuleVersion{ValidFrom: 300, Types: map[string]ObjectType{
		"A": {ID: "A", Parent: "Missing"},
	}}); err == nil {
		t.Fatal("缺失父类型应被拒绝")
	}
	// 覆盖未收窄。
	if err := rules.AddVersion(RuleVersion{ValidFrom: 400, Types: map[string]ObjectType{
		"A": {ID: "A", Props: map[string]Range{"n": IntRange{Min: 0, Max: 10}}},
		"B": {ID: "B", Parent: "A", Props: map[string]Range{"n": IntRange{Min: 0, Max: 20}}},
	}}); err == nil {
		t.Fatal("未收窄的覆盖应被拒绝")
	}
	// 首个版本之前无生效版本。
	if rv, idx := rules.EffectiveAt(99); rv != nil || idx != -1 {
		t.Fatal("首个版本之前应无生效版本")
	}
	if _, idx := rules.EffectiveAt(100); idx != 0 {
		t.Fatalf("t=100 应归属版本 0, 得到 %d", idx)
	}
}
