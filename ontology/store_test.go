package ontology

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustCreate(t *testing.T, s *Store, ids ...InstanceID) {
	t.Helper()
	for _, id := range ids {
		if !s.Create(id) {
			t.Fatalf("创建实例 %q 失败", id)
		}
	}
}

// 多个批次对同一实例声明同一前置版本并发争夺：恰好一个整体生效，
// 其余全部因版本不满足被整体拒绝。
func TestConcurrentContentionSameInstanceSameVersion(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "a", "b", "c")

	const n = 32
	results := make([]Decision, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b := Batch{
				ID: fmt.Sprintf("batch-%02d", i),
				Items: []Item{
					{Instance: "a", Expect: 1, SetAttrs: map[string]string{"winner": fmt.Sprint(i)}},
					{Instance: "b", Expect: 1},
					{Instance: "c", Expect: 1},
				},
			}
			results[i] = s.ApplyBatch(b)
		}(i)
	}
	wg.Wait()

	committed, conflicts := 0, 0
	for _, d := range results {
		switch d.Outcome {
		case OutcomeCommitted:
			committed++
		case OutcomeVersionConflict:
			conflicts++
		default:
			t.Fatalf("批次 %s 出现意外结果 %v", d.BatchID, d.Outcome)
		}
	}
	if committed != 1 || conflicts != n-1 {
		t.Fatalf("期望恰好 1 个成功、%d 个版本冲突，实际成功 %d、冲突 %d", n-1, committed, conflicts)
	}

	// 胜出的批次整体生效：三个实例版本各自推进到 2。
	for _, id := range []InstanceID{"a", "b", "c"} {
		snap, ok := s.SnapshotOf(id)
		if !ok || snap.Version != 2 {
			t.Fatalf("实例 %q 版本应为 2，实际 %+v", id, snap)
		}
	}
}

// 批次内对同一实例的重复前置声明必须被单独识别，
// 且判定先于版本前置检查（即使版本本就不匹配，也报重复声明）。
func TestDuplicatePrecondition(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "a")

	d := s.ApplyBatch(Batch{
		ID: "dup",
		Items: []Item{
			{Instance: "a", Expect: 999},
			{Instance: "a", Expect: 998},
		},
	})
	if d.Outcome != OutcomeDuplicatePrecondition {
		t.Fatalf("期望 duplicate-precondition，实际 %v", d.Outcome)
	}
	if d.Reads != 0 {
		t.Fatalf("重复声明判定不应读取任何实例版本，实际读取 %d 次", d.Reads)
	}
	snap, _ := s.SnapshotOf("a")
	if snap.Version != 1 {
		t.Fatalf("被拒绝批次不得改变版本，实际 %d", snap.Version)
	}
}

// 前置版本不满足时整批拒绝，且同批其他满足条件的实例也不得被修改。
func TestVersionConflictRejectsWholeBatch(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "a", "b")

	d := s.ApplyBatch(Batch{
		ID: "conflict",
		Items: []Item{
			{Instance: "a", Expect: 1, SetAttrs: map[string]string{"x": "1"}},
			{Instance: "b", Expect: 7},
		},
	})
	if d.Outcome != OutcomeVersionConflict {
		t.Fatalf("期望 version-conflict，实际 %v", d.Outcome)
	}
	if d.Observed["a"] != 1 || d.Observed["b"] != 1 {
		t.Fatalf("判定依据应记录读到的版本，实际 %+v", d.Observed)
	}
	snapA, _ := s.SnapshotOf("a")
	if snapA.Version != 1 || len(snapA.Attrs) != 0 {
		t.Fatalf("版本冲突的批次不得留下任何痕迹，实际 %+v", snapA)
	}
}

// 基数约束边界：恰好达到上限允许提交，超出一条即整批拒绝，
// 且拒绝后版本、属性、关联均不变。
func TestCardinalityBoundary(t *testing.T) {
	s := NewStore()
	s.SetLinkLimit("member", 2)
	mustCreate(t, s, "g", "u1", "u2", "u3")

	d1 := s.ApplyBatch(Batch{
		ID: "fill",
		Items: []Item{{
			Instance: "g", Expect: 1,
			AddLinks: []LinkRef{{Type: "member", Target: "u1"}, {Type: "member", Target: "u2"}},
		}},
	})
	if d1.Outcome != OutcomeCommitted {
		t.Fatalf("恰好达到上限应允许提交，实际 %v", d1.Outcome)
	}

	before, _ := s.SnapshotOf("g")
	d2 := s.ApplyBatch(Batch{
		ID: "overflow",
		Items: []Item{{
			Instance: "g", Expect: 2,
			SetAttrs: map[string]string{"dirty": "yes"},
			AddLinks: []LinkRef{{Type: "member", Target: "u3"}},
		}},
	})
	if d2.Outcome != OutcomeCardinalityViolation {
		t.Fatalf("超出上限应整体拒绝，实际 %v", d2.Outcome)
	}
	after, _ := s.SnapshotOf("g")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("基数拒绝不得留下痕迹，前 %+v 后 %+v", before, after)
	}
}

// 基数判定必须发生在版本判定之后：版本不满足时即使基数也会违例，
// 仍报告版本冲突。
func TestVersionCheckPrecedesCardinality(t *testing.T) {
	s := NewStore()
	s.SetLinkLimit("member", 0)
	mustCreate(t, s, "g")

	d := s.ApplyBatch(Batch{
		ID: "order",
		Items: []Item{{
			Instance: "g", Expect: 42,
			AddLinks: []LinkRef{{Type: "member", Target: "u1"}},
		}},
	})
	if d.Outcome != OutcomeVersionConflict {
		t.Fatalf("版本判定应先于基数判定，实际 %v", d.Outcome)
	}
}

// 联合批次中各实例版本按各自序列独立推进，不被统一绑定。
func TestVersionsAdvanceIndependently(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "a", "b")

	// 先单独推进 a 三次。
	for v := Version(1); v <= 3; v++ {
		d := s.ApplyBatch(Batch{ID: fmt.Sprintf("solo-%d", v), Items: []Item{{Instance: "a", Expect: v}}})
		if d.Outcome != OutcomeCommitted {
			t.Fatalf("单实例批次应成功，实际 %v", d.Outcome)
		}
	}
	// 联合批次：a 当前版本 4，b 当前版本 1。
	d := s.ApplyBatch(Batch{
		ID: "joint",
		Items: []Item{
			{Instance: "a", Expect: 4},
			{Instance: "b", Expect: 1},
		},
	})
	if d.Outcome != OutcomeCommitted {
		t.Fatalf("联合批次应成功，实际 %v (%s)", d.Outcome, d.Detail)
	}
	snapA, _ := s.SnapshotOf("a")
	snapB, _ := s.SnapshotOf("b")
	if snapA.Version != 5 || snapB.Version != 2 {
		t.Fatalf("版本应各自独立推进为 a=5, b=2，实际 a=%d, b=%d", snapA.Version, snapB.Version)
	}
}

// 被拒绝批次在外部可观察状态上必须与未发生不可区分。
func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	s := NewStore()
	s.SetLinkLimit("member", 1)
	mustCreate(t, s, "a", "b")
	ok := s.ApplyBatch(Batch{
		ID:    "seed",
		Items: []Item{{Instance: "a", Expect: 1, SetAttrs: map[string]string{"k": "v"}, AddLinks: []LinkRef{{Type: "member", Target: "b"}}}},
	})
	if ok.Outcome != OutcomeCommitted {
		t.Fatalf("种子批次应成功，实际 %v", ok.Outcome)
	}
	beforeA, _ := s.SnapshotOf("a")
	beforeB, _ := s.SnapshotOf("b")
	logLen := len(s.Decisions())

	rejects := []Batch{
		{ID: "r-dup", Items: []Item{{Instance: "a", Expect: 2}, {Instance: "a", Expect: 2}}},
		{ID: "r-ver", Items: []Item{{Instance: "a", Expect: 99}, {Instance: "b", Expect: 1}}},
		{ID: "r-card", Items: []Item{{Instance: "a", Expect: 2, AddLinks: []LinkRef{{Type: "member", Target: "b2"}}}}},
	}
	for _, b := range rejects {
		d := s.ApplyBatch(b)
		if d.Outcome == OutcomeCommitted {
			t.Fatalf("批次 %s 不应成功", b.ID)
		}
	}
	afterA, _ := s.SnapshotOf("a")
	afterB, _ := s.SnapshotOf("b")
	if !reflect.DeepEqual(beforeA, afterA) || !reflect.DeepEqual(beforeB, afterB) {
		t.Fatalf("拒绝路径产生了可观察的状态变化")
	}
	if len(s.Decisions()) != logLen+len(rejects) {
		t.Fatalf("每个批次都必须留下判定记录")
	}
}

// 判定开销证据：版本读取次数恒等于批次前置条件数量，与实例总数无关。
func TestReadCostIndependentOfStoreSize(t *testing.T) {
	s := NewStore()
	const total = 20000
	for i := 0; i < total; i++ {
		mustCreate(t, s, InstanceID(fmt.Sprintf("inst-%d", i)))
	}
	for round, k := range []int{1, 3, 7} {
		items := make([]Item, k)
		for i := range items {
			items[i] = Item{Instance: InstanceID(fmt.Sprintf("inst-%d", round*100+i)), Expect: 1}
		}
		d := s.ApplyBatch(Batch{ID: fmt.Sprintf("k%d", k), Items: items})
		if d.Outcome != OutcomeCommitted {
			t.Fatalf("批次应成功，实际 %v", d.Outcome)
		}
		if d.Reads != k {
			t.Fatalf("判定读取次数应等于前置条件数 %d，实际 %d（实例总数 %d）", k, d.Reads, total)
		}
	}
}

// 判定日志必须完整记录每个批次的前置条件、判定依据与结果。
func TestDecisionLogCompleteness(t *testing.T) {
	s := NewStore()
	mustCreate(t, s, "a", "b")
	s.ApplyBatch(Batch{ID: "b1", Items: []Item{{Instance: "a", Expect: 1}, {Instance: "b", Expect: 1}}})
	s.ApplyBatch(Batch{ID: "b2", Items: []Item{{Instance: "a", Expect: 1}}})

	log := s.Decisions()
	if len(log) != 2 {
		t.Fatalf("日志应含 2 条记录，实际 %d", len(log))
	}
	for _, d := range log {
		if d.BatchID == "" || len(d.Items) == 0 || d.Tick == 0 {
			t.Fatalf("记录缺少前置条件或逻辑时刻：%+v", d)
		}
	}
	if log[0].Outcome != OutcomeCommitted || log[0].CommitSeq != 1 {
		t.Fatalf("b1 应成功且提交序为 1，实际 %+v", log[0])
	}
	if log[1].Outcome != OutcomeVersionConflict || log[1].Observed["a"] != 2 {
		t.Fatalf("b2 应因 a 已推进到版本 2 被拒，实际 %+v", log[1])
	}
}
