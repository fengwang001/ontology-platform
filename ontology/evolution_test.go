package ontology

import (
	"errors"
	"testing"
)

func TestBatchAtomicAllOrNothing(t *testing.T) {
	store := newTestStore(
		Instance{ID: "ok", Fields: map[string]Value{"n": NewValue(int64(5))}},
		Instance{ID: "bad", Fields: map[string]Value{"n": NewValue(int64(99))}},
	)
	log := NewMemoryAuditLog()
	ot := NewObjectType("Order", store, NewReferenceRegistry(), log)

	// 初始版本：n 为 [0,100] 的 int；p 为可选 string。
	base := []FieldChange{
		{Name: "n", New: &FieldDef{Type: NewIntType(),
			Constraint: NewRangeConstraint(0, 100, true, true),
			HasDefault: true, Default: NewValue(int64(0))}},
		{Name: "p", New: &FieldDef{Type: NewStringType(),
			Constraint: AnyValue(), AllowMissing: true}},
	}
	if _, err := ot.Submit(base); err != nil {
		t.Fatalf("base schema rejected: %v", err)
	}
	versionBefore := ot.Version()

	// 打包：第 1 项兼容（放宽 p 为带默认值新字段无关，这里用纯放宽），
	// 第 2 项不兼容（收紧 n 到 [0,10]，bad=99 违规）。
	mixed := []FieldChange{
		{Name: "p",
			Old: &FieldDef{Type: NewStringType(), Constraint: AnyValue(), AllowMissing: true},
			New: &FieldDef{Type: NewStringType(), Constraint: AnyValue(),
				HasDefault: true, Default: NewValue("d")}},
		{Name: "n",
			Old: &FieldDef{Type: NewIntType(),
				Constraint: NewRangeConstraint(0, 100, true, true),
				HasDefault: true, Default: NewValue(int64(0))},
			New: &FieldDef{Type: NewIntType(),
				Constraint: NewRangeConstraint(0, 10, true, true),
				HasDefault: true, Default: NewValue(int64(0))}},
	}
	rep, err := ot.Submit(mixed)
	if err == nil {
		t.Fatal("mixed batch must be rejected")
	}
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("error must be *RejectError, got %T", err)
	}
	if rep.Compatible || rep.Reasons != KindConstraintValue {
		t.Fatalf("report = compatible:%v reasons:%v", rep.Compatible, rep.Reasons.Strings())
	}

	// 零状态变化核对：版本号、字段定义、实例数据全部保持提交前。
	if ot.Version() != versionBefore {
		t.Fatalf("version changed %d -> %d on rejected batch", versionBefore, ot.Version())
	}
	if def, ok := ot.Field("p"); !ok || def.HasDefault {
		t.Fatalf("compatible item must not take partial effect: p=%+v", def)
	}
	if def, ok := ot.Field("n"); !ok {
		t.Fatal("n disappeared")
	} else if rg, ok := def.Constraint.(rangeConstraint); !ok || (rg.hasMax && rg.max != 100) {
		t.Fatalf("n constraint mutated on rejected batch: %+v", def.Constraint)
	}
	store.Scan(func(inst Instance) bool {
		if _, has := inst.Fields["p"]; has {
			t.Fatal("backfill/default materialization leaked on rejected batch")
		}
		return true
	})
}

func TestAcceptedBatchAppliesAndBackfills(t *testing.T) {
	store := newTestStore(
		Instance{ID: "i1", Fields: map[string]Value{}},
		Instance{ID: "i2", Fields: map[string]Value{}},
	)
	ot := NewObjectType("O", store, nil, nil)
	fc := []FieldChange{{Name: "tenant", New: &FieldDef{
		Type: NewStringType(), Constraint: NewEnumConstraint([]string{"a", "b"})},
		Backfill: constBackfill{NewValue("a")}}}
	rep, err := ot.Submit(fc)
	if err != nil || !rep.Compatible {
		t.Fatalf("backfilled required add must apply: %v %+v", err, rep)
	}
	if ot.Version() != 1 {
		t.Fatalf("version=%d, want 1", ot.Version())
	}
	store.Scan(func(inst Instance) bool {
		v, ok := inst.Fields["tenant"]
		if !ok || v.Raw() != "a" {
			t.Fatalf("instance %s not backfilled: %v", inst.ID, v)
		}
		return true
	})
	// 新写入实例缺少必填字段必须被当前版本拒绝。
	if _, err := ot.WriteInstance("i3", map[string]Value{}); err == nil {
		t.Fatal("write missing required field must fail")
	}
	if _, err := ot.WriteInstance("i3", map[string]Value{
		"tenant": NewValue("zzz")}); err == nil {
		t.Fatal("write violating constraint must fail")
	}
	if _, err := ot.WriteInstance("i3", map[string]Value{
		"tenant": NewValue("b")}); err != nil {
		t.Fatalf("valid write must succeed: %v", err)
	}
}
