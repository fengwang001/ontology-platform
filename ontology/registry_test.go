package ontology

import (
	"errors"
	"testing"
)

func setupRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.RegisterObjectType("A", []FieldDef{
		{ID: "fa", Type: TString, Enum: []Value{StrValue("a"), StrValue("b")}},
		{ID: "fa2", Type: TString, Enum: []Value{StrValue("a"), StrValue("b")}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterObjectType("B", []FieldDef{
		{ID: "fb", Type: TString, Enum: []Value{StrValue("a"), StrValue("b")}},
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

func twoWayDecl(id string, left, right FieldRef) BindingDecl {
	return BindingDecl{
		LinkTypeID:     id,
		Left:           left,
		Right:          right,
		Direction:      TwoWay,
		Correspondence: Correspondence{Kind: Identity},
		Missing:        MissingPolicy{Kind: MissingForbidden},
	}
}

func TestDeclareRejectsTwoWayWhenOnlyOneWayHolds(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterObjectType("A", []FieldDef{
		{ID: "fa", Type: TString, Enum: []Value{StrValue("a")}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterObjectType("B", []FieldDef{
		{ID: "fb", Type: TString, Enum: []Value{StrValue("a"), StrValue("b")}},
	}); err != nil {
		t.Fatal(err)
	}
	decl := twoWayDecl("lt", FieldRef{"A", "fa"}, FieldRef{"B", "fb"})
	err := r.DeclareLinkType(decl)
	var incErr *IncompatibleError
	if !errors.As(err, &incErr) {
		t.Fatalf("expected IncompatibleError, got %v", err)
	}
	if incErr.Kind != IncompatDeclaredTwoWayOnlyOneWay {
		t.Fatalf("kind = %v, want %v", incErr.Kind, IncompatDeclaredTwoWayOnlyOneWay)
	}
	// 同样的对应关系声明为单向则通过。
	decl.Direction = LeftToRight
	decl.LinkTypeID = "lt-oneway"
	if err := r.DeclareLinkType(decl); err != nil {
		t.Fatalf("one-way declaration should pass, got %v", err)
	}
}

func TestOneWayBindingRejectsReverseDirection(t *testing.T) {
	r := setupRegistry(t)
	decl := twoWayDecl("lt", FieldRef{"A", "fa"}, FieldRef{"B", "fb"})
	decl.Direction = LeftToRight
	if err := r.DeclareLinkType(decl); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueryBinding("lt", LeftToRight); err != nil {
		t.Fatalf("declared direction query should pass, got %v", err)
	}
	if _, err := r.QueryBinding("lt", RightToLeft); !errors.Is(err, ErrDirectionNotAllowed) {
		t.Fatalf("reverse query must be rejected, got %v", err)
	}
}

func TestFieldUpdateTriggersRevalidationAndBlocksNewQueries(t *testing.T) {
	r := setupRegistry(t)
	if err := r.DeclareLinkType(twoWayDecl("lt", FieldRef{"A", "fa"}, FieldRef{"B", "fb"})); err != nil {
		t.Fatal(err)
	}
	// 建立一条链接实例，随后字段变更使绑定不再双向唯一。
	if err := r.CreateLinkInstance("lt", "inst-1", "obj-a", "obj-b"); err != nil {
		t.Fatal(err)
	}
	_, err := r.UpdateField("B", FieldDef{ID: "fb", Type: TString, Enum: []Value{StrValue("a"), StrValue("b"), StrValue("c")}})
	if err != nil {
		t.Fatal(err)
	}
	res, ok := r.Compatibility("lt")
	if !ok || res.Kind != IncompatDeclaredTwoWayOnlyOneWay {
		t.Fatalf("compatibility = %v, want declared-two-way-but-only-one-way", res.Kind)
	}
	if _, err := r.QueryBinding("lt", LeftToRight); !errors.Is(err, ErrBindingIncompatible) {
		t.Fatalf("new binding query must be blocked, got %v", err)
	}
	if err := r.CreateLinkInstance("lt", "inst-2", "x", "y"); !errors.Is(err, ErrBindingIncompatible) {
		t.Fatalf("new instance must be blocked, got %v", err)
	}
	// 已有实例不被撤销。
	if n := r.LiveInstanceCount("lt"); n != 1 {
		t.Fatalf("existing instance must be retained, live = %d", n)
	}
	// 审计记录包含两侧字段定义、检查依据与结论。
	log := r.AuditLog()
	if len(log) == 0 {
		t.Fatal("audit log must not be empty")
	}
	last := log[len(log)-1]
	if last.Trigger != "field-update" || !last.LeftFieldPresent || !last.RightFieldPresent {
		t.Fatalf("audit record incomplete: %+v", last)
	}
	if last.Result.Basis.Correspondence.Kind != Identity {
		t.Fatalf("audit must record correspondence basis: %+v", last.Result.Basis)
	}
}

func TestFieldDeleteHasPriorityOverOtherVerdicts(t *testing.T) {
	r := setupRegistry(t)
	if err := r.DeclareLinkType(twoWayDecl("lt", FieldRef{"A", "fa"}, FieldRef{"B", "fb"})); err != nil {
		t.Fatal(err)
	}
	// 先让绑定因取值空间变化而不兼容，再删除字段：结论必须转为定义失效。
	if _, err := r.UpdateField("B", FieldDef{ID: "fb", Type: TString, Enum: []Value{StrValue("a")}}); err != nil {
		t.Fatal(err)
	}
	if res, _ := r.Compatibility("lt"); res.Kind == IncompatFieldDeleted {
		t.Fatal("precondition: should be incompatible but not deleted yet")
	}
	if _, err := r.DeleteField("B", "fb"); err != nil {
		t.Fatal(err)
	}
	res, _ := r.Compatibility("lt")
	if res.Kind != IncompatFieldDeleted {
		t.Fatalf("kind = %v, want field-deleted (priority)", res.Kind)
	}
	if _, err := r.QueryBinding("lt", LeftToRight); !errors.Is(err, ErrBindingInvalid) {
		t.Fatalf("query on invalid binding must return ErrBindingInvalid, got %v", err)
	}
}

func TestCompatibilityStateIsIsolatedPerLinkType(t *testing.T) {
	r := setupRegistry(t)
	// 两个链接类型共用字段 A.fa，但对侧字段不同。
	if err := r.DeclareLinkType(twoWayDecl("lt-1", FieldRef{"A", "fa"}, FieldRef{"B", "fb"})); err != nil {
		t.Fatal(err)
	}
	decl2 := twoWayDecl("lt-2", FieldRef{"A", "fa"}, FieldRef{"A", "fa2"})
	if err := r.DeclareLinkType(decl2); err != nil {
		t.Fatal(err)
	}
	// 变更 B.fb 只应影响 lt-1。
	if _, err := r.UpdateField("B", FieldDef{ID: "fb", Type: TString, Enum: []Value{StrValue("a")}}); err != nil {
		t.Fatal(err)
	}
	res1, _ := r.Compatibility("lt-1")
	res2, _ := r.Compatibility("lt-2")
	if res1.Kind == Compatible {
		t.Fatal("lt-1 should become incompatible")
	}
	if res2.Kind != Compatible {
		t.Fatalf("lt-2 must stay compatible, got %v", res2.Kind)
	}
	if _, err := r.QueryBinding("lt-2", RightToLeft); err != nil {
		t.Fatalf("lt-2 query should still pass, got %v", err)
	}
}

func TestRevalidationVisitsOnlyLiveInstancesOfThatLinkType(t *testing.T) {
	r := setupRegistry(t)
	if err := r.DeclareLinkType(twoWayDecl("lt-1", FieldRef{"A", "fa"}, FieldRef{"B", "fb"})); err != nil {
		t.Fatal(err)
	}
	if err := r.DeclareLinkType(twoWayDecl("lt-2", FieldRef{"A", "fa2"}, FieldRef{"B", "fb"})); err != nil {
		t.Fatal(err)
	}
	// lt-1 有 3 条存活实例，lt-2 有 50 条；另有 1 条已删除的 lt-1 实例。
	for i := 0; i < 3; i++ {
		if err := r.CreateLinkInstance("lt-1", string(rune('a'+i)), "x", "y"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.CreateLinkInstance("lt-1", "removed", "x", "y"); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveLinkInstance("lt-1", "removed"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if err := r.CreateLinkInstance("lt-2", string(rune(i)), "x", "y"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.UpdateField("A", FieldDef{ID: "fa", Type: TString, Enum: []Value{StrValue("a"), StrValue("b")}}); err != nil {
		t.Fatal(err)
	}
	for _, rec := range r.AuditLog() {
		if rec.Trigger != "field-update" || rec.LinkTypeID != "lt-1" {
			continue
		}
		if rec.InstancesVisited != 3 {
			t.Fatalf("instances visited = %d, want exactly the 3 live instances of lt-1", rec.InstancesVisited)
		}
		return
	}
	t.Fatal("no revalidation audit record found for lt-1")
}

func TestMissingPolicyIsFixedAtDeclaration(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterObjectType("A", []FieldDef{
		{ID: "fa", Type: TString, Nullable: true, Enum: []Value{StrValue("a")}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterObjectType("B", []FieldDef{
		{ID: "fb", Type: TString, Nullable: true, Enum: []Value{StrValue("a")}},
	}); err != nil {
		t.Fatal(err)
	}
	decl := twoWayDecl("lt", FieldRef{"A", "fa"}, FieldRef{"B", "fb"})
	decl.Missing = MissingPolicy{Kind: MissingToMissing}
	if err := r.DeclareLinkType(decl); err != nil {
		t.Fatal(err)
	}
	// 重复声明同一链接类型被拒绝：对应方式与缺失策略在建立后不可变。
	decl.Missing = MissingPolicy{Kind: MissingForbidden}
	if err := r.DeclareLinkType(decl); !errors.Is(err, ErrLinkTypeExists) {
		t.Fatalf("re-declaration must be rejected, got %v", err)
	}
}

func TestDeclareRejectsMissingForbiddenOnNullableField(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterObjectType("A", []FieldDef{
		{ID: "fa", Type: TString, Nullable: true, Enum: []Value{StrValue("a")}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterObjectType("B", []FieldDef{
		{ID: "fb", Type: TString, Enum: []Value{StrValue("a")}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.DeclareLinkType(twoWayDecl("lt", FieldRef{"A", "fa"}, FieldRef{"B", "fb"})); err == nil {
		t.Fatal("missing-forbidden on nullable field must be rejected at declaration")
	}
}
