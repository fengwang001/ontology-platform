package adjudicator

import (
	"bytes"
	"encoding/gob"
	"reflect"
	"sync"
	"testing"
)

// standardSnapshot 构造一份覆盖四类备份、含两条依赖链的标准快照：
//
//	T1 <- O1, O2 <- L1 <- A1
//	T2 <- O3 <- L2 <- A2 (A2 同时涉及 O2)
func standardSnapshot() *Snapshot {
	s := &Snapshot{}
	s.Backups[CategoryObjectTypeDef] = Healthy(TypeDef("T1"), TypeDef("T2"))
	s.Backups[CategoryObjectInstance] = Healthy(
		Object("O1", "T1"), Object("O2", "T1"), Object("O3", "T2"))
	s.Backups[CategoryLinkInstance] = Healthy(
		Link("L1", "O1", "O2"), Link("L2", "O2", "O3"))
	s.Backups[CategoryActionRecord] = Healthy(
		Action("A1", ObjectRef("O1"), LinkRef("L1")),
		Action("A2", ObjectRef("O3"), LinkRef("L2"), ObjectRef("O2")))
	return s
}

func ref(c Category, id string) RecordRef {
	return RecordRef{Category: c, ID: id}
}

func verdictOf(t *testing.T, v Verdict, r RecordRef) RecordVerdict {
	t.Helper()
	for _, rv := range v.Verdicts {
		if rv.Ref == r {
			return rv
		}
	}
	t.Fatalf("记录 %v 缺少判定", r)
	return RecordVerdict{}
}

func assertRebuildable(t *testing.T, v Verdict, r RecordRef) {
	t.Helper()
	if rv := verdictOf(t, v, r); !rv.Rebuildable {
		t.Fatalf("记录 %v 应可重建, 实际不可重建 (%v, blockedBy=%v)", r, rv.Reason, rv.BlockedBy)
	}
}

func assertNotRebuildable(t *testing.T, v Verdict, r RecordRef, reason ReasonCode) {
	t.Helper()
	rv := verdictOf(t, v, r)
	if rv.Rebuildable {
		t.Fatalf("记录 %v 应不可重建, 实际可重建", r)
	}
	if rv.Reason != reason {
		t.Fatalf("记录 %v 原因码应为 %v, 实际为 %v", r, reason, rv.Reason)
	}
}

// assertOrderValid 校验重建顺序：每条记录的依赖都排在它之前。
func assertOrderValid(t *testing.T, s *Snapshot, v Verdict) {
	t.Helper()
	position := make(map[RecordRef]int, len(v.Order))
	for i, r := range v.Order {
		position[r] = i
	}
	idx := BuildIndex(s)
	for i, r := range v.Order {
		for _, dep := range idx.records[r].DependsOn {
			j, ok := position[dep]
			if !ok {
				t.Fatalf("记录 %v 的依赖 %v 不在重建顺序中", r, dep)
			}
			if j >= i {
				t.Fatalf("记录 %v 排在其依赖 %v 之前", r, dep)
			}
		}
	}
}

// 每一类备份单独整体缺失时，依赖该类别记录的数据全部级联不可重建，
// 其余类别不受影响；整体缺失是最高优先级错误。
func TestCategoryWhollyMissing(t *testing.T) {
	types := []RecordRef{ref(CategoryObjectTypeDef, "T1"), ref(CategoryObjectTypeDef, "T2")}
	objects := []RecordRef{ref(CategoryObjectInstance, "O1"), ref(CategoryObjectInstance, "O2"), ref(CategoryObjectInstance, "O3")}
	links := []RecordRef{ref(CategoryLinkInstance, "L1"), ref(CategoryLinkInstance, "L2")}
	actions := []RecordRef{ref(CategoryActionRecord, "A1"), ref(CategoryActionRecord, "A2")}

	type testCase struct {
		name    string
		missing Category
		wantBad []RecordRef
		wantOK  []RecordRef
	}
	join := func(groups ...[]RecordRef) []RecordRef {
		var out []RecordRef
		for _, g := range groups {
			out = append(out, g...)
		}
		return out
	}
	cases := []testCase{
		{"对象类型定义缺失", CategoryObjectTypeDef, join(objects, links, actions), nil},
		{"对象实例缺失", CategoryObjectInstance, join(links, actions), types},
		{"链接实例缺失", CategoryLinkInstance, actions, join(types, objects)},
		{"动作记录缺失", CategoryActionRecord, nil, join(types, objects, links)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := standardSnapshot()
			s.Backups[tc.missing] = Missing()
			v := New().Adjudicate(s)
			if v.Err == nil || v.Err.Kind != ErrKindCategoryUnavailable {
				t.Fatalf("错误类型应为 CategoryUnavailable, 实际为 %v", v.Err)
			}
			for _, r := range tc.wantBad {
				assertNotRebuildable(t, v, r, ReasonDependencyUnavailable)
			}
			for _, r := range tc.wantOK {
				assertRebuildable(t, v, r)
			}
			assertOrderValid(t, s, v)
		})
	}
}

// 类型定义整体缺失时，即使对象实例、链接备份完好，也不允许凭这些
// 残留片段单方面猜测出类型定义：对象/链接/动作全部不可重建。
func TestNoGuessingTypeDefFromResidue(t *testing.T) {
	s := standardSnapshot()
	s.Backups[CategoryObjectTypeDef] = Missing()
	v := New().Adjudicate(s)
	for _, r := range []RecordRef{
		ref(CategoryObjectInstance, "O1"),
		ref(CategoryLinkInstance, "L1"),
		ref(CategoryActionRecord, "A1"),
	} {
		if v.Rebuildable(r) {
			t.Fatalf("类型定义缺失时 %v 不得凭残留数据重建", r)
		}
	}
}

// 每一类备份单独整体损坏（存在但无法解析）时，该类别全部记录
// 判定为类别不可用，下游级联不可重建。
func TestCategoryWhollyCorrupt(t *testing.T) {
	for _, c := range []Category{CategoryObjectTypeDef, CategoryObjectInstance, CategoryLinkInstance, CategoryActionRecord} {
		t.Run(c.String(), func(t *testing.T) {
			s := standardSnapshot()
			records := s.Backups[c].Records
			s.Backups[c] = WhollyCorrupt(records...)
			v := New().Adjudicate(s)
			if v.Err == nil || v.Err.Kind != ErrKindCategoryUnavailable {
				t.Fatalf("错误类型应为 CategoryUnavailable, 实际为 %v", v.Err)
			}
			for _, r := range records {
				assertNotRebuildable(t, v, r.Ref, ReasonCategoryUnavailable)
			}
			assertOrderValid(t, s, v)
		})
	}
}

// 对象实例部分损坏：依赖损坏实例的链接与动作不可重建，
// 依赖完好实例的部分正常推进。
func TestPartialDamageObjectInstance(t *testing.T) {
	s := standardSnapshot()
	s.Backups[CategoryObjectInstance] = PartiallyDamaged([]string{"O2"},
		Object("O1", "T1"), Object("O2", "T1"), Object("O3", "T2"))
	v := New().Adjudicate(s)

	if v.Err == nil || v.Err.Kind != ErrKindCascade {
		t.Fatalf("错误类型应为 Cascade, 实际为 %v", v.Err)
	}
	assertNotRebuildable(t, v, ref(CategoryObjectInstance, "O2"), ReasonSelfDamaged)
	// L1 依赖 O1 与损坏的 O2 => 不可重建; A1 随之级联。
	assertNotRebuildable(t, v, ref(CategoryLinkInstance, "L1"), ReasonDependencyUnavailable)
	assertNotRebuildable(t, v, ref(CategoryActionRecord, "A1"), ReasonDependencyUnavailable)
	// L2 依赖 O2 与 O3 => 不可重建; A2 级联。
	assertNotRebuildable(t, v, ref(CategoryLinkInstance, "L2"), ReasonDependencyUnavailable)
	assertNotRebuildable(t, v, ref(CategoryActionRecord, "A2"), ReasonDependencyUnavailable)
	// 完好部分不受影响。
	assertRebuildable(t, v, ref(CategoryObjectTypeDef, "T1"))
	assertRebuildable(t, v, ref(CategoryObjectInstance, "O1"))
	assertRebuildable(t, v, ref(CategoryObjectInstance, "O3"))
	assertOrderValid(t, s, v)
}

// 链接实例部分损坏：只波及依赖损坏链接的动作。
func TestPartialDamageLinkInstance(t *testing.T) {
	s := standardSnapshot()
	s.Backups[CategoryLinkInstance] = PartiallyDamaged([]string{"L1"},
		Link("L1", "O1", "O2"), Link("L2", "O2", "O3"))
	v := New().Adjudicate(s)

	assertNotRebuildable(t, v, ref(CategoryLinkInstance, "L1"), ReasonSelfDamaged)
	assertNotRebuildable(t, v, ref(CategoryActionRecord, "A1"), ReasonDependencyUnavailable)
	assertRebuildable(t, v, ref(CategoryLinkInstance, "L2"))
	assertRebuildable(t, v, ref(CategoryActionRecord, "A2"))
}

// 类型定义部分损坏沿 类型->对象->链接->动作 四级依赖链级联。
func TestCascadeAcrossCategories(t *testing.T) {
	s := standardSnapshot()
	s.Backups[CategoryObjectTypeDef] = PartiallyDamaged([]string{"T1"}, TypeDef("T1"), TypeDef("T2"))
	v := New().Adjudicate(s)

	assertNotRebuildable(t, v, ref(CategoryObjectTypeDef, "T1"), ReasonSelfDamaged)
	assertNotRebuildable(t, v, ref(CategoryObjectInstance, "O1"), ReasonDependencyUnavailable)
	assertNotRebuildable(t, v, ref(CategoryObjectInstance, "O2"), ReasonDependencyUnavailable)
	assertNotRebuildable(t, v, ref(CategoryLinkInstance, "L1"), ReasonDependencyUnavailable)
	assertNotRebuildable(t, v, ref(CategoryActionRecord, "A1"), ReasonDependencyUnavailable)
	// T2 链路完好。
	assertRebuildable(t, v, ref(CategoryObjectTypeDef, "T2"))
	assertRebuildable(t, v, ref(CategoryObjectInstance, "O3"))
	// L2 依赖 O2(不可重建) 与 O3(可重建) => 不可重建。
	assertNotRebuildable(t, v, ref(CategoryLinkInstance, "L2"), ReasonDependencyUnavailable)
	assertOrderValid(t, s, v)
}

// 动作记录的依赖恰好落在可恢复与不可恢复的分界上：
// 必须逐一核对每个依赖项，严格按依赖是否落在可恢复范围内下结论，
// 不允许出现不确定的中间判定。
func TestBoundaryDependency(t *testing.T) {
	s := standardSnapshot()
	// O2 损坏是分界：A2 依赖 O3(可恢复) 与 O2(不可恢复)。
	s.Backups[CategoryObjectInstance] = PartiallyDamaged([]string{"O2"},
		Object("O1", "T1"), Object("O2", "T1"), Object("O3", "T2"))
	// A3 只依赖分界另一侧的完好记录 O1、O3。
	s.Backups[CategoryActionRecord] = Healthy(
		Action("A2", ObjectRef("O3"), ObjectRef("O2")),
		Action("A3", ObjectRef("O1"), ObjectRef("O3")))
	v := New().Adjudicate(s)

	rv := verdictOf(t, v, ref(CategoryActionRecord, "A2"))
	if rv.Rebuildable {
		t.Fatal("A2 依赖不可恢复的 O2, 必须判定为不可重建")
	}
	if rv.Reason != ReasonDependencyUnavailable {
		t.Fatalf("A2 原因码应为 DependencyUnavailable, 实际为 %v", rv.Reason)
	}
	// 阻断原因必须精确指向落在不可恢复侧的 O2。
	if !reflect.DeepEqual(rv.BlockedBy, []RecordRef{ref(CategoryObjectInstance, "O2")}) {
		t.Fatalf("A2 的阻断原因应为 [O2], 实际为 %v", rv.BlockedBy)
	}
	assertRebuildable(t, v, ref(CategoryActionRecord, "A3"))
}

// 可重建子图存在循环依赖时，顺序无法确定，环上记录不可重建。
func TestCycleDetected(t *testing.T) {
	s := &Snapshot{}
	for c := 0; c < categoryCount; c++ {
		s.Backups[c] = Healthy()
	}
	s.Backups[CategoryObjectTypeDef] = Healthy(
		Record{Ref: ref(CategoryObjectTypeDef, "T1"), DependsOn: []RecordRef{ref(CategoryObjectTypeDef, "T2")}},
		Record{Ref: ref(CategoryObjectTypeDef, "T2"), DependsOn: []RecordRef{ref(CategoryObjectTypeDef, "T1")}},
		TypeDef("T3"),
	)
	v := New().Adjudicate(s)
	if v.Err == nil || v.Err.Kind != ErrKindCycle {
		t.Fatalf("错误类型应为 Cycle, 实际为 %v", v.Err)
	}
	assertNotRebuildable(t, v, ref(CategoryObjectTypeDef, "T1"), ReasonCycle)
	assertNotRebuildable(t, v, ref(CategoryObjectTypeDef, "T2"), ReasonCycle)
	assertRebuildable(t, v, ref(CategoryObjectTypeDef, "T3"))
	if len(v.Order) != 1 || v.Order[0] != ref(CategoryObjectTypeDef, "T3") {
		t.Fatalf("重建顺序应只含 T3, 实际为 %v", v.Order)
	}
}

// 错误优先级固定：整体不可用 > 级联 > 循环。
func TestErrorPriority(t *testing.T) {
	// 同时存在整体缺失与循环：报整体缺失。
	s := standardSnapshot()
	s.Backups[CategoryActionRecord] = Missing()
	s.Backups[CategoryObjectTypeDef] = Healthy(
		Record{Ref: ref(CategoryObjectTypeDef, "T1"), DependsOn: []RecordRef{ref(CategoryObjectTypeDef, "T1")}},
	)
	v := New().Adjudicate(s)
	if v.Err.Kind != ErrKindCategoryUnavailable {
		t.Fatalf("整体缺失应优先于循环, 实际为 %v", v.Err.Kind)
	}

	// 同时存在级联与循环：报级联。
	s2 := standardSnapshot()
	s2.Backups[CategoryObjectInstance] = PartiallyDamaged([]string{"O1"},
		Object("O1", "T1"), Object("O2", "T1"), Object("O3", "T2"))
	s2.Backups[CategoryActionRecord] = Healthy(
		Record{Ref: ref(CategoryActionRecord, "A1"), DependsOn: []RecordRef{ref(CategoryActionRecord, "A1")}},
	)
	v2 := New().Adjudicate(s2)
	if v2.Err.Kind != ErrKindCascade {
		t.Fatalf("级联应优先于循环, 实际为 %v", v2.Err.Kind)
	}
}

// 同一批备份反复裁决，重建顺序与可重建范围必须完全一致。
func TestDeterministicRepeatedAdjudication(t *testing.T) {
	s := standardSnapshot()
	s.Backups[CategoryObjectInstance] = PartiallyDamaged([]string{"O2"},
		Object("O1", "T1"), Object("O2", "T1"), Object("O3", "T2"))
	first := New().Adjudicate(s)
	for i := 0; i < 50; i++ {
		got := New().Adjudicate(s)
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("第 %d 次裁决结果与首次不一致", i)
		}
	}
}

// 并发裁决同一批备份：结果一致且快照不被修改。
func TestConcurrentAdjudication(t *testing.T) {
	s := standardSnapshot()
	before := snapshotDigest(t, s)
	want := New().Adjudicate(s)

	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				got := New().Adjudicate(s)
				if !reflect.DeepEqual(want, got) {
					errs <- "并发裁决结果不一致"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if after := snapshotDigest(t, s); !bytes.Equal(before, after) {
		t.Fatal("裁决过程修改了备份快照")
	}
}

// snapshotDigest 为快照内容计算确定性摘要，用于检测裁决是否修改了输入。
func snapshotDigest(t *testing.T, s *Snapshot) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		t.Fatalf("快照序列化失败: %v", err)
	}
	return buf.Bytes()
}
