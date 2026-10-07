package ontology

import "testing"

// 场景一：属性重命名 + 取值变化。实例层必须报告为同一属性的
// 标识变化（OldKey != NewKey，PropID 相同），不得报成删除加新增。
func TestRenameIsIdentityChangeNotDeleteAdd(t *testing.T) {
	b0 := personBuilder()
	b0.PutObject("person", IntValue(1), map[string]Value{
		"p-name": StringValue("alice"), "p-age": IntValue(30),
	})
	b0.PutObject("person", IntValue(2), map[string]Value{
		"p-name": StringValue("bob"), "p-age": IntValue(40),
	})
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.RenameProperty("person", "p-name", "fullName")
	b1.PutObject("person", IntValue(1), map[string]Value{
		"p-name": StringValue("alice2"), "p-age": IntValue(30),
	})
	s1 := b1.Build()

	res := mustCompare(t, s0, s1)

	td := res.Schema.ObjectTypeDiff("person")
	if td == nil || td.Disposition != DispModified {
		t.Fatalf("expected person type modified, got %+v", td)
	}
	pd := findPropDiff(td.Props, "p-name")
	if pd == nil || pd.Renamed == nil || pd.Renamed.OldKey != "name" || pd.Renamed.NewKey != "fullName" {
		t.Fatalf("expected rename dimension on p-name, got %+v", pd)
	}
	if pd.Retyped != nil {
		t.Fatalf("unexpected retype dimension: %+v", pd.Retyped)
	}

	if len(res.Instances.Objects) != 1 {
		t.Fatalf("expected exactly 1 object diff, got %+v", res.Instances.Objects)
	}
	od := findObjectDiff(res.Instances, objKey("person", IntValue(1)))
	if od == nil || od.Kind != ObjectUpdated {
		t.Fatalf("expected obj1 updated, got %+v", od)
	}
	if len(od.Changes) != 1 {
		t.Fatalf("expected exactly 1 property change, got %+v", od.Changes)
	}
	pc := od.Changes[0]
	if pc.PropID != "p-name" || pc.Kind != ChangeValueChanged {
		t.Fatalf("expected value_changed on p-name, got %+v", pc)
	}
	if pc.OldKey != "name" || pc.NewKey != "fullName" {
		t.Fatalf("identity change must be visible as OldKey/NewKey, got %+v", pc)
	}
	if pc.OldValue == nil || pc.OldValue.Str != "alice" || pc.NewValue == nil || pc.NewValue.Str != "alice2" {
		t.Fatalf("value change across rename must be compared, got %+v", pc)
	}
	// 未变化的对象不得出现。
	if findObjectDiff(res.Instances, objKey("person", IntValue(2))) != nil {
		t.Fatalf("unchanged object must not appear in diff")
	}
}

func findPropDiff(props []PropertyDiff, id string) *PropertyDiff {
	for i := range props {
		if props[i].PropID == id {
			return &props[i]
		}
	}
	return nil
}

// 场景二：属性重命名与类型收紧同时发生。结构层必须给出两个独立
// 维度；旧值按新类型解读不再有效时，实例层报告类型不兼容，
// 而不是取值真实变化。
func TestRenameAndNarrowingTogether(t *testing.T) {
	b0 := NewBuilder("L", 1)
	b0.AddObjectType("item", "Item",
		Property{ID: "pk", Key: "id", Type: TypeInt},
		Property{ID: "p-score", Key: "score", Type: TypeFloat},
	)
	b0.PutObject("item", IntValue(1), map[string]Value{"p-score": FloatValue(1.5)})
	b0.PutObject("item", IntValue(2), map[string]Value{"p-score": FloatValue(2.0)})
	b0.PutObject("item", IntValue(3), map[string]Value{"p-score": FloatValue(3.5)})
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.RenameProperty("item", "p-score", "rating")
	b1.RetypeProperty("item", "p-score", TypeInt) // 收紧：旧 float 值被平台抹除
	b1.PutObject("item", IntValue(3), map[string]Value{"p-score": IntValue(5)})
	s1 := b1.Build()

	res := mustCompare(t, s0, s1)

	td := res.Schema.ObjectTypeDiff("item")
	pd := findPropDiff(td.Props, "p-score")
	if pd == nil {
		t.Fatalf("expected prop diff for p-score")
	}
	// 标识变化与类型变化是两个独立可分辨的维度。
	if pd.Renamed == nil || pd.Renamed.OldKey != "score" || pd.Renamed.NewKey != "rating" {
		t.Fatalf("missing rename dimension: %+v", pd)
	}
	if pd.Retyped == nil || pd.Retyped.OldType != TypeFloat || pd.Retyped.NewType != TypeInt || pd.Retyped.Class != RetypeNarrowing {
		t.Fatalf("missing retype dimension: %+v", pd)
	}

	if len(res.Instances.Objects) != 3 {
		t.Fatalf("expected 3 object diffs, got %+v", res.Instances.Objects)
	}
	for _, pk := range []int64{1, 2, 3} {
		od := findObjectDiff(res.Instances, objKey("item", IntValue(pk)))
		if od == nil || od.Kind != ObjectUpdated {
			t.Fatalf("expected obj %d updated, got %+v", pk, od)
		}
		pc := findPropChange(od.Changes, "p-score")
		if pc == nil || pc.Kind != ChangeTypeIncompatible {
			t.Fatalf("obj %d: expected type_incompatible, got %+v", pk, pc)
		}
		if pc.OldKey != "score" || pc.NewKey != "rating" {
			t.Fatalf("obj %d: identity change must accompany type change, got %+v", pk, pc)
		}
	}
	// obj3 的新值仍随类型不兼容差异一并给出。
	od3 := findObjectDiff(res.Instances, objKey("item", IntValue(3)))
	pc3 := findPropChange(od3.Changes, "p-score")
	if pc3.NewValue == nil || pc3.NewValue.Int != 5 {
		t.Fatalf("obj3: expected new value 5 attached, got %+v", pc3)
	}
}

// 场景三：类型拓宽（int→float）下旧值依然有效，取值真实变化
// 报告为 value_changed；数值相等的变化不产生差异。
func TestWideningKeepsValuesComparable(t *testing.T) {
	b0 := NewBuilder("L", 1)
	b0.AddObjectType("item", "Item",
		Property{ID: "pk", Key: "id", Type: TypeInt},
		Property{ID: "p-qty", Key: "qty", Type: TypeInt},
	)
	b0.PutObject("item", IntValue(1), map[string]Value{"p-qty": IntValue(3)})
	b0.PutObject("item", IntValue(2), map[string]Value{"p-qty": IntValue(3)})
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.RetypeProperty("item", "p-qty", TypeFloat)
	b1.PutObject("item", IntValue(1), map[string]Value{"p-qty": FloatValue(3.0)}) // 数值未变
	b1.PutObject("item", IntValue(2), map[string]Value{"p-qty": FloatValue(3.5)}) // 真实变化
	s1 := b1.Build()

	res := mustCompare(t, s0, s1)

	pd := findPropDiff(res.Schema.ObjectTypeDiff("item").Props, "p-qty")
	if pd.Retyped == nil || pd.Retyped.Class != RetypeWidening {
		t.Fatalf("expected widening retype, got %+v", pd.Retyped)
	}
	if len(res.Instances.Objects) != 1 {
		t.Fatalf("expected exactly 1 object diff, got %+v", res.Instances.Objects)
	}
	od := findObjectDiff(res.Instances, objKey("item", IntValue(2)))
	pc := findPropChange(od.Changes, "p-qty")
	if pc == nil || pc.Kind != ChangeValueChanged {
		t.Fatalf("expected value_changed, got %+v", pc)
	}
}

// 场景四：链接因端点删除而失效与链接被显式删除必须可区分，
// 且显式删除不因端点删除的发生而被掩盖。
func TestLinkEndpointDeletedVsExplicit(t *testing.T) {
	b0 := NewBuilder("L", 1)
	b0.AddObjectType("person", "Person", Property{ID: "pk", Key: "id", Type: TypeInt})
	b0.AddObjectType("company", "Company", Property{ID: "pk", Key: "id", Type: TypeInt})
	b0.AddLinkType("works_for", "WorksFor", "person", "company")
	b0.PutObject("person", IntValue(1), nil)
	b0.PutObject("person", IntValue(2), nil)
	b0.PutObject("company", IntValue(9), nil)
	p1 := ObjectRef{TypeID: "person", PK: IntValue(1)}
	p2 := ObjectRef{TypeID: "person", PK: IntValue(2)}
	c9 := ObjectRef{TypeID: "company", PK: IntValue(9)}
	b0.PutLink("works_for", p1, c9)
	b0.PutLink("works_for", p2, c9)
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.DeleteObject("person", IntValue(1)) // 级联使 wf(p1,c9) 失效
	b1.DeleteLink("works_for", p2, c9)     // 显式删除
	s1 := b1.Build()

	res := mustCompare(t, s0, s1)

	if len(res.Instances.Links) != 2 {
		t.Fatalf("expected 2 link diffs, got %+v", res.Instances.Links)
	}
	l1 := findLinkDiff(res.Instances, linkKey("works_for", p1, c9))
	if l1 == nil || l1.Kind != LinkDeleted || l1.Reason != ReasonEndpointDeleted {
		t.Fatalf("expected endpoint_deleted, got %+v", l1)
	}
	if len(l1.MissingEndpoints) != 1 || l1.MissingEndpoints[0] != p1 {
		t.Fatalf("expected missing endpoint p1, got %+v", l1.MissingEndpoints)
	}
	l2 := findLinkDiff(res.Instances, linkKey("works_for", p2, c9))
	if l2 == nil || l2.Kind != LinkDeleted || l2.Reason != ReasonLinkExplicit {
		t.Fatalf("expected explicit deletion not masked, got %+v", l2)
	}
}

// 场景五：对象类型整体废弃时，其全部实例统一归因为类型废弃，
// 与实例本身被显式删除严格区分。
func TestTypeDeprecatedUniformAttribution(t *testing.T) {
	b0 := NewBuilder("L", 1)
	b0.AddObjectType("legacy", "Legacy", Property{ID: "pk", Key: "id", Type: TypeInt})
	b0.AddObjectType("keep", "Keep", Property{ID: "pk", Key: "id", Type: TypeInt})
	for i := int64(1); i <= 3; i++ {
		b0.PutObject("legacy", IntValue(i), nil)
	}
	b0.PutObject("keep", IntValue(9), nil)
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.RemoveObjectType("legacy")
	b1.DeleteObject("keep", IntValue(9))
	s1 := b1.Build()

	res := mustCompare(t, s0, s1)

	if len(res.Instances.Objects) != 4 {
		t.Fatalf("expected 4 object diffs, got %+v", res.Instances.Objects)
	}
	for i := int64(1); i <= 3; i++ {
		od := findObjectDiff(res.Instances, objKey("legacy", IntValue(i)))
		if od == nil || od.Kind != ObjectDeleted || od.Reason != ReasonTypeDeprecated {
			t.Fatalf("legacy obj %d: expected type_deprecated, got %+v", i, od)
		}
	}
	od := findObjectDiff(res.Instances, objKey("keep", IntValue(9)))
	if od == nil || od.Kind != ObjectDeleted || od.Reason != ReasonObjectExplicit {
		t.Fatalf("keep obj: expected explicit, got %+v", od)
	}
}

// 场景六：属性先重命名、后在更晚快照中调整取值类型。
// 直接比对首尾快照时，两个维度必须分别给出。
func TestRenameThenRetypeAcrossSnapshots(t *testing.T) {
	b0 := NewBuilder("L", 1)
	b0.AddObjectType("doc", "Doc",
		Property{ID: "pk", Key: "id", Type: TypeInt},
		Property{ID: "p-label", Key: "label", Type: TypeString},
	)
	b0.PutObject("doc", IntValue(1), map[string]Value{"p-label": StringValue("hello")})
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.RenameProperty("doc", "p-label", "title")
	s1 := b1.Build()

	b2 := FromSnapshot(s1)
	b2.Advance(3)
	b2.RetypeProperty("doc", "p-label", TypeInt)
	s2 := b2.Build()

	// 首尾直接比对：重命名与改型作为两个独立维度出现。
	res := mustCompare(t, s0, s2)
	pd := findPropDiff(res.Schema.ObjectTypeDiff("doc").Props, "p-label")
	if pd.Renamed == nil || pd.Renamed.OldKey != "label" || pd.Renamed.NewKey != "title" {
		t.Fatalf("expected rename dimension, got %+v", pd)
	}
	if pd.Retyped == nil || pd.Retyped.OldType != TypeString || pd.Retyped.NewType != TypeInt {
		t.Fatalf("expected retype dimension, got %+v", pd)
	}
	od := findObjectDiff(res.Instances, objKey("doc", IntValue(1)))
	pc := findPropChange(od.Changes, "p-label")
	if pc == nil || pc.Kind != ChangeTypeIncompatible {
		t.Fatalf("expected type_incompatible, got %+v", pc)
	}

	// 分段比对：第一段只有重命名且无实例差异，第二段只有改型。
	res01 := mustCompare(t, s0, s1)
	pd01 := findPropDiff(res01.Schema.ObjectTypeDiff("doc").Props, "p-label")
	if pd01.Renamed == nil || pd01.Retyped != nil {
		t.Fatalf("first hop should be rename only, got %+v", pd01)
	}
	if !res01.Instances.Empty() {
		t.Fatalf("rename alone must not produce instance diffs, got %+v", res01.Instances)
	}
	res12 := mustCompare(t, s1, s2)
	pd12 := findPropDiff(res12.Schema.ObjectTypeDiff("doc").Props, "p-label")
	if pd12.Renamed != nil || pd12.Retyped == nil {
		t.Fatalf("second hop should be retype only, got %+v", pd12)
	}
}

// 场景七：更新类差异精确到具体属性；其他属性未变化不得牵连，
// 完全未变化的对象不得出现。
func TestUpdatePrecision(t *testing.T) {
	b0 := personBuilder()
	b0.PutObject("person", IntValue(1), map[string]Value{
		"p-name": StringValue("alice"), "p-age": IntValue(30),
	})
	b0.PutObject("person", IntValue(2), map[string]Value{
		"p-name": StringValue("bob"), "p-age": IntValue(40),
	})
	s0 := b0.Build()

	b1 := FromSnapshot(s0)
	b1.Advance(2)
	b1.PutObject("person", IntValue(1), map[string]Value{
		"p-name": StringValue("alice"), "p-age": IntValue(31), // 只有 age 变化
	})
	s1 := b1.Build()

	res := mustCompare(t, s0, s1)
	if len(res.Instances.Objects) != 1 {
		t.Fatalf("expected 1 object diff, got %+v", res.Instances.Objects)
	}
	od := res.Instances.Objects[0]
	if len(od.Changes) != 1 || od.Changes[0].PropID != "p-age" {
		t.Fatalf("update must be precise to p-age only, got %+v", od.Changes)
	}
}
