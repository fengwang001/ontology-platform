package ontology

import "testing"

func TestReinterpretExactnessMatrix(t *testing.T) {
	// bool -> string / string -> bool 精确往返。
	if !NewStringType().CanReinterpret(NewBoolType(), NewValue(true)) {
		t.Fatal("bool->string must be lossless")
	}
	if !NewBoolType().CanReinterpret(NewStringType(), NewValue("false")) {
		t.Fatal(`"false"->bool must be lossless`)
	}
	if NewBoolType().CanReinterpret(NewStringType(), NewValue("1")) {
		t.Fatal(`"1" must not be approximately accepted as bool`)
	}
	// int -> float 无损；float -> string 不属于声明的无损方向。
	if !NewFloatType().CanReinterpret(NewIntType(), NewValue(int64(42))) {
		t.Fatal("int->float must be lossless")
	}
	if NewFloatType().CanReinterpret(NewStringType(), NewValue("1.50")) {
		t.Fatal(`"1.50" is non-canonical and must not parse to the float 1.5`)
	}
	if !NewFloatType().CanReinterpret(NewStringType(), NewValue("1.5")) {
		t.Fatal(`canonical "1.5" must be lossless`)
	}

	// ReinterpretValue 与 CanReinterpret 结论一致且产出合法新值。
	nv, ok := ReinterpretValue(NewStringType(), NewIntType(), NewValue(int64(-7)))
	if !ok || nv.Raw() != "-7" {
		t.Fatalf("int -7 -> string = %v ok=%v", nv, ok)
	}
	if _, ok := ReinterpretValue(NewIntType(), NewFloatType(), NewValue(1.25)); ok {
		t.Fatal("1.25 -> int must fail")
	}
}

func TestRemoveFieldReferenceAndNoDataTouch(t *testing.T) {
	store := newTestStore(Instance{ID: "i1",
		Fields: map[string]Value{"gone": NewValue(int64(1)), "keep": NewValue(int64(2))}})

	// 无引用方：删除字段本身不需要扫描取值，兼容。
	c := NewChecker("O", store, nil, nil)
	rep := c.CheckBatch([]FieldChange{{Name: "gone",
		Old: &FieldDef{Type: NewIntType(), Constraint: AnyValue()}, New: nil}})
	if !rep.Compatible || rep.Items[0].Category != CatRemove {
		t.Fatalf("unreferenced removal must be compatible: %+v", rep.Items[0])
	}

	// 被动作前置条件引用：字段删除后新语义下取值缺失，判定翻转 => 漂移。
	refs := NewReferenceRegistry()
	refs.Add(ActionReference{
		RefID: "a1", ObjType: "O", Field: "gone", Phase: "pre",
		Judgment: func(v EffectiveView, sem *FieldSemantics) bool {
			return sem != nil && v["gone"].Present()
		},
	})
	c2 := NewChecker("O", store, refs, nil)
	rep2 := c2.CheckBatch([]FieldChange{{Name: "gone",
		Old: &FieldDef{Type: NewIntType(), Constraint: AnyValue()}, New: nil}})
	if rep2.Compatible || rep2.Items[0].Reasons != KindReferenceDrift {
		t.Fatalf("removing referenced field must drift: %v", rep2.Items[0].Reasons.Strings())
	}

	// 判定通过后提交：旧值在存储中保留（新路径不可见），其它字段不动。
	ot := NewObjectType("O", store, NewReferenceRegistry(), nil)
	if _, err := ot.Submit([]FieldChange{
		{Name: "gone", New: &FieldDef{Type: NewIntType(), Constraint: AnyValue(),
			HasDefault: true, Default: NewValue(int64(0))}},
		{Name: "keep", New: &FieldDef{Type: NewIntType(), Constraint: AnyValue(),
			HasDefault: true, Default: NewValue(int64(0))}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ot.Submit([]FieldChange{{Name: "gone", New: nil}}); err != nil {
		t.Fatalf("removal rejected: %v", err)
	}
	store.Scan(func(inst Instance) bool {
		if inst.Fields["keep"].Raw() != int64(2) {
			t.Fatal("unrelated field mutated by removal")
		}
		return true
	})
	if _, ok := ot.Field("gone"); ok {
		t.Fatal("removed field still visible in schema")
	}
}

func TestInvalidDefaultRejected(t *testing.T) {
	store := newTestStore(Instance{ID: "i1", Fields: map[string]Value{}})
	c := NewChecker("O", store, nil, nil)
	// 默认值违反字段自身约束 => 不兼容。
	rep := c.CheckBatch([]FieldChange{{Name: "n", New: &FieldDef{
		Type:       NewIntType(),
		Constraint: NewRangeConstraint(0, 10, true, true),
		HasDefault: true, Default: NewValue(int64(99))}}})
	if rep.Compatible || rep.Items[0].Reasons != KindConstraintValue {
		t.Fatalf("invalid default must reject: %v", rep.Items[0].Reasons.Strings())
	}
}

func TestMixedConstraintTreatedAsTighten(t *testing.T) {
	store := newTestStore(
		Instance{ID: "hit", Fields: map[string]Value{"s": NewValue("c")}}, // 被移出枚举
		Instance{ID: "ok", Fields: map[string]Value{"s": NewValue("a")}},
	)
	c := NewChecker("O", store, nil, nil)
	rep := c.CheckBatch([]FieldChange{{Name: "s",
		Old: &FieldDef{Type: NewStringType(), Constraint: NewEnumConstraint([]string{"a", "b", "c"}),
			HasDefault: true, Default: NewValue("a")},
		New: &FieldDef{Type: NewStringType(), Constraint: NewEnumConstraint([]string{"a", "d"}),
			HasDefault: true, Default: NewValue("a")}}})
	if rep.Items[0].Category != CatMixed {
		t.Fatalf("category=%v", rep.Items[0].Category)
	}
	if rep.Compatible || rep.Items[0].Reasons != KindConstraintValue {
		t.Fatalf("mixed change must scan and reject on old value 'c': %v",
			rep.Items[0].Reasons.Strings())
	}
}

func TestWriteInstanceHonorsDefaultAndAllowMissing(t *testing.T) {
	store := NewInstanceStore()
	ot := NewObjectType("O", store, nil, nil)
	_, err := ot.Submit([]FieldChange{
		{Name: "d", New: &FieldDef{Type: NewIntType(), Constraint: AnyValue(),
			HasDefault: true, Default: NewValue(int64(3))}},
		{Name: "o", New: &FieldDef{Type: NewIntType(), Constraint: AnyValue(), AllowMissing: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ver, err := ot.WriteInstance("i1", map[string]Value{})
	if err != nil || ver != 1 {
		t.Fatalf("empty write should pass via default/allow-missing: %v", err)
	}
}

func TestBatchAggregatesAcrossItems(t *testing.T) {
	store := newTestStore(
		Instance{ID: "i1", Fields: map[string]Value{"n": NewValue(int64(99))}},
	)
	c := NewChecker("O", store, nil, nil)
	rep := c.CheckBatch([]FieldChange{
		{Name: "ok", New: &FieldDef{Type: NewStringType(), Constraint: AnyValue(),
			HasDefault: true, Default: NewValue("z")}}, // 兼容：新增带默认
		{Name: "n", // 不兼容：收紧，99 违规
			Old: &FieldDef{Type: NewIntType(), Constraint: NewRangeConstraint(0, 100, true, true),
				HasDefault: true, Default: NewValue(int64(0))},
			New: &FieldDef{Type: NewIntType(), Constraint: NewRangeConstraint(0, 10, true, true),
				HasDefault: true, Default: NewValue(int64(0))}},
		{Name: "req", New: &FieldDef{Type: NewIntType(), Constraint: AnyValue()}}, // 不兼容：必填无回填
	})
	if rep.Compatible {
		t.Fatal("batch must be incompatible")
	}
	if rep.Reasons != KindConstraintValue|KindMissingRequired {
		t.Fatalf("aggregated reasons=%v", rep.Reasons.Strings())
	}
	okItems, badItems := 0, 0
	for _, it := range rep.Items {
		if it.Compatible {
			okItems++
		} else {
			badItems++
		}
	}
	if okItems != 1 || badItems != 2 {
		t.Fatalf("items compatible=%d incompatible=%d", okItems, badItems)
	}
}
