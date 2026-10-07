package ontology

import (
	"reflect"
	"testing"
)

func newTestStore(insts ...Instance) *InstanceStore {
	s := NewInstanceStore()
	for _, in := range insts {
		s.Put(in.ID, in.Fields)
	}
	return s
}

// ---- 新增字段 ----------------------------------------------------------

func TestAddWithDefaultCompatible(t *testing.T) {
	store := newTestStore(Instance{ID: "i1", Fields: map[string]Value{}})
	log := NewMemoryAuditLog()
	c := NewChecker("Order", store, nil, log)
	rep := c.CheckBatch([]FieldChange{{Name: "note",
		New: &FieldDef{Type: NewStringType(), Constraint: AnyValue(),
			HasDefault: true, Default: NewValue("x")}}})
	if !rep.Compatible {
		t.Fatalf("add with default must be compatible: %+v", rep.Items[0].Bases)
	}
}

func TestAddWithoutDefaultBoundaries(t *testing.T) {
	store := newTestStore(Instance{ID: "i1", Fields: map[string]Value{}})
	c := NewChecker("Order", store, nil, nil)

	// 不允许缺失、无默认值、无回填 => 不兼容（missing-required）。
	rep := c.CheckBatch([]FieldChange{{Name: "f", New: &FieldDef{
		Type: NewIntType(), Constraint: AnyValue()}}})
	if rep.Compatible || rep.Items[0].Reasons != KindMissingRequired {
		t.Fatalf("got compatible=%v reasons=%v", rep.Compatible, rep.Items[0].Reasons.Strings())
	}

	// 允许缺失 => 兼容。
	rep = c.CheckBatch([]FieldChange{{Name: "f", New: &FieldDef{
		Type: NewIntType(), Constraint: AnyValue(), AllowMissing: true}}})
	if !rep.Compatible {
		t.Fatalf("AllowMissing must be compatible")
	}

	// 不允许缺失，但有覆盖全部实例的回填 => 兼容。
	rep = c.CheckBatch([]FieldChange{{Name: "f", New: &FieldDef{
		Type: NewIntType(), Constraint: AnyValue()},
		Backfill: constBackfill{NewValue(int64(7))}}})
	if !rep.Compatible {
		t.Fatalf("complete backfill must be compatible: %+v", rep.Items[0].Bases)
	}

	// 回填规则在任一实例上失败 => 不兼容，即使只有一个实例失败也不允许放行。
	rep = c.CheckBatch([]FieldChange{{Name: "f", New: &FieldDef{
		Type: NewIntType(), Constraint: AnyValue()},
		Backfill: failingFor{"i1": {}}}})
	if rep.Compatible || rep.Items[0].Reasons != KindMissingRequired {
		t.Fatalf("partial backfill must be rejected, got %v", rep.Items[0].Reasons.Strings())
	}
}

type constBackfill struct{ v Value }

func (b constBackfill) Fill(InstanceID, map[string]Value) (Value, bool) { return b.v, true }

type failingFor map[InstanceID]struct{}

func (f failingFor) Fill(id InstanceID, _ map[string]Value) (Value, bool) {
	if _, bad := f[id]; bad {
		return Missing(), false
	}
	return NewValue(int64(1)), true
}

// ---- 收紧 / 放宽 -------------------------------------------------------

func TestTightenAllOrNothing(t *testing.T) {
	store := newTestStore(
		Instance{ID: "ok1", Fields: map[string]Value{"n": NewValue(int64(5))}},
		Instance{ID: "ok2", Fields: map[string]Value{"n": NewValue(int64(10))}},
		Instance{ID: "bad", Fields: map[string]Value{"n": NewValue(int64(42))}},
	)
	c := NewChecker("Order", store, nil, nil)
	fc := FieldChange{Name: "n",
		Old: intField(false, true, NewRangeConstraint(0, 100, true, true)),
		New: intField(false, true, NewRangeConstraint(0, 10, true, true))}

	rep := c.CheckBatch([]FieldChange{fc})
	if rep.Compatible {
		t.Fatalf("one violating live instance must reject the whole change")
	}
	if rep.Items[0].Reasons != KindConstraintValue {
		t.Fatalf("reasons=%v", rep.Items[0].Reasons.Strings())
	}
	found := false
	for _, b := range rep.Items[0].Bases {
		for _, id := range b.Instances {
			if id == "bad" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("violating instance must be evidenced, got %+v", rep.Items[0].Bases)
	}

	// 删除唯一违规实例后，同样的变更必须通过。
	if !store.Tombstone("bad") {
		t.Fatal("tombstone failed")
	}
	rep = c.CheckBatch([]FieldChange{fc})
	if !rep.Compatible {
		t.Fatalf("after violating instance is removed, change must be compatible")
	}
}

func TestLoosenAlwaysCompatibleNoScan(t *testing.T) {
	store := newTestStore(
		Instance{ID: "i1", Fields: map[string]Value{"n": NewValue(int64(5))}},
		Instance{ID: "i2", Fields: map[string]Value{"n": NewValue(int64(9))}},
	)
	store.ResetScanCounter()
	c := NewChecker("Order", store, nil, nil)
	rep := c.CheckBatch([]FieldChange{{Name: "n",
		Old: intField(false, true, NewRangeConstraint(0, 10, true, true)),
		New: intField(false, true, NewRangeConstraint(0, 100, true, true))}})
	if !rep.Compatible {
		t.Fatal("loosen must always be compatible")
	}
	// 放宽不需要扫描既有实例。
	got := 0
	for _, b := range rep.Items[0].Bases {
		if b.Check == "loosen-no-scan" {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("loosen-no-scan basis missing: %+v", rep.Items[0].Bases)
	}
}

// ---- 类型变更 ----------------------------------------------------------

func TestTypeChangeLosslessBoundaries(t *testing.T) {
	intOpen := func() *FieldDef { return intField(false, true, AnyValue()) }
	strOpen := func() *FieldDef {
		return &FieldDef{Type: NewStringType(), Constraint: AnyValue(),
			HasDefault: true, Default: NewValue("0")}
	}
	floatOpen := func() *FieldDef {
		return &FieldDef{Type: NewFloatType(), Constraint: AnyValue(),
			HasDefault: true, Default: NewValue(float64(0))}
	}

	// int -> string：全部 int 都有精确十进制表示，兼容。
	store := newTestStore(Instance{ID: "i1", Fields: map[string]Value{"x": NewValue(int64(123))}})
	c := NewChecker("O", store, nil, nil)
	if rep := c.CheckBatch([]FieldChange{{Name: "x", Old: intOpen(), New: strOpen()}}); !rep.Compatible {
		t.Fatalf("int->string must be lossless: %+v", rep.Items[0].Bases)
	}

	// float -> int：1.5 无法无损重解释 => 不兼容；2.0 可以。
	store = newTestStore(
		Instance{ID: "whole", Fields: map[string]Value{"x": NewValue(float64(2))}},
		Instance{ID: "frac", Fields: map[string]Value{"x": NewValue(1.5)}},
	)
	c = NewChecker("O", store, nil, nil)
	rep := c.CheckBatch([]FieldChange{{Name: "x", Old: floatOpen(), New: intOpen()}})
	if rep.Compatible || rep.Items[0].Reasons != KindIrreversibleType {
		t.Fatalf("fractional value must be irreversible, got %v", rep.Items[0].Reasons.Strings())
	}

	// 精确性反例：string "1.0" 不得被近似接受为 int。
	if NewIntType().CanReinterpret(NewStringType(), NewValue("1.0")) {
		t.Fatal(`"1.0" must not be losslessly reinterpreted as int`)
	}
	if !NewIntType().CanReinterpret(NewStringType(), NewValue("1")) {
		t.Fatal(`"1" must be losslessly reinterpreted as int`)
	}
}

// ---- 引用方语义漂移 ----------------------------------------------------

func TestReferenceDriftRejectsOtherwiseCompatibleChange(t *testing.T) {
	store := newTestStore(
		Instance{ID: "i1", Fields: map[string]Value{"n": NewValue(int64(5))}},
		Instance{ID: "i2", Fields: map[string]Value{"n": NewValue(int64(50))}},
	)
	refs := NewReferenceRegistry()
	// 链接判定旧语义：n < 10。放宽后旧判定在新语义下对 i2 会变化；
	// 未提供 NewJudgment（链接无法迁移），必须判语义漂移。
	refs.Add(LinkTypeReference{
		RefID:   "L1",
		ObjType: "Order",
		Field:   "n",
		// 链接判定依赖字段取值域本身：“值必须落在字段声明的取值域内”。
		// 取值域放宽后，i2=50 在旧语义下出界、新语义下入界，判定翻转。
		OldJudgment: func(v EffectiveView, sem *FieldSemantics) bool {
			if sem == nil {
				return false
			}
			n := v["n"].Raw().(int64)
			return sem.Constraint.Satisfies(NewValue(n))
		},
	})
	c := NewChecker("Order", store, refs, nil)
	rep := c.CheckBatch([]FieldChange{{Name: "n",
		Old: intField(false, true, NewRangeConstraint(0, 10, true, true)),
		New: intField(false, true, NewRangeConstraint(0, 100, true, true))}})
	if rep.Compatible {
		t.Fatal("reference drift must reject a change even if values are compatible")
	}
	if rep.Items[0].Reasons != KindReferenceDrift {
		t.Fatalf("reasons=%v", rep.Items[0].Reasons.Strings())
	}

	// 若引用方同步迁移了判定逻辑且在全部存活实例上结果一致，则放行。
	refs2 := NewReferenceRegistry()
	refs2.Add(LinkTypeReference{
		RefID: "L1", ObjType: "Order", Field: "n",
		OldJudgment: func(v EffectiveView, sem *FieldSemantics) bool {
			return v["n"].Raw().(int64) < 10
		},
		NewJudgment: func(v EffectiveView, sem *FieldSemantics) bool {
			return v["n"].Raw().(int64) < 10
		},
	})
	c2 := NewChecker("Order", store, refs2, nil)
	rep2 := c2.CheckBatch([]FieldChange{{Name: "n",
		Old: intField(false, true, NewRangeConstraint(0, 100, true, true)),
		New: intField(false, true, NewRangeConstraint(0, 200, true, true))}})
	if !rep2.Compatible {
		t.Fatalf("adapted reference with identical outcomes must pass: %+v", rep2.Items[0].Bases)
	}
}

// ---- 多类不兼容同时暴露 ------------------------------------------------

func TestMultipleIncompatKindsAllReturned(t *testing.T) {
	store := newTestStore(
		Instance{ID: "i1", Fields: map[string]Value{"n": NewValue(1.5)}},
		Instance{ID: "i3", Fields: map[string]Value{"n": NewValue(float64(99))}},
	)
	refs := NewReferenceRegistry()
	refs.Add(ActionReference{
		RefID: "act1", ObjType: "O", Field: "n", Phase: "pre",
		Judgment: func(v EffectiveView, sem *FieldSemantics) bool {
			if sem == nil {
				return false
			}
			// 前置条件同时依赖类型语义与取值域。
			_, isFloat := v["n"].Raw().(float64)
			inRange := sem.Constraint.Satisfies(v["n"])
			return isFloat && inRange
		},
	})
	c := NewChecker("O", store, refs, nil)
	fc := FieldChange{Name: "n",
		Old: &FieldDef{Type: NewFloatType(), Constraint: AnyValue(),
			HasDefault: true, Default: NewValue(0.0)},
		New: &FieldDef{Type: NewIntType(), Constraint: NewRangeConstraint(0, 10, true, true),
			HasDefault: true, Default: NewValue(int64(0))}}
	// i1=1.5 无法无损重解释（类型不可逆）；i3=99 可重解释为 int
	// 但落在新约束 [0,10] 之外（取值不满足）；动作前置条件依赖
	// “类型为 float 且在取值域内”，在新 int 语义下翻转（引用漂移）。
	rep := c.CheckBatch([]FieldChange{fc})
	if rep.Compatible {
		t.Fatal("must be incompatible")
	}
	want := KindIrreversibleType | KindConstraintValue | KindReferenceDrift
	if rep.Reasons != want {
		t.Fatalf("reasons=%v, want all of %v", rep.Reasons.Strings(), want.Strings())
	}
}

// ---- 审计记录 ----------------------------------------------------------

func TestAuditRecordsChangeBasisAndConclusion(t *testing.T) {
	store := newTestStore(Instance{ID: "i1", Fields: map[string]Value{"n": NewValue(int64(99))}})
	log := NewMemoryAuditLog()
	c := NewChecker("O", store, nil, log)
	c.CheckBatch([]FieldChange{{Name: "n",
		Old: intField(false, true, NewRangeConstraint(0, 100, true, true)),
		New: intField(false, true, NewRangeConstraint(0, 10, true, true))}})
	recs := log.Records()
	if len(recs) != 1 || recs[0].Compatible || recs[0].Field != "n" {
		t.Fatalf("bad audit record: %+v", recs)
	}
	if !reflect.DeepEqual(recs[0].Reasons, []string{"constraint-value-violated"}) {
		t.Fatalf("audit reasons=%v", recs[0].Reasons)
	}
	if len(recs[0].Bases) == 0 || recs[0].Category != "tighten-constraint" {
		t.Fatalf("audit must carry category and bases: %+v", recs[0])
	}
}
