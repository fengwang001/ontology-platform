package ontology

import (
	"errors"
	"fmt"
	"testing"
)

// newTestStore 构造带两个索引结构（精确 + 前缀）的属性 "status" 的存储。
func newTestStore(log Logger) (*Store, *FaultInjector, *Index, *Index) {
	faults := NewFaultInjector()
	exact := NewIndex("status-exact", ExactKey)
	prefix := NewIndex("status-prefix", PrefixKey(1))
	s := NewStore(NewMemoryWAL(), faults, log)
	s.RegisterProperty("status", exact, prefix)
	return s, faults, exact, prefix
}

func mustWrite(t *testing.T, s *Store, inst, prop string, v Value) {
	t.Helper()
	if err := s.Write(inst, prop, v); err != nil {
		t.Fatalf("write %s.%s failed: %v", inst, prop, err)
	}
}

func assertQuery(t *testing.T, s *Store, prop string, v Value, want ...string) {
	t.Helper()
	got, err := s.QueryByValue(prop, v)
	if err != nil {
		t.Fatalf("query %s=%+v failed: %v", prop, v, err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("query %s=%+v got %v want %v", prop, v, got, want)
	}
}

func assertAbsentQuery(t *testing.T, s *Store, prop string, want ...string) {
	t.Helper()
	got, err := s.QueryAbsent(prop)
	if err != nil {
		t.Fatalf("query-absent %s failed: %v", prop, err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("query-absent %s got %v want %v", prop, got, want)
	}
}

func TestSingleWriteIndexConsistency(t *testing.T) {
	log := NewBufferLogger()
	s, _, exact, prefix := newTestStore(log)
	s.AddInstance("A")
	s.AddInstance("B")

	mustWrite(t, s, "A", "status", Of("active"))
	assertQuery(t, s, "status", Of("active"), "A")
	assertAbsentQuery(t, s, "status", "B")

	// 改写后旧键不再命中，新键命中；两个索引结构同步。
	mustWrite(t, s, "A", "status", Of("paused"))
	assertQuery(t, s, "status", Of("active"))
	assertQuery(t, s, "status", Of("paused"), "A")
	got, err := s.QueryByIndex("status", prefix.ID, KeyOf("prefix:p"))
	if err != nil || fmt.Sprint(got) != "[A]" {
		t.Fatalf("prefix query got %v err %v", got, err)
	}
	if exact.Size() != 2 || prefix.Size() != 2 {
		t.Fatalf("index size exact=%d prefix=%d, want 2 each", exact.Size(), prefix.Size())
	}
	t.Logf("log excerpt:\n%s", log.String())
}

func TestBatchPartialFailureRollsBackAll(t *testing.T) {
	log := NewBufferLogger()
	s, faults, exact, _ := newTestStore(log)
	for _, id := range []string{"A", "B", "C"} {
		s.AddInstance(id)
		mustWrite(t, s, id, "status", Of("init"))
	}
	clockBefore := s.Clock()

	// 第三个实例（C）应用时精确索引维护失败 → 整体回滚。
	faults.FailIndexFor("status-exact", "C")
	errs := s.BatchWrite("status", []InstanceWrite{
		{InstanceID: "A", Value: Of("x")},
		{InstanceID: "B", Value: Of("y")},
		{InstanceID: "C", Value: Of("z")},
	})
	if !errors.Is(errs[0], ErrBatchRolledBack) || !errors.Is(errs[1], ErrBatchRolledBack) {
		t.Fatalf("A/B should report ErrBatchRolledBack, got %v / %v", errs[0], errs[1])
	}
	if !errors.Is(errs[2], ErrIndexMaintenance) {
		t.Fatalf("C should report ErrIndexMaintenance, got %v", errs[2])
	}
	// 所有实例的属性值与索引都保持写入前状态，时钟戳不变。
	for _, id := range []string{"A", "B", "C"} {
		v, _ := s.Get(id, "status")
		if v != Of("init") {
			t.Fatalf("%s value=%+v, want init", id, v)
		}
	}
	assertQuery(t, s, "status", Of("init"), "A", "B", "C")
	assertQuery(t, s, "status", Of("x"))
	if s.Clock() != clockBefore {
		t.Fatalf("clock advanced on rejected batch: %d -> %d", clockBefore, s.Clock())
	}
	if exact.Size() != 3 {
		t.Fatalf("exact index size=%d, want 3", exact.Size())
	}
}

func TestAbsentVsExplicitDefault(t *testing.T) {
	s, _, _, _ := newTestStore(NewBufferLogger())
	s.AddInstance("A") // 从未写入 → Absent
	s.AddInstance("B")
	s.AddInstance("C")

	mustWrite(t, s, "B", "status", Of("")) // 显式写成默认值 ""
	mustWrite(t, s, "C", "status", Of("x"))
	mustWrite(t, s, "C", "status", Absent) // 显式写成不存在标记

	// 显式默认值只命中 B；不存在状态命中 A 和 C，二者不得混淆。
	assertQuery(t, s, "status", Of(""), "B")
	assertAbsentQuery(t, s, "status", "A", "C")
	vB, _ := s.Get("B", "status")
	vC, _ := s.Get("C", "status")
	if vB != Of("") || !vB.Present {
		t.Fatalf("B should be explicit default, got %+v", vB)
	}
	if vC != Absent || vC.Present {
		t.Fatalf("C should be Absent, got %+v", vC)
	}
}

func TestDeleteRemovesAllIndexEntries(t *testing.T) {
	s, _, exact, prefix := newTestStore(NewBufferLogger())
	s.AddInstance("A")
	s.AddInstance("B")
	mustWrite(t, s, "A", "status", Of("active"))
	mustWrite(t, s, "B", "status", Of("active"))

	if err := s.DeleteInstance("A"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	assertQuery(t, s, "status", Of("active"), "B")
	assertAbsentQuery(t, s, "status")
	if _, err := s.Get("A", "status"); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("deleted instance should be invisible, got %v", err)
	}
	if exact.Size() != 1 || prefix.Size() != 1 {
		t.Fatalf("residual entries: exact=%d prefix=%d", exact.Size(), prefix.Size())
	}
	if err := s.DeleteInstance("A"); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("double delete should report not found, got %v", err)
	}
}

func TestMultipleIndexesStayInSync(t *testing.T) {
	log := NewBufferLogger()
	faults := NewFaultInjector()
	exact := NewIndex("p-exact", ExactKey)
	prefix := NewIndex("p-prefix", PrefixKey(2))
	length := NewIndex("p-length", LengthKey)
	s := NewStore(NewMemoryWAL(), faults, log)
	s.RegisterProperty("p", exact, prefix, length)
	s.AddInstance("A")

	mustWrite(t, s, "A", "p", Of("hello"))
	for _, tc := range []struct {
		idx *Index
		key Key
	}{
		{exact, KeyOf("hello")},
		{prefix, KeyOf("prefix:he")},
		{length, KeyOf("len:5")},
	} {
		got, err := s.QueryByIndex("p", tc.idx.ID, tc.key)
		if err != nil || fmt.Sprint(got) != "[A]" {
			t.Fatalf("index %s key %v got %v err %v", tc.idx.ID, tc.key, got, err)
		}
	}

	// 任一索引结构维护失败 → 写入整体不生效，其余索引结构回退。
	faults.FailIndex("p-length")
	clockBefore := s.Clock()
	err := s.Write("A", "p", Of("world!"))
	if !errors.Is(err, ErrIndexMaintenance) {
		t.Fatalf("want ErrIndexMaintenance, got %v", err)
	}
	v, _ := s.Get("A", "p")
	if v != Of("hello") {
		t.Fatalf("value changed despite rejection: %+v", v)
	}
	if exact.Size() != 1 || prefix.Size() != 1 || length.Size() != 1 {
		t.Fatalf("index sizes %d/%d/%d, want 1 each", exact.Size(), prefix.Size(), length.Size())
	}
	assertQuery(t, s, "p", Of("hello"), "A")
	assertQuery(t, s, "p", Of("world!"))
	if s.Clock() != clockBefore {
		t.Fatalf("clock advanced on rejected write")
	}
}

func TestErrorPriority(t *testing.T) {
	s, faults, _, _ := newTestStore(NewBufferLogger())
	s.AddInstance("A")
	faults.FailIndex("status-exact") // 若走到应用阶段必然索引维护失败
	clockBefore := s.Clock()

	// 实例不存在 + 属性不支持索引 + 索引维护失败同时成立 → 报实例不存在。
	err := s.Write("ghost", "nope", Of("x"))
	if !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("want ErrInstanceNotFound, got %v", err)
	}
	// 属性不支持索引 + 索引维护失败 → 报属性不支持索引。
	err = s.Write("A", "nope", Of("x"))
	if !errors.Is(err, ErrPropertyNotIndexable) {
		t.Fatalf("want ErrPropertyNotIndexable, got %v", err)
	}
	// 批量中实例不存在优先于其他实例的索引维护失败，且整体不生效。
	errs := s.BatchWrite("status", []InstanceWrite{
		{InstanceID: "A", Value: Of("x")},
		{InstanceID: "ghost", Value: Of("y")},
	})
	if !errors.Is(HighestPriorityError(errs), ErrInstanceNotFound) {
		t.Fatalf("batch should report ErrInstanceNotFound, got %v", errs)
	}
	if !errors.Is(errs[0], ErrBatchRolledBack) {
		t.Fatalf("A should report ErrBatchRolledBack, got %v", errs[0])
	}
	v, _ := s.Get("A", "status")
	if v != Absent {
		t.Fatalf("rejected batch changed value: %+v", v)
	}
	if s.Clock() != clockBefore {
		t.Fatalf("rejected writes must not advance clock: %d -> %d", clockBefore, s.Clock())
	}
}

func TestBatchDuplicateInstanceLastWins(t *testing.T) {
	s, _, exact, _ := newTestStore(NewBufferLogger())
	s.AddInstance("A")
	mustWrite(t, s, "A", "status", Of("init"))
	errs := s.BatchWrite("status", []InstanceWrite{
		{InstanceID: "A", Value: Of("first")},
		{InstanceID: "A", Value: Of("second")},
	})
	for i, e := range errs {
		if e != nil {
			t.Fatalf("errs[%d]=%v", i, e)
		}
	}
	v, _ := s.Get("A", "status")
	if v != Of("second") {
		t.Fatalf("value=%+v, want second", v)
	}
	assertQuery(t, s, "status", Of("second"), "A")
	assertQuery(t, s, "status", Of("first"))
	if exact.Size() != 1 {
		t.Fatalf("exact size=%d, want 1", exact.Size())
	}
}

func TestQueryCostIndependentOfInstanceCount(t *testing.T) {
	s, _, exact, _ := newTestStore(NewBufferLogger())
	const total = 5000
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("inst-%05d", i)
		s.AddInstance(id)
		if i < 3 {
			mustWrite(t, s, id, "status", Of("hot"))
		}
	}
	before := exact.Visits()
	got, err := s.QueryByValue("status", Of("hot"))
	if err != nil || len(got) != 3 {
		t.Fatalf("got %d hits err %v", len(got), err)
	}
	visits := exact.Visits() - before
	if visits != 3 {
		t.Fatalf("query visited %d entries for 3 hits among %d instances", visits, total)
	}
	// 未命中查询不访问任何条目。
	before = exact.Visits()
	if got, _ = s.QueryByValue("status", Of("no-such-value")); len(got) != 0 {
		t.Fatalf("unexpected hits %v", got)
	}
	if exact.Visits()-before != 0 {
		t.Fatalf("miss query visited entries")
	}
}

func TestRecoveryCostIndependentOfIndexSize(t *testing.T) {
	s, faults, _, _ := newTestStore(NewBufferLogger())
	for i := 0; i < 2000; i++ {
		id := fmt.Sprintf("inst-%05d", i)
		s.AddInstance(id)
		mustWrite(t, s, id, "status", Of("v"))
	}
	// 制造一个未完成事务后崩溃。
	faults.CrashWhen(func(p CutPoint, _ uint64, _ string) bool { return p == CutAfterValue })
	func() {
		defer func() { recover() }()
		_ = s.Write("inst-00000", "status", Of("crashed"))
	}()
	decisions := s.Recover()
	if len(decisions) != 1 {
		t.Fatalf("recover scanned %d txns, want exactly 1 pending", len(decisions))
	}
	if decisions[0].Decision != "rolled-back" {
		t.Fatalf("decision=%s, want rolled-back", decisions[0].Decision)
	}
}
