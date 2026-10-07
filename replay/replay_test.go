package replay

import (
	"reflect"
	"strconv"
	"sync"
	"testing"
)

// --- 测试辅助 ---

// baseSnapshot 构造一份基础良好快照：
// 类型 Person(name, age) / Company(title)，链接类型 worksAt(Person->Company)，
// 对象 p1, p2, c1，链接 worksAt(p1->c1)。
func baseSnapshot() *Snapshot {
	schema := Schema{
		ObjectTypes: map[string]ObjectTypeDef{
			"Person":  {TypeID: "Person", Properties: map[string]struct{}{"name": {}, "age": {}}},
			"Company": {TypeID: "Company", Properties: map[string]struct{}{"title": {}}},
		},
		LinkTypes: map[string]LinkTypeDef{
			"worksAt": {TypeID: "worksAt", SourceType: "Person", TargetType: "Company",
				Constraints: map[string]PropertyValue{}},
		},
	}
	objects := map[string]ObjectInstance{
		"p1": {ObjectID: "p1", ObjectType: "Person",
			Properties: map[string]PropertyValue{"name": "alice", "age": 30}},
		"p2": {ObjectID: "p2", ObjectType: "Person",
			Properties: map[string]PropertyValue{"name": "bob", "age": 40}},
		"c1": {ObjectID: "c1", ObjectType: "Company",
			Properties: map[string]PropertyValue{"title": "acme"}},
	}
	links := map[LinkKey]LinkInstance{
		{LinkTypeID: "worksAt", SourceID: "p1", TargetID: "c1"}: {LinkTypeID: "worksAt", SourceID: "p1", TargetID: "c1"},
	}
	return NewSnapshot(schema, objects, links)
}

// targetOf 用参照模型的物化重放计算一组变化应用后的目标状态。
func targetOf(t *testing.T, snap *Snapshot, changes []Change) (Schema, map[string]ObjectInstance, map[LinkKey]LinkInstance) {
	t.Helper()
	st := stateFromSnapshot(snap)
	for i := range changes {
		if reason := refApply(st, &changes[i]); reason != "" {
			t.Fatalf("targetOf: 第 %d 条变化应用失败: %s", i, reason)
		}
	}
	return st.Schema, st.Objects, st.Links
}

// makeDelta 构造一份差异记录，目标状态由变化序列在快照上重放得到。
func makeDelta(t *testing.T, snap *Snapshot, changes []Change) *DeltaLog {
	t.Helper()
	schema, objects, links := targetOf(t, snap, changes)
	return &DeltaLog{
		Changes:       changes,
		TargetSchema:  schema,
		TargetObjects: objects,
		TargetLinks:   links,
	}
}

func personBefore(id, name string, age int) ObjectInstance {
	return ObjectInstance{ObjectID: id, ObjectType: "Person",
		Properties: map[string]PropertyValue{"name": name, "age": age}}
}

// --- 等价（ happy path ） ---

func TestVerifyEquivalent(t *testing.T) {
	snap := baseSnapshot()
	changes := []Change{
		{Kind: ChangeUpdateObject,
			ObjectBefore: personBefore("p1", "alice", 30),
			ObjectAfter:  personBefore("p1", "alice", 31)},
		{Kind: ChangeAddObject,
			ObjectAfter: ObjectInstance{ObjectID: "c2", ObjectType: "Company",
				Properties: map[string]PropertyValue{"title": "globex"}}},
		{Kind: ChangeAddLink,
			LinkAfter: LinkInstance{LinkTypeID: "worksAt", SourceID: "p2", TargetID: "c2"}},
	}
	log := makeDelta(t, snap, changes)
	got := NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictEquivalent {
		t.Fatalf("期望 Equivalent，得到 %v: %s", got.Verdict, got.Reason)
	}
	if ref := ReferenceVerify(snap, log); ref.Verdict != VerdictEquivalent {
		t.Fatalf("参照模型期望 Equivalent，得到 %v", ref.Verdict)
	}
}

// --- 结构层/实例层顺序错位 ---

func TestInstanceChangeBeforeSchemaChange(t *testing.T) {
	snap := baseSnapshot()

	// 对象实例变化出现在其类型声明之前。
	log := &DeltaLog{Changes: []Change{
		{Kind: ChangeAddObject, ObjectAfter: ObjectInstance{ObjectID: "r1", ObjectType: "Robot",
			Properties: map[string]PropertyValue{}}},
		{Kind: ChangeAddObjectType, TypeID: "Robot",
			ObjectTypeAfter: ObjectTypeDef{TypeID: "Robot", Properties: map[string]struct{}{}}},
	}}
	got := NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictInconsistentDelta || got.ChangeIndex != 0 {
		t.Fatalf("期望 InconsistentDelta@0，得到 %v@%d", got.Verdict, got.ChangeIndex)
	}

	// 链接实例变化出现在其链接类型声明之前。
	log = &DeltaLog{Changes: []Change{
		{Kind: ChangeAddLink, LinkAfter: LinkInstance{LinkTypeID: "knows", SourceID: "p1", TargetID: "p2"}},
		{Kind: ChangeAddLinkType, LinkTypeID: "knows",
			LinkTypeAfter: LinkTypeDef{TypeID: "knows", SourceType: "Person", TargetType: "Person",
				Constraints: map[string]PropertyValue{}}},
	}}
	got = NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictInconsistentDelta || got.ChangeIndex != 0 {
		t.Fatalf("期望 InconsistentDelta@0，得到 %v@%d", got.Verdict, got.ChangeIndex)
	}
}

// 差异记录不自洽时必须整体拒绝：即使前面的变化本身有效，
// 也不允许先重放一部分。
func TestInconsistentDeltaRejectedAsWhole(t *testing.T) {
	snap := baseSnapshot()
	before := cloneSnapshot(snap)
	log := &DeltaLog{Changes: []Change{
		{Kind: ChangeUpdateObject,
			ObjectBefore: personBefore("p1", "alice", 30),
			ObjectAfter:  personBefore("p1", "alice", 31)},
		{Kind: ChangeUpdateObject, // 与上一条产生的状态矛盾：不自洽
			ObjectBefore: personBefore("p1", "alice", 30),
			ObjectAfter:  personBefore("p1", "alice", 99)},
	}}
	got := NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictInconsistentDelta || got.ChangeIndex != 1 {
		t.Fatalf("期望 InconsistentDelta@1，得到 %v@%d", got.Verdict, got.ChangeIndex)
	}
	if !reflect.DeepEqual(before, snap) {
		t.Fatal("校验修改了原始快照")
	}
}

// --- 前提环境不满足 ---

// 差异记录声明更新的对象在快照中根本不存在。
func TestUpdateObjectMissingInSnapshot(t *testing.T) {
	snap := baseSnapshot()
	log := &DeltaLog{Changes: []Change{
		{Kind: ChangeUpdateObject,
			ObjectBefore: personBefore("p999", "ghost", 20),
			ObjectAfter:  personBefore("p999", "ghost", 21)},
	}}
	got := NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictPreconditionFailed || got.ChangeIndex != 0 {
		t.Fatalf("期望 PreconditionFailed@0，得到 %v@%d", got.Verdict, got.ChangeIndex)
	}
}

// --- 并发确定性与快照不可变 ---

// 对同一份快照与同一份差异记录并发校验，所有结果必须完全一致，
// 且原始快照不得被修改。
func TestConcurrentVerifyDeterministic(t *testing.T) {
	snap := baseSnapshot()
	log := validDelta(t, snap)
	snapshotBefore := cloneSnapshot(snap)

	v := NewVerifier()
	want := v.Verify(snap, log)
	if want.Verdict != VerdictEquivalent {
		t.Fatalf("前置校验期望 Equivalent，得到 %v", want.Verdict)
	}

	const workers = 32
	const iterations = 20
	results := make([]Result, workers*iterations)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for it := 0; it < iterations; it++ {
				results[worker*iterations+it] = v.Verify(snap, log)
			}
		}(w)
	}
	wg.Wait()

	for i, got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("第 %d 次并发校验结果不一致: %+v != %+v", i, got, want)
		}
	}
	if !reflect.DeepEqual(snapshotBefore, snap) {
		t.Fatal("并发校验修改了原始快照")
	}
}

// 反复执行校验，结果必须始终一致。
func TestRepeatedVerifyDeterministic(t *testing.T) {
	snap := baseSnapshot()
	logs := []*DeltaLog{
		validDelta(t, snap),
		{Changes: []Change{{Kind: ChangeUpdateObject,
			ObjectBefore: personBefore("p999", "ghost", 20),
			ObjectAfter:  personBefore("p999", "ghost", 21)}}},
		{Changes: []Change{{Kind: ChangeAddObjectType, TypeID: "Person",
			ObjectTypeAfter: ObjectTypeDef{TypeID: "Person", Properties: map[string]struct{}{}}}}},
	}
	v := NewVerifier()
	for i, log := range logs {
		want := v.Verify(snap, log)
		for it := 0; it < 100; it++ {
			if got := v.Verify(snap, log); !reflect.DeepEqual(got, want) {
				t.Fatalf("差异记录 %d 第 %d 次校验结果不一致: %+v != %+v", i, it, got, want)
			}
		}
	}
}

// 校验（包括用于核对的重放）不得修改原始快照。
func TestVerifyDoesNotModifySnapshot(t *testing.T) {
	snap := baseSnapshot()
	log := validDelta(t, snap)
	before := cloneSnapshot(snap)
	NewVerifier().Verify(snap, log)
	if !reflect.DeepEqual(before, snap) {
		t.Fatal("校验修改了原始快照")
	}
}

// --- 复杂度与快照规模无关 ---

// 固定同一份差异记录，快照中未被触及的对象与链接数量增长两个
// 数量级后，对原始快照的读取统计必须完全一致。
func TestCostIndependentOfUntouchedSnapshotSize(t *testing.T) {
	small := baseSnapshot()
	log := validDelta(t, small)

	// 大同照：额外加入 5000 个未被差异记录触及的对象与链接。
	big := cloneSnapshot(small)
	for i := 0; i < 5000; i++ {
		id := "extra-p" + strconv.Itoa(i)
		big.Objects[id] = ObjectInstance{ObjectID: id, ObjectType: "Person",
			Properties: map[string]PropertyValue{"name": "x", "age": i}}
		big.Links[LinkKey{LinkTypeID: "worksAt", SourceID: id, TargetID: "c1"}] =
			LinkInstance{LinkTypeID: "worksAt", SourceID: id, TargetID: "c1"}
	}
	big.Indexes = BuildIndexes(big.Objects, big.Links)

	v := NewVerifier()
	resSmall, statsSmall := v.VerifyWithStats(small, log)
	resBig, statsBig := v.VerifyWithStats(big, log)

	if resSmall.Verdict != VerdictEquivalent {
		t.Fatalf("小快照期望 Equivalent，得到 %v", resSmall.Verdict)
	}
	// 大同照多出差异记录未声明的对象/链接，重放结果与声明目标不等价。
	if resBig.Verdict != VerdictNotEquivalent {
		t.Fatalf("大同照期望 NotEquivalent，得到 %v", resBig.Verdict)
	}
	if statsSmall != statsBig {
		t.Fatalf("快照未触及部分增长后读取统计发生变化: %+v != %+v", statsSmall, statsBig)
	}
	t.Logf("小快照统计: %+v", statsSmall)
	t.Logf("大同照统计: %+v", statsBig)
}

// 变化前状态与快照实际取值不一致：环境与其产生环境不一致。
func TestBeforeStateMismatchWithSnapshot(t *testing.T) {
	snap := baseSnapshot()
	log := &DeltaLog{Changes: []Change{
		{Kind: ChangeUpdateObject,
			ObjectBefore: personBefore("p1", "alice", 999),
			ObjectAfter:  personBefore("p1", "alice", 31)},
	}}
	got := NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictPreconditionFailed || got.ChangeIndex != 0 {
		t.Fatalf("期望 PreconditionFailed@0，得到 %v@%d", got.Verdict, got.ChangeIndex)
	}
}

// 删除一个仍被快照中链接引用的对象。
func TestRemoveObjectWithDanglingSnapshotLink(t *testing.T) {
	snap := baseSnapshot()
	log := &DeltaLog{Changes: []Change{
		{Kind: ChangeRemoveObject,
			ObjectBefore: ObjectInstance{ObjectID: "c1", ObjectType: "Company",
				Properties: map[string]PropertyValue{"title": "acme"}}},
	}}
	got := NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictPreconditionFailed || got.ChangeIndex != 0 {
		t.Fatalf("期望 PreconditionFailed@0，得到 %v@%d", got.Verdict, got.ChangeIndex)
	}
}

// --- 重放结果与声明目标不等价：结构/对象/链接三类 ---

// validDelta 构造一份完全有效的差异记录（供破坏目标状态使用）。
func validDelta(t *testing.T, snap *Snapshot) *DeltaLog {
	t.Helper()
	changes := []Change{
		{Kind: ChangeAddProperty, TypeID: "Person", Property: "email"},
		{Kind: ChangeUpdateObject,
			ObjectBefore: personBefore("p1", "alice", 30),
			ObjectAfter: ObjectInstance{ObjectID: "p1", ObjectType: "Person",
				Properties: map[string]PropertyValue{"name": "alice", "age": 30, "email": "a@x"}}},
		{Kind: ChangeAddObject,
			ObjectAfter: ObjectInstance{ObjectID: "c2", ObjectType: "Company",
				Properties: map[string]PropertyValue{"title": "globex"}}},
		{Kind: ChangeAddLink,
			LinkAfter: LinkInstance{LinkTypeID: "worksAt", SourceID: "p2", TargetID: "c2"}},
	}
	return makeDelta(t, snap, changes)
}

func assertNotEquivalent(t *testing.T, got Result, wantCategory MismatchCategory) {
	t.Helper()
	if got.Verdict != VerdictNotEquivalent {
		t.Fatalf("期望 NotEquivalent，得到 %v: %s", got.Verdict, got.Reason)
	}
	if len(got.Mismatches) == 0 {
		t.Fatal("NotEquivalent 但未报告不一致位置")
	}
	for _, m := range got.Mismatches {
		if m.Category != wantCategory {
			t.Fatalf("不一致类别期望 %v，得到 %v (%+v)", wantCategory, m.Category, m)
		}
	}
}

func TestNotEquivalentSchema(t *testing.T) {
	snap := baseSnapshot()
	log := validDelta(t, snap)
	// 破坏声明目标的结构层：删掉 Person 上声明的 email 属性。
	delete(log.TargetSchema.ObjectTypes["Person"].Properties, "email")
	assertNotEquivalent(t, NewVerifier().Verify(snap, log), CategorySchema)
	assertNotEquivalent(t, ReferenceVerify(snap, log), CategorySchema)
}

func TestNotEquivalentObject(t *testing.T) {
	snap := baseSnapshot()
	log := validDelta(t, snap)
	// 破坏声明目标中的对象取值。
	obj := log.TargetObjects["p1"]
	obj.Properties["age"] = 777
	log.TargetObjects["p1"] = obj
	assertNotEquivalent(t, NewVerifier().Verify(snap, log), CategoryObject)
	assertNotEquivalent(t, ReferenceVerify(snap, log), CategoryObject)
}

func TestNotEquivalentLink(t *testing.T) {
	snap := baseSnapshot()
	log := validDelta(t, snap)
	// 破坏声明目标中的链接：删掉一条。
	delete(log.TargetLinks, LinkKey{LinkTypeID: "worksAt", SourceID: "p2", TargetID: "c2"})
	assertNotEquivalent(t, NewVerifier().Verify(snap, log), CategoryLink)
	assertNotEquivalent(t, ReferenceVerify(snap, log), CategoryLink)
}

// --- 结构层约束新增与依赖它的实例层变化紧邻出现 ---

// mentors 场景辅助：新增链接类型 mentors(Person->Person)。
func addMentorsType() Change {
	return Change{Kind: ChangeAddLinkType, LinkTypeID: "mentors",
		LinkTypeAfter: LinkTypeDef{TypeID: "mentors", SourceType: "Person", TargetType: "Person",
			Constraints: map[string]PropertyValue{}}}
}

func addUniqueSourceConstraint() Change {
	return Change{Kind: ChangeAddLinkTypeConstraint, LinkTypeID: "mentors",
		Constraint: ConstraintUniqueSource, ConstraintValue: true}
}

func addPersonP3() Change {
	return Change{Kind: ChangeAddObject, ObjectAfter: personBefore("p3", "carol", 25)}
}

// 约束新增后紧随的实例变化违反该约束：差异记录自身矛盾。
func TestConstraintViolatedByFollowingInstanceChange(t *testing.T) {
	snap := baseSnapshot()
	log := &DeltaLog{Changes: []Change{
		addPersonP3(),
		addMentorsType(),
		addUniqueSourceConstraint(),
		{Kind: ChangeAddLink, LinkAfter: LinkInstance{LinkTypeID: "mentors", SourceID: "p1", TargetID: "p2"}},
		{Kind: ChangeAddLink, LinkAfter: LinkInstance{LinkTypeID: "mentors", SourceID: "p1", TargetID: "p3"}},
	}}
	got := NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictInconsistentDelta || got.ChangeIndex != 4 {
		t.Fatalf("期望 InconsistentDelta@4，得到 %v@%d: %s", got.Verdict, got.ChangeIndex, got.Reason)
	}
}

// 约束新增后紧随的实例变化遵守该约束：等价。
func TestConstraintHonoredByFollowingInstanceChange(t *testing.T) {
	snap := baseSnapshot()
	changes := []Change{
		addPersonP3(),
		addMentorsType(),
		addUniqueSourceConstraint(),
		{Kind: ChangeAddLink, LinkAfter: LinkInstance{LinkTypeID: "mentors", SourceID: "p1", TargetID: "p2"}},
		{Kind: ChangeAddLink, LinkAfter: LinkInstance{LinkTypeID: "mentors", SourceID: "p2", TargetID: "p3"}},
	}
	log := makeDelta(t, snap, changes)
	if got := NewVerifier().Verify(snap, log); got.Verdict != VerdictEquivalent {
		t.Fatalf("期望 Equivalent，得到 %v: %s", got.Verdict, got.Reason)
	}
}

// 约束尚未生效之前的实例变化不受其约束：重放组件不得在结构层变化
// 应用之前提前校验相关实例。
func TestNoInstanceCheckBeforeConstraintApplied(t *testing.T) {
	snap := baseSnapshot()
	changes := []Change{
		addPersonP3(),
		addMentorsType(),
		{Kind: ChangeAddLink, LinkAfter: LinkInstance{LinkTypeID: "mentors", SourceID: "p1", TargetID: "p2"}},
		{Kind: ChangeAddLink, LinkAfter: LinkInstance{LinkTypeID: "mentors", SourceID: "p1", TargetID: "p3"}},
		addUniqueSourceConstraint(), // 约束在最后才新增，不追溯既往
	}
	log := makeDelta(t, snap, changes)
	if got := NewVerifier().Verify(snap, log); got.Verdict != VerdictEquivalent {
		t.Fatalf("期望 Equivalent（约束不追溯既往），得到 %v: %s", got.Verdict, got.Reason)
	}
}

// 约束与快照中已有链接冲突：前提环境不满足。
func TestConstraintViolatedBySnapshotLink(t *testing.T) {
	snap := baseSnapshot()
	// 给快照中的 worksAt 加上 uniqueSource 约束（快照自身满足该约束）。
	def := snap.Schema.LinkTypes["worksAt"]
	def.Constraints[ConstraintUniqueSource] = true
	snap.Schema.LinkTypes["worksAt"] = def

	log := &DeltaLog{Changes: []Change{
		{Kind: ChangeAddObject, ObjectAfter: ObjectInstance{ObjectID: "c2", ObjectType: "Company",
			Properties: map[string]PropertyValue{"title": "globex"}}},
		// p1 在快照中已有 worksAt(p1->c1)，再添加 worksAt(p1->c2) 违反约束。
		{Kind: ChangeAddLink, LinkAfter: LinkInstance{LinkTypeID: "worksAt", SourceID: "p1", TargetID: "c2"}},
	}}
	got := NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictPreconditionFailed || got.ChangeIndex != 1 {
		t.Fatalf("期望 PreconditionFailed@1，得到 %v@%d: %s", got.Verdict, got.ChangeIndex, got.Reason)
	}
}

// --- 判定优先级 ---

// 同时存在不自洽与前提问题时，必须报告不自洽。
func TestPriorityInconsistentBeatsPrecondition(t *testing.T) {
	snap := baseSnapshot()
	log := &DeltaLog{Changes: []Change{
		{Kind: ChangeUpdateObject, // 快照中不存在 p999：前提问题
			ObjectBefore: personBefore("p999", "ghost", 20),
			ObjectAfter:  personBefore("p999", "ghost", 21)},
		{Kind: ChangeAddObjectType, // 类型已存在：不自洽
			TypeID:          "Person",
			ObjectTypeAfter: ObjectTypeDef{TypeID: "Person", Properties: map[string]struct{}{}}},
	}}
	got := NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictInconsistentDelta || got.ChangeIndex != 1 {
		t.Fatalf("期望 InconsistentDelta@1，得到 %v@%d", got.Verdict, got.ChangeIndex)
	}
}

// 同时存在前提问题与目标不等价时，必须报告前提问题。
func TestPriorityPreconditionBeatsNotEquivalent(t *testing.T) {
	snap := baseSnapshot()
	log := &DeltaLog{
		Changes: []Change{
			{Kind: ChangeUpdateObject, // 快照中不存在：前提问题
				ObjectBefore: personBefore("p999", "ghost", 20),
				ObjectAfter:  personBefore("p999", "ghost", 21)},
		},
		// 目标状态也与快照不符（若误判到等价性阶段会报 NotEquivalent）。
		TargetSchema:  Schema{ObjectTypes: map[string]ObjectTypeDef{}, LinkTypes: map[string]LinkTypeDef{}},
		TargetObjects: map[string]ObjectInstance{},
		TargetLinks:   map[LinkKey]LinkInstance{},
	}
	got := NewVerifier().Verify(snap, log)
	if got.Verdict != VerdictPreconditionFailed || got.ChangeIndex != 0 {
		t.Fatalf("期望 PreconditionFailed@0，得到 %v@%d", got.Verdict, got.ChangeIndex)
	}
}
