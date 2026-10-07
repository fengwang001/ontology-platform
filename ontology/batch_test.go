package ontology

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func sortedLive(e *Engine, t string) []Instance {
	live := e.LiveInstances(t)
	sort.Slice(live, func(i, j int) bool { return live[i].ID < live[j].ID })
	return live
}

func TestBatchPartialIncompatibleRejectedAtomically(t *testing.T) {
	e, _ := newFixture(50) // score ∈ [0,100]，取值 0..49
	// 引用方依赖 level 字段（尚不存在）——先加字段再登记引用。
	res := e.Commit([]FieldChange{{ObjectType: "Employee", Field: "level",
		New: &FieldDef{Name: "level", Type: IntType, HasDefault: true, Default: IntValue(1)}}})
	if !res.Accepted {
		t.Fatalf("setup commit rejected: %+v", res)
	}
	e.RegisterRef(FieldReference{ID: "link-x", Kind: LinkRef, Owner: "L",
		ObjectType: "Employee", Field: "level", Depends: Dependency{OnDefault: true}})

	defsBefore := e.ObjectTypeDef("Employee")
	liveBefore := sortedLive(e, "Employee")
	logBefore := len(e.Log())

	// 打包：① 放宽 score（兼容）② 收紧 score 到 [0,10]（不兼容：有实例取值 >10）
	// ③ 改 level 默认值（引用方语义漂移，不兼容）。
	batch := []FieldChange{
		{ObjectType: "Employee", Field: "score",
			Old: intField("score", NumRange(0, 100)), New: intField("score", NumRange(0, 200))},
		{ObjectType: "Employee", Field: "score",
			Old: intField("score", NumRange(0, 100)), New: intField("score", NumRange(0, 10))},
		{ObjectType: "Employee", Field: "level",
			Old: &FieldDef{Name: "level", Type: IntType, HasDefault: true, Default: IntValue(1)},
			New: &FieldDef{Name: "level", Type: IntType, HasDefault: true, Default: IntValue(2)}},
	}
	res = e.Commit(batch)
	if res.Accepted {
		t.Fatal("batch with incompatible items must be rejected")
	}
	want := InstanceConstraintViolation | ExternalSemanticDrift
	if res.Categories != want {
		t.Fatalf("categories %v, want %v", res.Categories, want)
	}
	if len(res.Decisions) != 3 {
		t.Fatalf("want 3 decisions, got %d", len(res.Decisions))
	}
	if !res.Decisions[0].Compatible {
		t.Fatalf("item 0 (loosen) should be individually compatible: %+v", res.Decisions[0])
	}

	// 零状态变化：字段定义、实例数据完全一致，兼容项不得单独生效。
	if !reflect.DeepEqual(defsBefore, e.ObjectTypeDef("Employee")) {
		t.Fatal("field definitions changed despite rejection")
	}
	if !reflect.DeepEqual(liveBefore, sortedLive(e, "Employee")) {
		t.Fatal("instance data changed despite rejection")
	}
	if len(e.Log()) != logBefore+1 {
		t.Fatal("rejected batch must still be recorded exactly once in the log")
	}
}

func TestBatchAllCategoriesReported(t *testing.T) {
	e, _ := newFixture(5)
	e.RegisterRef(FieldReference{ID: "act-y", Kind: ActionRef, Owner: "A",
		ObjectType: "Employee", Field: "score", Depends: Dependency{OnConstraint: true}})

	// 直接写入存储，模拟约束生效前遗留的越界实例。
	e.Store().Put(Instance{ID: "big", Type: "Employee",
		Values: map[string]Value{"score": IntValue(1<<53 + 1)}})

	batch := []FieldChange{
		// 触发 InstanceConstraintViolation
		{ObjectType: "Employee", Field: "score",
			Old: intField("score", NumRange(0, 100)), New: intField("score", NumRange(0, 1))},
		// 触发 IrreversibleTypeConversion（1<<53+1 无法精确表示）
		{ObjectType: "Employee", Field: "score",
			Old: intField("score", NumRange(0, 100)),
			New: &FieldDef{Name: "score", Type: FloatType, Nullable: true}},
		// 触发 MissingValueNotAllowed
		{ObjectType: "Employee", Field: "tag",
			New: &FieldDef{Name: "tag", Type: StringType}},
		// 触发 ExternalSemanticDrift（且实例校验本身兼容的放宽）
		{ObjectType: "Employee", Field: "score",
			Old: intField("score", NumRange(0, 100)), New: intField("score", NumRange(0, 10000))},
	}
	res := e.Commit(batch)
	if res.Accepted {
		t.Fatal("must be rejected")
	}
	want := InstanceConstraintViolation | IrreversibleTypeConversion | MissingValueNotAllowed | ExternalSemanticDrift
	if res.Categories != want {
		t.Fatalf("categories %v, want all four: %v", res.Categories, want)
	}
}

func TestBatchAllCompatibleAppliedAtomically(t *testing.T) {
	e, _ := newFixture(3)
	batch := []FieldChange{
		{ObjectType: "Employee", Field: "score",
			Old: intField("score", NumRange(0, 100)), New: intField("score", NumRange(0, 200))},
		{ObjectType: "Employee", Field: "nick",
			New:      &FieldDef{Name: "nick", Type: StringType},
			Backfill: func(id string) (Value, bool) { return StringValue("n-" + id), true }},
	}
	res := e.Commit(batch)
	if !res.Accepted {
		t.Fatalf("all-compatible batch must be accepted: %+v", res)
	}
	defs := e.ObjectTypeDef("Employee")
	if defs["score"].Constraint.Max == nil || *defs["score"].Constraint.Max != 200 {
		t.Fatalf("loosen not applied: %+v", defs["score"])
	}
	if _, ok := defs["nick"]; !ok {
		t.Fatal("new field not added")
	}
	for _, in := range e.LiveInstances("Employee") {
		if v, ok := in.Values["nick"]; !ok || v.S != "n-"+in.ID {
			t.Fatalf("backfill not applied to %s: %v", in.ID, in.Values)
		}
	}
}

func TestChangeTypeAppliedToInstances(t *testing.T) {
	e := NewEngine()
	ot := NewObjectType("T")
	ot.Fields["n"] = &FieldDef{Name: "n", Type: IntType, Nullable: true}
	e.RegisterObjectType(ot)
	for i := 0; i < 5; i++ {
		mustWrite(t, e, Instance{ID: fmt.Sprintf("i%d", i), Type: "T", Values: map[string]Value{"n": IntValue(int64(i))}})
	}
	res := e.Commit([]FieldChange{{ObjectType: "T", Field: "n",
		Old: &FieldDef{Name: "n", Type: IntType, Nullable: true},
		New: &FieldDef{Name: "n", Type: FloatType, Nullable: true}}})
	if !res.Accepted {
		t.Fatalf("want accepted: %+v", res)
	}
	for _, in := range e.LiveInstances("T") {
		if in.Values["n"].Kind != KindFloat {
			t.Fatalf("instance %s value not reinterpreted: %+v", in.ID, in.Values["n"])
		}
	}
}

func TestStaleBaseRejected(t *testing.T) {
	e, _ := newFixture(3)
	stale := intField("score", NumRange(0, 999)) // 与当前 [0,100] 不一致
	res := e.Commit([]FieldChange{{ObjectType: "Employee", Field: "score",
		Old: stale, New: intField("score", NumRange(0, 2000))}})
	if res.Accepted {
		t.Fatal("stale-base change must be rejected")
	}
}
