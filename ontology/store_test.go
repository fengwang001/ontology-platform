package ontology

import (
	"reflect"
	"testing"
)

// ticketType 是测试用对象类型：两个不可合并属性 + 三个可合并属性。
func ticketType() *ObjectType {
	return &ObjectType{
		Name: "ticket",
		Properties: []PropertySpec{
			{Name: "status", Mergeable: false},
			{Name: "assignee", Mergeable: false},
			{Name: "priority", Mergeable: true, MergeRule: RuleMaxInt},
			{Name: "tags", Mergeable: true, MergeRule: RuleSetUnion},
			{Name: "summary", Mergeable: true, MergeRule: RuleLWW},
		},
	}
}

func ticketInitial() map[string]Value {
	return map[string]Value{
		"status":   "open",
		"assignee": "nobody",
		"priority": int64(0),
		"tags":     []string{},
		"summary":  LWWValue{},
	}
}

func newTicketStore(t *testing.T, opts ...Option) *Store {
	t.Helper()
	s := NewStore(opts...)
	if err := s.CreateInstance("T1", ticketType(), ticketInitial()); err != nil {
		t.Fatalf("创建实例失败: %v", err)
	}
	return s
}

func mustApply(t *testing.T, s *Store, req WriteRequest) WriteResult {
	t.Helper()
	res, err := s.Apply(req)
	if err != nil {
		t.Fatalf("Apply(%+v) 出错: %v", req, err)
	}
	return res
}

// TestMixedRequestAtomicRejection 同一请求同时触及可合并与不可合并属性，
// 不可合并部分冲突时整条请求必须原子地整体拒绝：
// 可合并部分不得单独生效，版本号不得变化。
func TestMixedRequestAtomicRejection(t *testing.T) {
	s := newTicketStore(t)

	r1 := mustApply(t, s, WriteRequest{
		RequestID: "w1", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]Value{"status": "closed"},
	})
	if r1.Outcome != OutcomeCommitted || r1.Version != 1 {
		t.Fatalf("w1 结果异常: %+v", r1)
	}

	// w2 与 w1 并发（基线同为 0）：status 冲突，priority 本可合并。
	r2 := mustApply(t, s, WriteRequest{
		RequestID: "w2", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]Value{"status": "done", "priority": int64(9)},
	})
	if r2.Outcome != OutcomeRejectedConflict || r2.ConflictProperty != "status" {
		t.Fatalf("w2 应整体拒绝: %+v", r2)
	}

	version, values, ok := s.Snapshot("T1")
	if !ok {
		t.Fatal("实例不存在")
	}
	if version != 1 {
		t.Fatalf("拒绝后版本号变为 %d", version)
	}
	if values["status"] != "closed" {
		t.Fatalf("status 被改动: %v", values["status"])
	}
	if values["priority"] != int64(0) {
		t.Fatalf("可合并属性 priority 被单独合并生效: %v", values["priority"])
	}
}

// TestEquivalentWriteSameValue 并发的两次写入对同一不可合并属性声明
// 恰好相同的新值：两者都必须成功，且只产生一次实际状态变化。
func TestEquivalentWriteSameValue(t *testing.T) {
	s := newTicketStore(t)

	r1 := mustApply(t, s, WriteRequest{
		RequestID: "w1", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]Value{"status": "closed"},
	})
	r2 := mustApply(t, s, WriteRequest{
		RequestID: "w2", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]Value{"status": "closed"},
	})
	if r1.Outcome != OutcomeCommitted || r2.Outcome != OutcomeCommitted {
		t.Fatalf("等价写应均成功: %+v %+v", r1, r2)
	}
	version, values, _ := s.Snapshot("T1")
	if version != 1 {
		t.Fatalf("等价写应只产生一次实际状态变化，版本为 %d", version)
	}
	if values["status"] != "closed" {
		t.Fatalf("status=%v", values["status"])
	}
	// 第二次为 no-op 提交。
	entries := s.Log().Entries()
	if len(entries) != 2 || !entries[1].NoOp {
		t.Fatalf("第二次写入应记录为 no-op 提交: %+v", entries)
	}
}

// TestBaselineConflictPriorToConflict 基线版本冲突必须优先于
// 不可合并属性冲突判定，且三种结果互斥。
func TestBaselineConflictPriorToConflict(t *testing.T) {
	s := newTicketStore(t, WithRetentionWindow(2))

	// 推进到版本 3，使最低可接受基线为 1。
	for i, st := range []string{"a", "b", "c"} {
		mustApply(t, s, WriteRequest{
			RequestID: st, InstanceID: "T1", BaseVersion: uint64(i),
			Changes: map[string]Value{"status": st},
		})
	}
	// 基线 0 已落后保留水位；同时 status 也存在不可合并冲突。
	// 必须判定为基线冲突而非属性冲突。
	r := mustApply(t, s, WriteRequest{
		RequestID: "w", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]Value{"status": "zzz"},
	})
	if r.Outcome != OutcomeRejectedStaleBaseline {
		t.Fatalf("基线冲突应优先判定，实际: %+v", r)
	}
	// 基线超过当前版本同样判定为基线冲突。
	r = mustApply(t, s, WriteRequest{
		RequestID: "w2", InstanceID: "T1", BaseVersion: 99,
		Changes: map[string]Value{"status": "zzz"},
	})
	if r.Outcome != OutcomeRejectedStaleBaseline {
		t.Fatalf("未来基线应判定为基线冲突，实际: %+v", r)
	}
}

// TestRejectedWriteInvisible 被拒绝的写入在外部可观察状态上
// 必须与从未发生不可区分：版本、属性值均不变。
func TestRejectedWriteInvisible(t *testing.T) {
	s := newTicketStore(t)
	mustApply(t, s, WriteRequest{
		RequestID: "w1", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]Value{"status": "closed", "priority": int64(3)},
	})
	vBefore, valsBefore, _ := s.Snapshot("T1")

	mustApply(t, s, WriteRequest{
		RequestID: "w2", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]Value{
			"status":   "done",
			"priority": int64(8),
			"tags":     []string{"x"},
		},
	})
	vAfter, valsAfter, _ := s.Snapshot("T1")
	if vBefore != vAfter {
		t.Fatalf("拒绝后版本变化: %d -> %d", vBefore, vAfter)
	}
	if !reflect.DeepEqual(valsBefore, valsAfter) {
		t.Fatalf("拒绝后属性值变化: %v -> %v", valsBefore, valsAfter)
	}
}

// TestMixedRequestCommit 同一请求触及可合并与不可合并属性且
// 不可合并部分无冲突时，整条请求原子生效。
func TestMixedRequestCommit(t *testing.T) {
	s := newTicketStore(t)
	r := mustApply(t, s, WriteRequest{
		RequestID: "w1", InstanceID: "T1", BaseVersion: 0,
		Changes: map[string]Value{
			"status":   "closed",
			"priority": int64(5),
			"tags":     []string{"b", "a"},
		},
	})
	if r.Outcome != OutcomeCommitted || r.Version != 1 {
		t.Fatalf("应整体提交: %+v", r)
	}
	_, values, _ := s.Snapshot("T1")
	if values["status"] != "closed" || values["priority"] != int64(5) {
		t.Fatalf("提交后状态异常: %v", values)
	}
	if !reflect.DeepEqual(values["tags"], []string{"a", "b"}) {
		t.Fatalf("tags 应为排序规范形: %v", values["tags"])
	}
}

// TestMergeOrderIndependence 连续多轮并发写入后，可合并属性的
// 最终合并结果必须与写入到达的物理顺序无关。
func TestMergeOrderIndependence(t *testing.T) {
	writes := []WriteRequest{
		{RequestID: "a", InstanceID: "T1", BaseVersion: 0, Changes: map[string]Value{
			"priority": int64(3), "tags": []string{"x"},
			"summary": LWWValue{Clock: 2, Writer: "alice", Data: "hello"},
		}},
		{RequestID: "b", InstanceID: "T1", BaseVersion: 0, Changes: map[string]Value{
			"priority": int64(7), "tags": []string{"y", "x"},
			"summary": LWWValue{Clock: 5, Writer: "bob", Data: "world"},
		}},
		{RequestID: "c", InstanceID: "T1", BaseVersion: 0, Changes: map[string]Value{
			"priority": int64(5), "tags": []string{"z"},
			"summary": LWWValue{Clock: 5, Writer: "carol", Data: "!"},
		}},
		{RequestID: "d", InstanceID: "T1", BaseVersion: 0, Changes: map[string]Value{
			"priority": int64(1), "tags": []string{"x", "w"},
			"summary": LWWValue{Clock: 1, Writer: "dan", Data: "old"},
		}},
	}

	// 枚举全部排列，最终属性状态必须完全一致。
	// 注意：版本号等于“产生实际变更的提交数”，no-op 提交不推进版本，
	// 因此版本号可随顺序不同；其与朴素模型的逐顺序对照见 compare_test.go。
	var want map[string]Value
	perm := make([]int, len(writes))
	for i := range perm {
		perm[i] = i
	}
	first := true
	for _, order := range permutations(perm) {
		s := newTicketStore(t)
		for _, idx := range order {
			mustApply(t, s, writes[idx])
		}
		_, values, _ := s.Snapshot("T1")
		if first {
			want = values
			first = false
			continue
		}
		if !reflect.DeepEqual(values, want) {
			t.Fatalf("顺序 %v 得到 %v，期望 %v", order, values, want)
		}
	}
	//  sanity：合并结果内容正确。
	if want["priority"] != int64(7) {
		t.Fatalf("priority=%v", want["priority"])
	}
	if !reflect.DeepEqual(want["tags"], []string{"w", "x", "y", "z"}) {
		t.Fatalf("tags=%v", want["tags"])
	}
	if want["summary"] != (LWWValue{Clock: 5, Writer: "carol", Data: "!"}) {
		t.Fatalf("summary=%v", want["summary"])
	}
}

func permutations(items []int) [][]int {
	var out [][]int
	var rec func(prefix []int, rest []int)
	rec = func(prefix, rest []int) {
		if len(rest) == 0 {
			cp := make([]int, len(prefix))
			copy(cp, prefix)
			out = append(out, cp)
			return
		}
		for i, v := range rest {
			next := make([]int, 0, len(rest)-1)
			next = append(next, rest[:i]...)
			next = append(next, rest[i+1:]...)
			rec(append(prefix, v), next)
		}
	}
	rec(nil, items)
	return out
}

// TestConflictCheckCostIndependentOfHistory 不可合并属性的冲突判定开销
// 不得随历史版本总数增长：无论积累多少版本，单次判定读取每属性版本号
// 的次数恰好等于请求触及的不可合并属性数，且历史扫描次数恒为 0。
func TestConflictCheckCostIndependentOfHistory(t *testing.T) {
	s := newTicketStore(t)

	// 积累大量历史版本（只动可合并属性，避免干扰后续判定）。
	const rounds = 5000
	for i := 0; i < rounds; i++ {
		version, _, _ := s.Snapshot("T1")
		mustApply(t, s, WriteRequest{
			RequestID: "m", InstanceID: "T1", BaseVersion: version,
			Changes: map[string]Value{"priority": int64(i + 1)},
		})
	}

	readsBefore := s.PropVersionReads()
	version, _, _ := s.Snapshot("T1")
	// 一次触及两个不可合并属性的写入。
	mustApply(t, s, WriteRequest{
		RequestID: "probe", InstanceID: "T1", BaseVersion: version,
		Changes: map[string]Value{"status": "x", "assignee": "y"},
	})
	if got := s.PropVersionReads() - readsBefore; got != 2 {
		t.Fatalf("单次判定读取属性版本号 %d 次，期望恰为触及的不可合并属性数 2", got)
	}
	if s.HistoryScans() != 0 {
		t.Fatalf("判定过程扫描了历史记录 %d 次", s.HistoryScans())
	}
}
