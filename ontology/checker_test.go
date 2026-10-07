package ontology

import (
	"fmt"
	"testing"
)

// newFixture 构造一个含 n 个存活实例的对象类型 score:int[0,100]。
func newFixture(n int) (*Engine, *Checker) {
	e := NewEngine()
	ot := NewObjectType("Employee")
	ot.Fields["score"] = &FieldDef{Name: "score", Type: IntType, Constraint: NumRange(0, 100), Nullable: true}
	e.RegisterObjectType(ot)
	for i := 0; i < n; i++ {
		if err := e.Write(Instance{ID: fmt.Sprintf("e%03d", i), Type: "Employee",
			Values: map[string]Value{"score": IntValue(int64(i % 101))}}); err != nil {
			panic(err)
		}
	}
	return e, &Checker{Store: e.Store(), Refs: e.Refs()}
}

func TestTightenBoundary(t *testing.T) {
	_, c := newFixture(50) // 取值 0..49
	// 收紧到 [0,49]：全部满足，兼容。
	d, err := c.Check(FieldChange{ObjectType: "Employee", Field: "score",
		Old: intField("score", NumRange(0, 100)), New: intField("score", NumRange(0, 49))})
	if err != nil || !d.Compatible {
		t.Fatalf("want compatible, got %+v err=%v", d, err)
	}
	if d.CheckedInstances != 50 {
		t.Fatalf("checked %d instances, want 50", d.CheckedInstances)
	}
	// 收紧到 [0,48]：取值 49 的一个实例不满足，整次不兼容。
	d, err = c.Check(FieldChange{ObjectType: "Employee", Field: "score",
		Old: intField("score", NumRange(0, 100)), New: intField("score", NumRange(0, 48))})
	if err != nil {
		t.Fatal(err)
	}
	if d.Compatible || !d.Categories.Has(InstanceConstraintViolation) {
		t.Fatalf("want InstanceConstraintViolation, got %+v", d)
	}
	if len(d.ViolatingInstances) != 1 {
		t.Fatalf("want exactly 1 violating instance, got %v", d.ViolatingInstances)
	}
}

func TestLoosenAlwaysCompatibleNoScan(t *testing.T) {
	_, c := newFixture(50)
	d, err := c.Check(FieldChange{ObjectType: "Employee", Field: "score",
		Old: intField("score", NumRange(10, 50)), New: intField("score", NumRange(0, 100))})
	if err != nil || !d.Compatible {
		t.Fatalf("want compatible, got %+v err=%v", d, err)
	}
	if d.CheckedInstances != 0 {
		t.Fatalf("loosen must not scan instances, checked %d", d.CheckedInstances)
	}
}

func TestChangeTypeLossless(t *testing.T) {
	e := NewEngine()
	ot := NewObjectType("T")
	ot.Fields["n"] = &FieldDef{Name: "n", Type: IntType, Nullable: true}
	e.RegisterObjectType(ot)
	mustWrite(t, e, Instance{ID: "a", Type: "T", Values: map[string]Value{"n": IntValue(42)}})
	mustWrite(t, e, Instance{ID: "b", Type: "T", Values: map[string]Value{"n": IntValue(1<<53 + 1)}}) // 超出 float64 精确整数范围

	c := &Checker{Store: e.Store(), Refs: e.Refs()}
	old := &FieldDef{Name: "n", Type: IntType, Nullable: true}
	newF := &FieldDef{Name: "n", Type: FloatType, Nullable: true}
	d, err := c.Check(FieldChange{ObjectType: "T", Field: "n", Old: old, New: newF})
	if err != nil {
		t.Fatal(err)
	}
	if d.Compatible || !d.Categories.Has(IrreversibleTypeConversion) {
		t.Fatalf("want IrreversibleTypeConversion, got %+v", d)
	}
	if len(d.ViolatingInstances) != 1 || d.ViolatingInstances[0] != "b" {
		t.Fatalf("want only instance b violating, got %v", d.ViolatingInstances)
	}

	// 删除不可转换的实例后，同样的变更变为兼容。
	e.Store().Delete("T", "b")
	d, err = c.Check(FieldChange{ObjectType: "T", Field: "n", Old: old, New: newF})
	if err != nil || !d.Compatible {
		t.Fatalf("want compatible after delete, got %+v err=%v", d, err)
	}
	if d.CheckedInstances != 1 {
		t.Fatalf("checked %d, want 1 (only live instances)", d.CheckedInstances)
	}
}

func TestChangeTypeStringToIntNeverLossless(t *testing.T) {
	e := NewEngine()
	ot := NewObjectType("T")
	ot.Fields["s"] = &FieldDef{Name: "s", Type: StringType, Nullable: true}
	e.RegisterObjectType(ot)
	mustWrite(t, e, Instance{ID: "a", Type: "T", Values: map[string]Value{"s": StringValue("123")}})
	c := &Checker{Store: e.Store(), Refs: e.Refs()}
	d, err := c.Check(FieldChange{ObjectType: "T", Field: "s",
		Old: &FieldDef{Name: "s", Type: StringType, Nullable: true},
		New: &FieldDef{Name: "s", Type: IntType, Nullable: true}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Compatible || !d.Categories.Has(IrreversibleTypeConversion) {
		t.Fatalf("want IrreversibleTypeConversion, got %+v", d)
	}
}

func TestAddFieldWithoutDefault(t *testing.T) {
	_, c := newFixture(10)
	newNullable := &FieldDef{Name: "nick", Type: StringType, Nullable: true}
	d, err := c.Check(FieldChange{ObjectType: "Employee", Field: "nick", New: newNullable})
	if err != nil || !d.Compatible {
		t.Fatalf("nullable add must be compatible, got %+v err=%v", d, err)
	}

	newRequired := &FieldDef{Name: "nick", Type: StringType}
	d, err = c.Check(FieldChange{ObjectType: "Employee", Field: "nick", New: newRequired})
	if err != nil {
		t.Fatal(err)
	}
	if d.Compatible || !d.Categories.Has(MissingValueNotAllowed) {
		t.Fatalf("want MissingValueNotAllowed, got %+v", d)
	}

	// 携带回填规则后转为兼容。
	d, err = c.Check(FieldChange{ObjectType: "Employee", Field: "nick", New: newRequired,
		Backfill: func(id string) (Value, bool) { return StringValue("anon"), true }})
	if err != nil || !d.Compatible {
		t.Fatalf("backfill add must be compatible, got %+v err=%v", d, err)
	}
}

func TestAddFieldWithDefaultAlwaysCompatible(t *testing.T) {
	_, c := newFixture(10)
	d, err := c.Check(FieldChange{ObjectType: "Employee", Field: "level",
		New: &FieldDef{Name: "level", Type: IntType, HasDefault: true, Default: IntValue(1)}})
	if err != nil || !d.Compatible || d.CheckedInstances != 0 {
		t.Fatalf("want compatible without scan, got %+v err=%v", d, err)
	}
}

func TestRemoveField(t *testing.T) {
	_, c := newFixture(10)
	d, err := c.Check(FieldChange{ObjectType: "Employee", Field: "score", Old: intField("score", NumRange(0, 100))})
	if err != nil || !d.Compatible {
		t.Fatalf("remove without refs must be compatible, got %+v err=%v", d, err)
	}
}

func TestExternalSemanticDrift(t *testing.T) {
	e, c := newFixture(10)
	// 链接类型依赖 score 的约束做链接判定；动作依赖其类型做前置条件。
	e.RegisterRef(FieldReference{ID: "link-1", Kind: LinkRef, Owner: "WorksWith",
		ObjectType: "Employee", Field: "score", Depends: Dependency{OnConstraint: true}})
	e.RegisterRef(FieldReference{ID: "act-1", Kind: ActionRef, Owner: "Promote",
		ObjectType: "Employee", Field: "score", Depends: Dependency{OnType: true}})

	// 收紧约束：实例全部满足，但链接引用方语义漂移，整体不兼容。
	d, err := c.Check(FieldChange{ObjectType: "Employee", Field: "score",
		Old: intField("score", NumRange(0, 100)), New: intField("score", NumRange(0, 49))})
	if err != nil {
		t.Fatal(err)
	}
	if d.Compatible || !d.Categories.Has(ExternalSemanticDrift) {
		t.Fatalf("want ExternalSemanticDrift, got %+v", d)
	}
	if d.Categories.Has(InstanceConstraintViolation) {
		t.Fatalf("instances all satisfy; unexpected category in %+v", d.Categories)
	}
	if len(d.DriftedRefs) != 1 || d.DriftedRefs[0] != "link-1" {
		t.Fatalf("want link-1 drifted, got %v", d.DriftedRefs)
	}

	// 类型变更：实例取值可无损转换，但动作引用方依赖类型，整体不兼容。
	d, err = c.Check(FieldChange{ObjectType: "Employee", Field: "score",
		Old: intField("score", NumRange(0, 100)),
		New: &FieldDef{Name: "score", Type: FloatType, Constraint: NumRange(0, 100), Nullable: true}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Compatible || !d.Categories.Has(ExternalSemanticDrift) {
		t.Fatalf("want ExternalSemanticDrift, got %+v", d)
	}

	// 删除字段：存在性变化对任何引用方都必然构成漂移。
	d, err = c.Check(FieldChange{ObjectType: "Employee", Field: "score", Old: intField("score", NumRange(0, 100))})
	if err != nil {
		t.Fatal(err)
	}
	if d.Compatible || !d.Categories.Has(ExternalSemanticDrift) {
		t.Fatalf("want ExternalSemanticDrift on remove, got %+v", d)
	}
}

// TestLiveCountBound 证明判定开销只与当前存活实例数有关，
// 与历史上被删除/迁移的实例总量无关。
func TestLiveCountBound(t *testing.T) {
	e := NewEngine()
	ot := NewObjectType("T")
	ot.Fields["n"] = &FieldDef{Name: "n", Type: IntType, Constraint: NumRange(0, 1000), Nullable: true}
	e.RegisterObjectType(ot)
	const live, deleted, migrated = 7, 5000, 3000
	for i := 0; i < live+deleted+migrated; i++ {
		mustWrite(t, e, Instance{ID: fmt.Sprintf("i%d", i), Type: "T", Values: map[string]Value{"n": IntValue(1)}})
	}
	for i := live; i < live+deleted; i++ {
		e.Store().Delete("T", fmt.Sprintf("i%d", i))
	}
	for i := live + deleted; i < live+deleted+migrated; i++ {
		e.Store().MigrateOut("T", fmt.Sprintf("i%d", i))
	}
	if got := e.Store().LiveCount("T"); got != live {
		t.Fatalf("live count %d, want %d", got, live)
	}
	c := &Checker{Store: e.Store(), Refs: e.Refs()}
	d, err := c.Check(FieldChange{ObjectType: "T", Field: "n",
		Old: intField("n", NumRange(0, 1000)), New: intField("n", NumRange(0, 500))})
	if err != nil {
		t.Fatal(err)
	}
	if d.CheckedInstances != live {
		t.Fatalf("checked %d instances, want exactly %d (history of %d must not add cost)",
			d.CheckedInstances, live, deleted+migrated)
	}
	if !d.Compatible {
		t.Fatalf("want compatible, got %+v", d)
	}
}

func mustWrite(t *testing.T, e *Engine, in Instance) {
	t.Helper()
	if err := e.Write(in); err != nil {
		t.Fatalf("write %s: %v", in.ID, err)
	}
}
