package binding

import "testing"

func bijectiveSpec(linkType string) BindingSpec {
	return BindingSpec{
		LinkType:   linkType,
		LeftObject: "A", LeftField: "f",
		RightObject: "B", RightField: "g",
		Direction:      Bidirectional,
		Correspondence: Correspondence{Mapping: pairs("a", "x", "b", "y")},
	}
}

func newTestRegistry(t *testing.T) (*Registry, *MemoryInstanceStore) {
	t.Helper()
	store := NewMemoryInstanceStore()
	r := NewRegistry(store)
	r.DeclareField(*enumField("A", "f", false, "a", "b"))
	r.DeclareField(*enumField("B", "g", false, "x", "y"))
	return r, store
}

// 双向绑定声明时若仅单向可对应，必须在声明阶段被拒绝。
func TestDeclareRejectsOneWayBidirectional(t *testing.T) {
	r, _ := newTestRegistry(t)
	spec := bijectiveSpec("L1")
	spec.Correspondence.Mapping = pairs("a", "x", "b", "x") // 前向全函数，反向多对一
	err := r.DeclareBinding(spec)
	if err == nil {
		t.Fatal("bidirectional declaration with one-way correspondence must be rejected")
	}
	var rej *ErrBidirectionalRejected
	if !asErr(err, &rej) {
		t.Fatalf("want *ErrBidirectionalRejected, got %T", err)
	}
	if rej.Result.Verdict != VerdictOneWayOnly {
		t.Fatalf("want one_way_only, got %s", rej.Result.Verdict)
	}
	if _, ok := r.links["L1"]; ok {
		t.Fatal("rejected binding must not be registered")
	}

	// 同样的对应关系声明为单向则允许登记。
	spec2 := spec
	spec2.LinkType = "L2"
	spec2.Direction = LeftToRight
	if err := r.DeclareBinding(spec2); err != nil {
		t.Fatalf("one-way declaration should be accepted: %v", err)
	}
}

func asErr(err error, target **ErrBidirectionalRejected) bool {
	for err != nil {
		if e, ok := err.(*ErrBidirectionalRejected); ok {
			*target = e
			return true
		}
		break
	}
	return false
}

// 单向绑定反方向查询/写入被拒绝，且不会静默退化为双向。
func TestQueryDirectionDenied(t *testing.T) {
	r, _ := newTestRegistry(t)
	spec := bijectiveSpec("L")
	spec.Direction = LeftToRight
	if err := r.DeclareBinding(spec); err != nil {
		t.Fatal(err)
	}
	if res, err := r.Query("L", QLeftToRight); err != nil || res.Verdict != VerdictCompatible {
		t.Fatalf("forward query: %v %v", res.Verdict, err)
	}
	res, err := r.Query("L", QRightToLeft)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictDirectionDenied {
		t.Fatalf("reverse query must be denied, got %s (%s)", res.Verdict, res.Reason)
	}
}

// 字段删除导致定义失效，优先级高于其余三类不兼容。
func TestFieldDeletedTakesPrecedence(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.DeclareBinding(bijectiveSpec("L")); err != nil {
		t.Fatal(err)
	}
	// 先制造一个 one-way 变更，再删除字段；删除结论必须胜出。
	r.UpdateField(*enumField("A", "f", false, "a"))
	r.DeleteField("B", "g")
	res := r.Evaluate("L")
	if res.Verdict != VerdictFieldDeleted {
		t.Fatalf("want field_deleted, got %s (%s)", res.Verdict, res.Reason)
	}
	if res, _ := r.Query("L", QLeftToRight); res.Verdict != VerdictFieldDeleted {
		t.Fatalf("query after delete: want field_deleted, got %s", res.Verdict)
	}
}

// 各链接类型对同一字段的绑定兼容性状态相互隔离。
func TestLinkTypesAreolated(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.DeclareBinding(bijectiveSpec("L1")); err != nil {
		t.Fatal(err)
	}
	// L2 是左→右单向：左侧取值域缩小后仍可单向兼容。
	spec2 := bijectiveSpec("L2")
	spec2.Direction = LeftToRight
	if err := r.DeclareBinding(spec2); err != nil {
		t.Fatal(err)
	}
	// 右侧字段扩大：L1 的双向对应被破坏，L2 的单向对应仍成立。
	r.UpdateField(*enumField("B", "g", false, "x", "y", "z"))
	if got := r.Evaluate("L1").Verdict; got != VerdictOneWayOnly {
		t.Fatalf("L1 want one_way_only, got %s", got)
	}
	if got := r.Evaluate("L2").Verdict; got != VerdictCompatible {
		t.Fatalf("L2 must remain independently compatible, got %s", got)
	}
}

// 变更后阻止新查询，但既有实例不被撤销；核验遍历不超过存活总数。
func TestBreakChangeBlocksButKeepsInstances(t *testing.T) {
	r, store := newTestRegistry(t)
	if err := r.DeclareBinding(bijectiveSpec("L")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		store.Add(Instance{ID: string(rune('a' + i)), LinkType: "L"})
	}
	// 制造历史垃圾实例：历史上存在但已删除，绝不应进入核验遍历。
	store.Add(Instance{ID: "dead", LinkType: "L"})
	store.Remove("dead")

	before := store.Visits("L")
	r.UpdateField(*enumField("A", "f", false, "a", "b", "c"))
	res := r.Evaluate("L")
	if res.Verdict == VerdictCompatible {
		t.Fatalf("broken bijection must be incompatible, got %+v", res)
	}
	if res.InstancesLive != 5 {
		t.Fatalf("live=5 want, got %d", res.InstancesLive)
	}
	if res.InstancesSeen != 5 {
		t.Fatalf("seen must equal live snapshot 5, got %d", res.InstancesSeen)
	}
	if store.LiveCount("L") != 5 {
		t.Fatal("existing instances must not be revoked by incompatibility")
	}
	if delta := store.Visits("L") - before; delta != 10 {
		// 一次 Update 触发一次重新核验 + 一次 Evaluate 触发一次；每次恰好 5。
		t.Fatalf("each re-evaluation must walk at most live count; delta=%d", delta)
	}
	if res, _ := r.Query("L", QLeftToRight); res.Verdict == VerdictCompatible {
		t.Fatal("new binding queries must be blocked")
	}
}

// 类型在新版本不可比较 => 无法判定。
func TestIncomparableAfterUpdate(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.DeclareBinding(bijectiveSpec("L")); err != nil {
		t.Fatal(err)
	}
	r.UpdateField(FieldDef{
		ObjectType: "A", Name: "f",
		Type:    FieldType{Kind: KindInt},
		Allowed: []Value{{Raw: int64(1)}},
	})
	if got := r.Evaluate("L").Verdict; got != VerdictIncomparableTypes {
		t.Fatalf("want incomparable_types, got %s", got)
	}
}

// 每次核验都记录两侧字段定义、依据与结论。
func TestAuditRecords(t *testing.T) {
	r, _ := newTestRegistry(t)
	if err := r.DeclareBinding(bijectiveSpec("L")); err != nil {
		t.Fatal(err)
	}
	r.UpdateField(*enumField("A", "f", false, "a", "b", "c"))
	log := r.AuditLog()
	if len(log) != 2 {
		t.Fatalf("want 2 audit entries (declare + update), got %d", len(log))
	}
	last := log[1]
	if last.LeftField.Name != "f" || last.RightField.Name != "g" {
		t.Fatalf("audit must snapshot both field defs: %+v", last)
	}
	if last.Result.Verdict != VerdictOneWayOnly {
		t.Fatalf("want one_way_only verdict in audit, got %s", last.Result.Verdict)
	}
	if last.Basis.LeftDomain == nil {
		t.Fatal("audit basis must include the domain evidence")
	}
	if last.Trigger == "" {
		t.Fatal("audit entry must record trigger")
	}
}
