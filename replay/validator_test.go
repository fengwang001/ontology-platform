package replay

import (
	"strings"
	"sync"
	"testing"
)

// --- 测试构造辅助 ---

func objType(name string, props map[string]string) ObjectType {
	return ObjectType{Name: name, Properties: props}
}

func obj(id, typ string, props map[string]any) ObjectInstance {
	return ObjectInstance{ID: id, Type: typ, Properties: props}
}

func lnk(id, typ, src, tgt string) LinkInstance {
	return LinkInstance{ID: id, Type: typ, Source: src, Target: tgt}
}

func ptr[T any](v T) *T { return &v }

// baseSnapshot 返回一份基础快照：Person 类型（仅 name）、两个对象、无链接。
func baseSnapshot() *Snapshot {
	return NewSnapshot(
		Schema{
			ObjectTypes: map[string]ObjectType{
				"Person": objType("Person", map[string]string{"name": PropString}),
			},
			LinkTypes: map[string]LinkType{},
		},
		map[string]ObjectInstance{
			"p1": obj("p1", "Person", map[string]any{"name": "甲"}),
			"p2": obj("p2", "Person", map[string]any{"name": "乙"}),
		},
		map[string]LinkInstance{},
	)
}

// knowsType 为带基数约束的 Knows 链接类型定义。
func knowsType() LinkType {
	return LinkType{
		Name:       "Knows",
		SourceType: "Person",
		TargetType: "Person",
		Constraint: Constraint{MaxOutgoingPerSource: 1},
	}
}

// goodDelta 为一条在基础快照上完全合法的差异记录：
// 先结构层（加属性、加带约束的链接类型），后实例层（更新、创建、建链）。
func goodDelta() *Delta {
	personAfter := objType("Person", map[string]string{"name": PropString, "age": PropInt})
	knows := knowsType()
	p1After := obj("p1", "Person", map[string]any{"name": "甲", "age": 30})
	p3 := obj("p3", "Person", map[string]any{"name": "丙", "age": 20})
	l1 := lnk("l1", "Knows", "p1", "p3")
	return &Delta{
		Changes: []Change{
			{Kind: AddProperty, TypeName: "Person", Property: "age", AfterPropType: PropInt},
			{Kind: AddLinkType, TypeName: "Knows", DeclaredLinkType: &knows},
			{Kind: UpdateObject,
				ObjectBefore: ptr(obj("p1", "Person", map[string]any{"name": "甲"})),
				ObjectAfter:  ptr(p1After)},
			{Kind: CreateObject, ObjectAfter: ptr(p3)},
			{Kind: CreateLink, LinkAfter: ptr(l1)},
		},
		Target: DeclaredTarget{
			ObjectTypes: map[string]*ObjectType{"Person": &personAfter},
			LinkTypes:   map[string]*LinkType{"Knows": &knows},
			Objects: map[string]*ObjectInstance{
				"p1": &p1After,
				"p3": &p3,
			},
			Links: map[string]*LinkInstance{"l1": &l1},
		},
	}
}

func expectVerdict(t *testing.T, v Verdict, cat Category, loc Location) {
	t.Helper()
	if v.Category != cat {
		t.Fatalf("期望类别 %v，实际 %v（依据：%s）", cat, v.Category, v.Reason)
	}
	if cat == NotEquivalent && v.Location != loc {
		t.Fatalf("期望不等价位置 %v，实际 %v（依据：%s）", loc, v.Location, v.Reason)
	}
}

// TestValidReplay 覆盖完全合法差异记录的端到端确证。
func TestValidReplay(t *testing.T) {
	v := NewValidator().Validate(baseSnapshot(), goodDelta())
	expectVerdict(t, v, Valid, NoLocation)
}

// TestSchemaInstanceOrderViolation 覆盖结构层与实例层顺序错位：
// 实例层变化先于其依赖的结构层变化出现，必须判定为差异记录自身不自洽，
// 且不得由校验组件自行调整顺序。
func TestSchemaInstanceOrderViolation(t *testing.T) {
	personAfter := objType("Person", map[string]string{"name": PropString, "age": PropInt})
	p1After := obj("p1", "Person", map[string]any{"name": "甲", "age": 30})
	delta := &Delta{
		Changes: []Change{
			// 实例层变化先于相关的结构层变化（AddProperty Person.age）。
			{Kind: UpdateObject,
				ObjectBefore: ptr(obj("p1", "Person", map[string]any{"name": "甲"})),
				ObjectAfter:  ptr(p1After)},
			{Kind: AddProperty, TypeName: "Person", Property: "age", AfterPropType: PropInt},
		},
		Target: DeclaredTarget{
			ObjectTypes: map[string]*ObjectType{"Person": &personAfter},
			Objects:     map[string]*ObjectInstance{"p1": &p1After},
		},
	}
	v := NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, DeltaInconsistent, NoLocation)
	if !strings.Contains(v.Reason, "顺序") && !strings.Contains(v.Reason, "之后") {
		t.Fatalf("判定依据应指出顺序错位，实际：%s", v.Reason)
	}
}

// TestSnapshotMissingObject 覆盖良好快照缺失差异记录所依赖对象的情形：
// 必须判定为重放前提环境不满足，而非差异记录不自洽。
func TestSnapshotMissingObject(t *testing.T) {
	p9After := obj("p9", "Person", map[string]any{"name": "不存在"})
	delta := &Delta{
		Changes: []Change{
			{Kind: UpdateObject,
				ObjectBefore: ptr(obj("p9", "Person", map[string]any{"name": "旧"})),
				ObjectAfter:  ptr(p9After)},
		},
		Target: DeclaredTarget{
			Objects: map[string]*ObjectInstance{"p9": &p9After},
		},
	}
	v := NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, PreconditionUnmet, NoLocation)
	if !strings.Contains(v.Reason, "p9") {
		t.Fatalf("判定依据应指出缺失对象，实际：%s", v.Reason)
	}
}

// TestSnapshotBeforeStateMismatch 覆盖快照中对象取值与声明的变化前状态不一致：
// 属于重放环境与差异记录产生时的环境不一致，判定为前提环境不满足。
func TestSnapshotBeforeStateMismatch(t *testing.T) {
	p1After := obj("p1", "Person", map[string]any{"name": "改"})
	delta := &Delta{
		Changes: []Change{
			{Kind: UpdateObject,
				ObjectBefore: ptr(obj("p1", "Person", map[string]any{"name": "与快照不符"})),
				ObjectAfter:  ptr(p1After)},
		},
		Target: DeclaredTarget{
			Objects: map[string]*ObjectInstance{"p1": &p1After},
		},
	}
	v := NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, PreconditionUnmet, NoLocation)
}

// TestDeltaInternalBeforeStateMismatch 覆盖差异记录内部投影矛盾：
// 第二次更新声明的变化前状态与差异记录自身推出的状态不一致。
func TestDeltaInternalBeforeStateMismatch(t *testing.T) {
	mid := obj("p1", "Person", map[string]any{"name": "中间"})
	final := obj("p1", "Person", map[string]any{"name": "最终"})
	delta := &Delta{
		Changes: []Change{
			{Kind: UpdateObject,
				ObjectBefore: ptr(obj("p1", "Person", map[string]any{"name": "甲"})),
				ObjectAfter:  ptr(mid)},
			{Kind: UpdateObject,
				ObjectBefore: ptr(obj("p1", "Person", map[string]any{"name": "错误的前态"})),
				ObjectAfter:  ptr(final)},
		},
		Target: DeclaredTarget{
			Objects: map[string]*ObjectInstance{"p1": &final},
		},
	}
	v := NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, DeltaInconsistent, NoLocation)
}

// TestNotEquivalentSchema 覆盖重放结果仅在结构层面与声明目标不一致。
func TestNotEquivalentSchema(t *testing.T) {
	delta := goodDelta()
	// 声明目标中的 Person 结构缺少 age，与重放结果不符。
	wrong := objType("Person", map[string]string{"name": PropString})
	delta.Target.ObjectTypes["Person"] = &wrong
	v := NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, NotEquivalent, SchemaLocation)
}

// TestNotEquivalentObject 覆盖重放结果仅在对象层面与声明目标不一致。
func TestNotEquivalentObject(t *testing.T) {
	delta := goodDelta()
	wrong := obj("p1", "Person", map[string]any{"name": "甲", "age": 99})
	delta.Target.Objects["p1"] = &wrong
	v := NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, NotEquivalent, ObjectLocation)
}

// TestNotEquivalentLink 覆盖重放结果仅在链接层面与声明目标不一致。
func TestNotEquivalentLink(t *testing.T) {
	delta := goodDelta()
	wrong := lnk("l1", "Knows", "p3", "p1")
	delta.Target.Links["l1"] = &wrong
	v := NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, NotEquivalent, LinkLocation)
}

// TestNotEquivalentAbsent 覆盖声明目标要求某实体终态不存在而重放结果存在。
func TestNotEquivalentAbsent(t *testing.T) {
	delta := goodDelta()
	delta.Target.Objects["p3"] = nil
	v := NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, NotEquivalent, ObjectLocation)
}

// TestConstraintThenDependentInstance 覆盖结构层约束新增与依赖该约束的
// 实例层变化紧邻出现的情形：链接类型在快照中原本不存在，
// 校验组件不得提前对实例做先行校验，结构生效后必须重新核对约束。
func TestConstraintThenDependentInstance(t *testing.T) {
	// 合法情形：新增带约束的链接类型后紧邻创建一条满足约束的链接。
	v := NewValidator().Validate(baseSnapshot(), goodDelta())
	expectVerdict(t, v, Valid, NoLocation)

	// 违反约束情形：同一源对象紧建两条出边，超出 MaxOutgoingPerSource=1。
	delta := goodDelta()
	l2 := lnk("l2", "Knows", "p1", "p2")
	delta.Changes = append(delta.Changes, Change{Kind: CreateLink, LinkAfter: ptr(l2)})
	delta.Target.Links["l2"] = &l2
	v = NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, DeltaInconsistent, NoLocation)
	if !strings.Contains(v.Reason, "约束") {
		t.Fatalf("判定依据应指出约束违反，实际：%s", v.Reason)
	}
}

// TestConstraintRecheckAfterSchemaChange 覆盖对快照中已存在的链接类型
// 收紧约束后，后续实例层变化必须按新约束重新核对：
// 快照中 p1 已有一条出边，新增约束上限为 1 后再建一条必须被拒绝。
func TestConstraintRecheckAfterSchemaChange(t *testing.T) {
	snap := NewSnapshot(
		Schema{
			ObjectTypes: map[string]ObjectType{
				"Person": objType("Person", map[string]string{"name": PropString}),
			},
			LinkTypes: map[string]LinkType{
				"Knows": {Name: "Knows", SourceType: "Person", TargetType: "Person"},
			},
		},
		map[string]ObjectInstance{
			"p1": obj("p1", "Person", map[string]any{"name": "甲"}),
			"p2": obj("p2", "Person", map[string]any{"name": "乙"}),
			"p3": obj("p3", "Person", map[string]any{"name": "丙"}),
		},
		map[string]LinkInstance{
			"l0": lnk("l0", "Knows", "p1", "p2"),
		},
	)
	newConstraint := Constraint{MaxOutgoingPerSource: 1}
	l1 := lnk("l1", "Knows", "p1", "p3")
	delta := &Delta{
		Changes: []Change{
			{Kind: SetLinkConstraint, TypeName: "Knows",
				BeforeConstraint: ptr(Constraint{}),
				AfterConstraint:  ptr(newConstraint)},
			{Kind: CreateLink, LinkAfter: ptr(l1)},
		},
		Target: DeclaredTarget{
			LinkTypes: map[string]*LinkType{"Knows": {
				Name: "Knows", SourceType: "Person", TargetType: "Person", Constraint: newConstraint,
			}},
			Links: map[string]*LinkInstance{"l1": &l1},
		},
	}
	v := NewValidator().Validate(snap, delta)
	expectVerdict(t, v, DeltaInconsistent, NoLocation)
	if !strings.Contains(v.Reason, "约束") {
		t.Fatalf("判定依据应指出约束违反，实际：%s", v.Reason)
	}
}

// TestPrioritySelfBeforePrecondition 覆盖固定优先级：
// 差异记录同时存在自身不自洽与前提环境不满足时，必须命中前者。
func TestPrioritySelfBeforePrecondition(t *testing.T) {
	personAfter := objType("Person", map[string]string{"name": PropString, "age": PropInt})
	p9After := obj("p9", "Person", map[string]any{"name": "不存在", "age": 1})
	delta := &Delta{
		Changes: []Change{
			// 顺序错位（实例先于相关结构变化）→ 差异记录自身不自洽。
			{Kind: UpdateObject,
				ObjectBefore: ptr(obj("p9", "Person", map[string]any{"name": "旧"})),
				ObjectAfter:  ptr(p9After)},
			{Kind: AddProperty, TypeName: "Person", Property: "age", AfterPropType: PropInt},
		},
		Target: DeclaredTarget{
			ObjectTypes: map[string]*ObjectType{"Person": &personAfter},
			Objects:     map[string]*ObjectInstance{"p9": &p9After},
		},
	}
	v := NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, DeltaInconsistent, NoLocation)
}

// TestPriorityPreconditionBeforeEquivalence 覆盖固定优先级：
// 前提环境不满足与目标不等价同时存在时，必须命中前者。
func TestPriorityPreconditionBeforeEquivalence(t *testing.T) {
	p9After := obj("p9", "Person", map[string]any{"name": "不存在"})
	wrong := obj("p9", "Person", map[string]any{"name": "与重放不符"})
	delta := &Delta{
		Changes: []Change{
			{Kind: UpdateObject,
				ObjectBefore: ptr(obj("p9", "Person", map[string]any{"name": "旧"})),
				ObjectAfter:  ptr(p9After)},
		},
		Target: DeclaredTarget{
			Objects: map[string]*ObjectInstance{"p9": &wrong},
		},
	}
	v := NewValidator().Validate(baseSnapshot(), delta)
	expectVerdict(t, v, PreconditionUnmet, NoLocation)
}

// TestNoPartialReplay 覆盖“不允许先重放一部分再发现问题”：
// 差异记录前段合法、后段不自洽时整体拒绝，且快照不受任何影响。
func TestNoPartialReplay(t *testing.T) {
	snap := baseSnapshot()
	before := CloneSnapshot(snap)
	p3 := obj("p3", "Person", map[string]any{"name": "丙"})
	p3Final := obj("p3", "Person", map[string]any{"name": "改"})
	delta := &Delta{
		Changes: []Change{
			{Kind: CreateObject, ObjectAfter: ptr(p3)},
			// 第二条声明的变化前状态与差异记录自身投影矛盾。
			{Kind: UpdateObject,
				ObjectBefore: ptr(obj("p3", "Person", map[string]any{"name": "矛盾"})),
				ObjectAfter:  ptr(p3Final)},
		},
		Target: DeclaredTarget{
			Objects: map[string]*ObjectInstance{"p3": &p3Final},
		},
	}
	v := NewValidator().Validate(snap, delta)
	expectVerdict(t, v, DeltaInconsistent, NoLocation)
	assertSnapshotEqual(t, before, snap)
}

// TestRepeatDeterminism 覆盖对同一快照与同一差异记录反复校验结果一致。
func TestRepeatDeterminism(t *testing.T) {
	snap := baseSnapshot()
	delta := goodDelta()
	validator := NewValidator()
	first := validator.Validate(snap, delta)
	for i := 0; i < 100; i++ {
		if v := validator.Validate(snap, delta); v != first {
			t.Fatalf("第 %d 次校验结果 %+v 与首次 %+v 不一致", i, v, first)
		}
	}
}

// TestConcurrentConsistency 覆盖并发多次调用结果完全一致，
// 且校验过程（含重放）不修改原始快照。
func TestConcurrentConsistency(t *testing.T) {
	snap := baseSnapshot()
	before := CloneSnapshot(snap)
	delta := goodDelta()
	validator := NewValidator()
	want := validator.Validate(snap, delta)

	const workers = 16
	const rounds = 50
	var wg sync.WaitGroup
	errs := make(chan Verdict, workers*rounds)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				if v := validator.Validate(snap, delta); v != want {
					errs <- v
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for v := range errs {
		t.Fatalf("并发校验出现不一致结果：%+v（期望 %+v）", v, want)
	}
	assertSnapshotEqual(t, before, snap)
}

func assertSnapshotEqual(t *testing.T, want, got *Snapshot) {
	t.Helper()
	if !snapshotDeepEqual(want, got) {
		t.Fatalf("校验过程修改了原始快照")
	}
}

func snapshotDeepEqual(a, b *Snapshot) bool {
	if len(a.Schema.ObjectTypes) != len(b.Schema.ObjectTypes) ||
		len(a.Schema.LinkTypes) != len(b.Schema.LinkTypes) ||
		len(a.Objects) != len(b.Objects) || len(a.Links) != len(b.Links) {
		return false
	}
	for k, va := range a.Schema.ObjectTypes {
		vb, ok := b.Schema.ObjectTypes[k]
		if !ok || !equalObjectType(&va, &vb) {
			return false
		}
	}
	for k, va := range a.Schema.LinkTypes {
		vb, ok := b.Schema.LinkTypes[k]
		if !ok || !equalLinkType(&va, &vb) {
			return false
		}
	}
	for k, va := range a.Objects {
		vb, ok := b.Objects[k]
		if !ok || !equalObject(&va, &vb) {
			return false
		}
	}
	for k, va := range a.Links {
		vb, ok := b.Links[k]
		if !ok || !equalLink(&va, &vb) {
			return false
		}
	}
	return true
}
