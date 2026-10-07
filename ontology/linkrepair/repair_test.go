package linkrepair

import (
	"reflect"
	"sync"
	"testing"
)

func objSet(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func reasonsOf(r Report) []Reason {
	out := make([]Reason, len(r.Verdicts))
	for i, v := range r.Verdicts {
		out[i] = v.Reason
	}
	return out
}

func keptKeys(r Report) [][3]string {
	out := make([][3]string, 0, len(r.Kept))
	for _, k := range r.Kept {
		out = append(out, [3]string{k.LinkTypeID, k.SourceID, k.TargetID})
	}
	return out
}

// 单一基数：候选数量为 1（无冲突）、2、4，固定优先规则始终选字典序最小对方。
func TestToOneCardinalityConflict(t *testing.T) {
	types := []LinkType{{ID: "owns", MaxA: 1, MaxB: 1}}
	objs := objSet("o1", "p_a", "p_b", "p_c", "p_d", "solo")

	cases := []struct {
		name        string
		records     []RawRecord
		wantKeep    [][3]string
		wantReasons []Reason
	}{
		{
			name: "single candidate no conflict",
			records: []RawRecord{
				{ID: "e1", LinkTypeID: "owns", SourceID: "solo", TargetID: "p_a"},
			},
			wantKeep:    [][3]string{{"owns", "solo", "p_a"}},
			wantReasons: []Reason{ReasonNone},
		},
		{
			name: "two candidates pick lexicographically smaller",
			records: []RawRecord{
				{ID: "e1", LinkTypeID: "owns", SourceID: "o1", TargetID: "p_b"},
				{ID: "e2", LinkTypeID: "owns", SourceID: "o1", TargetID: "p_a"},
			},
			wantKeep:    [][3]string{{"owns", "o1", "p_a"}},
			wantReasons: []Reason{ReasonCardinalityConflict, ReasonNone},
		},
		{
			name: "four candidates regardless of input order",
			records: []RawRecord{
				{ID: "e1", LinkTypeID: "owns", SourceID: "o1", TargetID: "p_d"},
				{ID: "e2", LinkTypeID: "owns", SourceID: "o1", TargetID: "p_a"},
				{ID: "e3", LinkTypeID: "owns", SourceID: "o1", TargetID: "p_c"},
				{ID: "e4", LinkTypeID: "owns", SourceID: "o1", TargetID: "p_b"},
			},
			wantKeep: [][3]string{{"owns", "o1", "p_a"}},
			wantReasons: []Reason{
				ReasonCardinalityConflict, ReasonNone,
				ReasonCardinalityConflict, ReasonCardinalityConflict,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: tc.records})
			if got := keptKeys(rep); !reflect.DeepEqual(got, tc.wantKeep) {
				t.Fatalf("kept=%v want %v", got, tc.wantKeep)
			}
			if got := reasonsOf(rep); !reflect.DeepEqual(got, tc.wantReasons) {
				t.Fatalf("reasons=%v want %v", got, tc.wantReasons)
			}
		})
	}
}

// 带上限的多值基数：恰好达到上限全部保留；恰好超出 1 条时按固定规则舍弃末位。
func TestCappedMultiValued(t *testing.T) {
	types := []LinkType{{ID: "tagged", MaxA: 3, MaxB: 0}}
	objs := objSet("o1", "t1", "t2", "t3", "t4")

	t.Run("at limit keeps all", func(t *testing.T) {
		recs := []RawRecord{
			{LinkTypeID: "tagged", SourceID: "o1", TargetID: "t3"},
			{LinkTypeID: "tagged", SourceID: "o1", TargetID: "t1"},
			{LinkTypeID: "tagged", SourceID: "o1", TargetID: "t2"},
		}
		rep := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs})
		if len(rep.Kept) != 3 {
			t.Fatalf("kept %d, want 3", len(rep.Kept))
		}
	})

	t.Run("exceeded by exactly one drops lexicographic tail", func(t *testing.T) {
		recs := []RawRecord{
			{ID: "e1", LinkTypeID: "tagged", SourceID: "o1", TargetID: "t4"},
			{ID: "e2", LinkTypeID: "tagged", SourceID: "o1", TargetID: "t2"},
			{ID: "e3", LinkTypeID: "tagged", SourceID: "o1", TargetID: "t1"},
			{ID: "e4", LinkTypeID: "tagged", SourceID: "o1", TargetID: "t3"},
		}
		rep := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs})
		want := [][3]string{
			{"tagged", "o1", "t1"},
			{"tagged", "o1", "t2"},
			{"tagged", "o1", "t3"},
		}
		if got := keptKeys(rep); !reflect.DeepEqual(got, want) {
			t.Fatalf("kept=%v want %v", got, want)
		}
		if rep.Verdicts[0].Reason != ReasonCardinalityConflict {
			t.Fatalf("t4 verdict=%v want cardinality_conflict", rep.Verdicts[0].Reason)
		}
	})
}

// 残留不完整记录与重复记录混合出现，且彼此可区分。
func TestIncompleteAndDuplicateMixture(t *testing.T) {
	types := []LinkType{{ID: "owns", MaxA: 1, MaxB: 1}}
	objs := objSet("o1", "o2", "p1", "p2")
	recs := []RawRecord{
		{ID: "e1", LinkTypeID: "owns", SourceID: "o1", TargetID: "p1"},  // 保留
		{ID: "e2", LinkTypeID: "owns", SourceID: "o1", TargetID: "p1"},  // 重复 e1
		{ID: "e3", LinkTypeID: "owns", SourceID: "o2"},                  // 残留：B 端丢失
		{ID: "e4", LinkTypeID: "owns", TargetID: "p2"},                  // 残留：A 端丢失
		{ID: "e5", SourceID: "o1", TargetID: "p1"},                      // 残留：链接类型丢失
		{ID: "e6", LinkTypeID: "ghost", SourceID: "o1", TargetID: "p1"}, // 未知链接类型
	}
	rep := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs})
	want := []Reason{
		ReasonNone,
		ReasonDuplicate,
		ReasonMalformed,
		ReasonMalformed,
		ReasonMalformed,
		ReasonMalformed,
	}
	if got := reasonsOf(rep); !reflect.DeepEqual(got, want) {
		t.Fatalf("reasons=%v want %v", got, want)
	}
	if len(rep.Kept) != 1 {
		t.Fatalf("kept %d records, want 1: %+v", len(rep.Kept), rep.Kept)
	}
}

// 结构完好但引用对象不可用：独立判定，不与结构损坏/基数/重复混淆。
func TestReferencedObjectUnavailable(t *testing.T) {
	types := []LinkType{{ID: "owns", MaxA: 1, MaxB: 1}}
	objs := objSet("o1", "p1") // o2、p2 不可用
	recs := []RawRecord{
		{ID: "e1", LinkTypeID: "owns", SourceID: "o1", TargetID: "p1"},
		{ID: "e2", LinkTypeID: "owns", SourceID: "o1", TargetID: "p2"},
		{ID: "e3", LinkTypeID: "owns", SourceID: "o2", TargetID: "p1"},
		{ID: "e4", LinkTypeID: "owns", SourceID: "o2", TargetID: "p2"},
	}
	rep := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs})
	want := []Reason{
		ReasonNone,
		ReasonReferencedUnavailable,
		ReasonReferencedUnavailable,
		ReasonReferencedUnavailable,
	}
	if got := reasonsOf(rep); !reflect.DeepEqual(got, want) {
		t.Fatalf("reasons=%v want %v", got, want)
	}

	// 同一内容的重复副本引用了不可用对象：引用判定优先于重复判定。
	recs2 := []RawRecord{
		{LinkTypeID: "owns", SourceID: "o2", TargetID: "p9"},
		{LinkTypeID: "owns", SourceID: "o2", TargetID: "p9"},
	}
	rep2 := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs2})
	for i, v := range rep2.Verdicts {
		if v.Reason != ReasonReferencedUnavailable {
			t.Fatalf("record %d reason=%v want referenced_unavailable", i, v.Reason)
		}
	}
}

// 固定优先级：基数落败内容键的重复副本归类为基数冲突而非重复。
func TestCardinalityBeatsDuplicate(t *testing.T) {
	types := []LinkType{{ID: "owns", MaxA: 1, MaxB: 1}}
	objs := objSet("o1", "p1", "p2")
	recs := []RawRecord{
		{ID: "w1", LinkTypeID: "owns", SourceID: "o1", TargetID: "p2"},
		{ID: "w2", LinkTypeID: "owns", SourceID: "o1", TargetID: "p2"},
		{ID: "x1", LinkTypeID: "owns", SourceID: "o1", TargetID: "p1"},
	}
	rep := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs})
	want := []Reason{
		ReasonCardinalityConflict,
		ReasonCardinalityConflict,
		ReasonNone,
	}
	if got := reasonsOf(rep); !reflect.DeepEqual(got, want) {
		t.Fatalf("reasons=%v want %v", got, want)
	}
}

// 同一对象同时卷入两种链接类型的基数冲突，两类裁决相互独立。
func TestMultipleLinkTypesIndependent(t *testing.T) {
	types := []LinkType{
		{ID: "owns", MaxA: 1, MaxB: 1},
		{ID: "authored", MaxA: 2, MaxB: 1},
	}
	objs := objSet("o1", "a", "b", "c", "d")
	recs := []RawRecord{
		{LinkTypeID: "owns", SourceID: "o1", TargetID: "b"},
		{LinkTypeID: "owns", SourceID: "o1", TargetID: "a"},
		{LinkTypeID: "authored", SourceID: "o1", TargetID: "d"},
		{LinkTypeID: "authored", SourceID: "o1", TargetID: "c"},
	}
	rep := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs})
	want := [][3]string{
		{"authored", "o1", "c"},
		{"authored", "o1", "d"},
		{"owns", "o1", "a"},
	}
	if got := keptKeys(rep); !reflect.DeepEqual(got, want) {
		t.Fatalf("kept=%v want %v", got, want)
	}
}

// B 端（target）侧的基数约束同样独立生效。
func TestTargetSideCardinality(t *testing.T) {
	types := []LinkType{{ID: "assign", MaxA: 0, MaxB: 1}}
	objs := objSet("u1", "u2", "issue")
	recs := []RawRecord{
		{LinkTypeID: "assign", SourceID: "u1", TargetID: "issue"},
		{LinkTypeID: "assign", SourceID: "u2", TargetID: "issue"},
	}
	rep := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs})
	want := [][3]string{{"assign", "u1", "issue"}}
	if got := keptKeys(rep); !reflect.DeepEqual(got, want) {
		t.Fatalf("kept=%v want %v", got, want)
	}
}

// 并发与重复执行：同一份快照反复裁决，保留集合与全部判定完全一致；
// 原始输入不被修改。
func TestIdempotentConcurrentAndImmutable(t *testing.T) {
	types := []LinkType{
		{ID: "owns", MaxA: 1, MaxB: 1},
		{ID: "tagged", MaxA: 2, MaxB: 0},
	}
	objs := objSet("o1", "a", "b", "c", "t1", "t2", "t3") // "dead" 故意不可用
	original := []RawRecord{
		{ID: "r1", LinkTypeID: "owns", SourceID: "o1", TargetID: "b"},
		{ID: "r2", LinkTypeID: "owns", SourceID: "o1", TargetID: "a"},
		{ID: "r3", LinkTypeID: "owns", SourceID: "o1", TargetID: "a"},
		{ID: "r4", LinkTypeID: "owns", SourceID: "o1"},
		{ID: "r5", LinkTypeID: "tagged", SourceID: "o1", TargetID: "t3"},
		{ID: "r6", LinkTypeID: "tagged", SourceID: "o1", TargetID: "t1"},
		{ID: "r7", LinkTypeID: "tagged", SourceID: "o1", TargetID: "t2"},
		{ID: "r8", LinkTypeID: "owns", SourceID: "o1", TargetID: "dead"},
	}

	snapshot := func() Snapshot {
		// 每次调用都复制输入，防止测试本身污染原始数据。
		recs := append([]RawRecord(nil), original...)
		return Snapshot{
			LinkTypes:        append([]LinkType(nil), types...),
			AvailableObjects: objs,
			Records:          recs,
		}
	}

	baseline := Repair(snapshot())

	// 顺序重复执行 10 次。
	for i := 0; i < 10; i++ {
		rep := Repair(snapshot())
		if !reflect.DeepEqual(rep, baseline) {
			t.Fatalf("iteration %d drifted from baseline", i)
		}
	}

	// 并发执行 32 路，各自拿到与基线完全一致的结果。
	var wg sync.WaitGroup
	errs := make(chan string, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rep := Repair(snapshot())
			if !reflect.DeepEqual(rep, baseline) {
				errs <- "concurrent repair drifted"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}

	// 原始输入切片不被修复过程修改。
	want := append([]RawRecord(nil), original...)
	if !reflect.DeepEqual(original, want) {
		t.Fatal("input records were mutated by Repair")
	}
}

// 保留结果中不得引用任何不可用对象：随机场景之外的直接不变量检查。
func TestKeptNeverReferencesUnavailable(t *testing.T) {
	types := []LinkType{{ID: "owns", MaxA: 1, MaxB: 1}}
	objs := objSet("o1", "p1")
	recs := []RawRecord{
		{LinkTypeID: "owns", SourceID: "o1", TargetID: "p1"},
		{LinkTypeID: "owns", SourceID: "o1", TargetID: "ghost1"},
		{LinkTypeID: "owns", SourceID: "ghost2", TargetID: "p1"},
	}
	rep := Repair(Snapshot{LinkTypes: types, AvailableObjects: objs, Records: recs})
	for _, k := range rep.Kept {
		if _, ok := objs[k.SourceID]; !ok {
			t.Fatalf("kept record references unavailable source %q", k.SourceID)
		}
		if _, ok := objs[k.TargetID]; !ok {
			t.Fatalf("kept record references unavailable target %q", k.TargetID)
		}
	}
}
