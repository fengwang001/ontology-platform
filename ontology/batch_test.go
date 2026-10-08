package ontology

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

var errInjected = errors.New("注入的逆操作失败")

func itoa(i int) string { return strconv.Itoa(i) }

// newSchemaStore 构造测试夹具：两个对象类型、两个链接类型。
// Manages 受基数约束（同一源最多一个目标），MemberOf 不受限。
func newSchemaStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	mustNoErr(t, s.RegisterObjectType(ObjectType{RID: "Person", Properties: []PropertySpec{
		{Name: "name", Type: PropString, Required: true},
		{Name: "age", Type: PropInt},
		{Name: "mentor", Type: PropObjectRef, RefObjectType: "Person"},
	}}))
	mustNoErr(t, s.RegisterObjectType(ObjectType{RID: "Team", Properties: []PropertySpec{
		{Name: "title", Type: PropString},
	}}))
	mustNoErr(t, s.RegisterLinkType(LinkType{
		RID: "Manages", SourceObjectType: "Person", TargetObjectType: "Person",
		MaxTargetsPerSource: 1,
	}))
	mustNoErr(t, s.RegisterLinkType(LinkType{
		RID: "MemberOf", SourceObjectType: "Person", TargetObjectType: "Team",
	}))
	return s
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// testLogger 把每个条目的输入、判定结果与依据打印到测试日志。
func testLogger(t *testing.T) func(LogRecord) {
	t.Helper()
	return func(rec LogRecord) {
		t.Logf("条目[%d]=%s 输入=%+v 判定=%s 依据=%s",
			rec.Index, rec.Entry.ID, rec.Entry, rec.Result.Verdict, rec.Result.Reason)
	}
}

func verdictOf(t *testing.T, rep Report, id string) Verdict {
	t.Helper()
	res, ok := rep.Result(id)
	if !ok {
		t.Fatalf("报告中缺少条目 %s", id)
	}
	return res.Verdict
}

// 引用排序：链接条目声明在对象条目之前（前向引用），仍须按依赖顺序落地。
func TestReferenceOrderingAndResolution(t *testing.T) {
	s := newSchemaStore(t)
	var landed []string
	s.SetHooks(Hooks{AfterLand: func(rid string) { landed = append(landed, rid) }})
	imp := NewImporter(s, testLogger(t))
	rep := imp.Import(Request{
		Mode: BestEffort,
		Entries: []Entry{
			{ID: "L1", Kind: EntryLink, LinkTypeRID: "Manages",
				Source: EntryRef("e1"), Target: EntryRef("e2")},
			{ID: "e1", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "Alice"}},
			{ID: "e2", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "Bob", "mentor": EntryRef("e1")}},
		},
	})
	if rep.Rejected {
		t.Fatalf("请求被错误拒绝: %s", rep.RejectReason)
	}
	for _, id := range []string{"e1", "e2", "L1"} {
		if v := verdictOf(t, rep, id); v != VerdictSucceeded {
			t.Fatalf("条目 %s 判定=%s，期望 SUCCEEDED", id, v)
		}
	}
	want := []string{"obj-e1", "obj-e2", "lnk-L1"}
	if strings.Join(landed, ",") != strings.Join(want, ",") {
		t.Fatalf("落地顺序=%v，期望 %v（被引用者先落地）", landed, want)
	}
	obj, ok := s.GetObject("obj-e2")
	if !ok {
		t.Fatal("obj-e2 未落地")
	}
	if obj.Properties["mentor"] != "obj-e1" {
		t.Fatalf("mentor 属性解析为 %v，期望 obj-e1", obj.Properties["mentor"])
	}
}

// 环检测：引用构成环的请求在任何条目落地前被整体拒绝，且无可观察改动。
func TestCycleRejectedBeforeAnyLanding(t *testing.T) {
	s := newSchemaStore(t)
	landings := 0
	s.SetHooks(Hooks{AfterLand: func(rid string) { landings++ }})
	imp := NewImporter(s, testLogger(t))

	rep := imp.Import(Request{
		Mode: Atomic,
		Entries: []Entry{
			{ID: "e1", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "A", "mentor": EntryRef("e2")}},
			{ID: "e2", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "B", "mentor": EntryRef("e1")}},
			{ID: "e3", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "C"}},
		},
	})
	if !rep.Rejected {
		t.Fatal("成环请求未被拒绝")
	}
	if !strings.Contains(rep.RejectReason, "环") {
		t.Fatalf("拒绝原因未说明环: %s", rep.RejectReason)
	}
	if landings != 0 || s.ObjectCount() != 0 || s.LinkCountAll() != 0 {
		t.Fatalf("拒绝产生了可观察改动: landings=%d 对象=%d 链接=%d",
			landings, s.ObjectCount(), s.LinkCountAll())
	}

	// 自环同样构成环。
	rep2 := imp.Import(Request{
		Mode: BestEffort,
		Entries: []Entry{
			{ID: "x1", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "X", "mentor": EntryRef("x1")}},
		},
	})
	if !rep2.Rejected {
		t.Fatal("自环请求未被拒绝")
	}
}

// 重复条目 ID 无法消歧引用，整体拒绝。
func TestDuplicateEntryIDRejected(t *testing.T) {
	s := newSchemaStore(t)
	imp := NewImporter(s, testLogger(t))
	rep := imp.Import(Request{
		Mode: BestEffort,
		Entries: []Entry{
			{ID: "e1", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "A"}},
			{ID: "e1", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "B"}},
		},
	})
	if !rep.Rejected {
		t.Fatal("重复条目 ID 未被拒绝")
	}
	if s.ObjectCount() != 0 {
		t.Fatal("拒绝后图中不应有对象")
	}
}

// 失败沿引用传播：被传播失败的条目不尝试落地，也不再计入自身校验。
func TestFailurePropagation(t *testing.T) {
	s := newSchemaStore(t)
	imp := NewImporter(s, testLogger(t))
	rep := imp.Import(Request{
		Mode: BestEffort,
		Entries: []Entry{
			// e1 自身校验失败（缺少必填属性 name）。
			{ID: "e1", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{}},
			// e2 引用失败的 e1；同时 e2 自身也有非法属性，
			// 但传播失败优先，不得计入它本可能引发的进一步校验。
			{ID: "e2", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "B", "mentor": EntryRef("e1"), "bogus": 1}},
			// e3 引用 e2，失败沿链传播。
			{ID: "e3", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "C", "mentor": EntryRef("e2")}},
			// e4 与失败链无关，不受影响。
			{ID: "e4", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "D"}},
		},
	})
	cases := map[string]Verdict{
		"e1": VerdictValidationFailed,
		"e2": VerdictPropagatedFailed,
		"e3": VerdictPropagatedFailed,
		"e4": VerdictSucceeded,
	}
	for id, want := range cases {
		if v := verdictOf(t, rep, id); v != want {
			t.Fatalf("条目 %s 判定=%s，期望 %s", id, v, want)
		}
	}
	if s.ObjectCount() != 1 {
		t.Fatalf("只有 e4 应落地，实际对象数=%d", s.ObjectCount())
	}
}

// 基数冲突按声明顺序放行：先声明者放行，即使它落地更晚。
func TestCardinalityDeclarationOrder(t *testing.T) {
	s := newSchemaStore(t)
	mustNoErr(t, s.SeedObject(ObjectInstance{RID: "S", TypeRID: "Person",
		Properties: map[string]any{"name": "Boss"}}))
	mustNoErr(t, s.SeedObject(ObjectInstance{RID: "T2", TypeRID: "Person",
		Properties: map[string]any{"name": "Extern"}}))
	var landed []string
	s.SetHooks(Hooks{AfterLand: func(rid string) { landed = append(landed, rid) }})
	imp := NewImporter(s, testLogger(t))
	rep := imp.Import(Request{
		Mode: BestEffort,
		Entries: []Entry{
			// e1 声明最先，但依赖同批次对象 e3，落地最晚。
			{ID: "e1", Kind: EntryLink, LinkTypeRID: "Manages",
				Source: ExistingRef("S"), Target: EntryRef("e3")},
			// e2 无依赖，落地最早，但声明顺序靠后，不得放行。
			{ID: "e2", Kind: EntryLink, LinkTypeRID: "Manages",
				Source: ExistingRef("S"), Target: ExistingRef("T2")},
			{ID: "e3", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "New"}},
		},
	})
	if v := verdictOf(t, rep, "e1"); v != VerdictSucceeded {
		t.Fatalf("声明最先的 e1 判定=%s，期望 SUCCEEDED", v)
	}
	if v := verdictOf(t, rep, "e2"); v != VerdictCardinalityConflict {
		t.Fatalf("声明靠后的 e2 判定=%s，期望 CARDINALITY_CONFLICT", v)
	}
	// 落地顺序确实是 e2 的可落地时刻更早（e2 不等待 e3），
	// 但放行判定只依赖声明顺序。
	if len(landed) != 2 || landed[0] != "obj-e3" || landed[1] != "lnk-e1" {
		t.Fatalf("落地序列=%v，期望 [obj-e3 lnk-e1]", landed)
	}
	if _, ok := s.GetLink("lnk-e2"); ok {
		t.Fatal("e2 不应落地")
	}
	if got := s.LinkCount("S", "Manages"); got != 1 {
		t.Fatalf("S 的 Manages 链接数=%d，期望 1", got)
	}
}

// 联合校验：与批次外既有链接联合判定基数。
func TestJointCardinalityWithExistingLinks(t *testing.T) {
	s := newSchemaStore(t)
	mustNoErr(t, s.SeedObject(ObjectInstance{RID: "S", TypeRID: "Person",
		Properties: map[string]any{"name": "Boss"}}))
	mustNoErr(t, s.SeedObject(ObjectInstance{RID: "T1", TypeRID: "Person",
		Properties: map[string]any{"name": "Old"}}))
	mustNoErr(t, s.SeedObject(ObjectInstance{RID: "T2", TypeRID: "Person",
		Properties: map[string]any{"name": "New"}}))
	mustNoErr(t, s.SeedLink(LinkInstance{RID: "L0", TypeRID: "Manages",
		SourceRID: "S", TargetRID: "T1"}))
	imp := NewImporter(s, testLogger(t))
	rep := imp.Import(Request{
		Mode: BestEffort,
		Entries: []Entry{
			{ID: "e1", Kind: EntryLink, LinkTypeRID: "Manages",
				Source: ExistingRef("S"), Target: ExistingRef("T2")},
		},
	})
	if v := verdictOf(t, rep, "e1"); v != VerdictCardinalityConflict {
		t.Fatalf("e1 判定=%s，期望 CARDINALITY_CONFLICT（与既有链接联合判定）", v)
	}
	if got := s.LinkCount("S", "Manages"); got != 1 {
		t.Fatalf("S 的 Manages 链接数=%d，期望仍为 1", got)
	}
}

// 联合校验开销证明：不随源实例已有链接总数增长。
// 方法：两个源实例分别带有 1000 / 4000 条既有链接，对它们各执行一次
// 单条目导入，断言 (a) 全程零邻接扫描（不遍历已有链接），
// (b) 索引访问次数相同（与已有链接总数无关的常数）。
func TestJointCardinalityConstantCost(t *testing.T) {
	s := newSchemaStore(t)
	seedSourceWithLinks := func(rid string, n int) {
		t.Helper()
		mustNoErr(t, s.SeedObject(ObjectInstance{RID: rid, TypeRID: "Person",
			Properties: map[string]any{"name": rid}}))
		for i := 0; i < n; i++ {
			team := rid + "-team-" + itoa(i)
			mustNoErr(t, s.SeedObject(ObjectInstance{RID: team, TypeRID: "Team"}))
			mustNoErr(t, s.SeedLink(LinkInstance{
				RID: rid + "-lk-" + itoa(i), TypeRID: "MemberOf",
				SourceRID: rid, TargetRID: team,
			}))
		}
	}
	seedSourceWithLinks("S1", 1000)
	seedSourceWithLinks("S2", 4000)
	mustNoErr(t, s.SeedObject(ObjectInstance{RID: "Target", TypeRID: "Person",
		Properties: map[string]any{"name": "T"}}))

	imp := NewImporter(s, testLogger(t))
	ops := make([]Stats, 2)
	for i, src := range []string{"S1", "S2"} {
		s.ResetStats()
		rep := imp.Import(Request{
			Mode: BestEffort,
			Entries: []Entry{
				{ID: "add-" + src, Kind: EntryLink, LinkTypeRID: "Manages",
					Source: ExistingRef(src), Target: ExistingRef("Target")},
			},
		})
		if v := verdictOf(t, rep, "add-"+src); v != VerdictSucceeded {
			t.Fatalf("%s: 判定=%s，期望 SUCCEEDED", src, v)
		}
		ops[i] = s.Stats()
	}
	if ops[0].AdjacencyScans != 0 || ops[1].AdjacencyScans != 0 {
		t.Fatalf("批量导入路径发生了邻接遍历: %+v / %+v", ops[0], ops[1])
	}
	if ops[0].IndexOps != ops[1].IndexOps {
		t.Fatalf("索引访问次数随已有链接数增长: 1000条->%d 次, 4000条->%d 次",
			ops[0].IndexOps, ops[1].IndexOps)
	}
	t.Logf("既有 1000 条与 4000 条链接的源，联合校验索引访问均为 %d 次，邻接遍历 0 次",
		ops[0].IndexOps)
}

// 总体开销证明：索引访问次数随批次条目数线性增长（精确 2 倍关系），
// 引用排序基于哈希邻接（Kahn O(V+E)），不做两两比较。
func TestLinearScalingWithBatchSize(t *testing.T) {
	runBatch := func(nLinks int) Stats {
		s := newSchemaStore(t)
		var entries []Entry
		for i := 0; i < nLinks; i++ {
			p := "p" + itoa(i)
			q := "q" + itoa(i)
			entries = append(entries,
				Entry{ID: p, Kind: EntryObject, ObjectTypeRID: "Person",
					Properties: map[string]any{"name": p}},
				Entry{ID: q, Kind: EntryObject, ObjectTypeRID: "Person",
					Properties: map[string]any{"name": q}},
				Entry{ID: "lk" + itoa(i), Kind: EntryLink, LinkTypeRID: "Manages",
					Source: EntryRef(p), Target: EntryRef(q)},
			)
		}
		s.ResetStats()
		imp := NewImporter(s, nil)
		rep := imp.Import(Request{Mode: BestEffort, Entries: entries})
		for _, r := range rep.Results {
			if r.Verdict != VerdictSucceeded {
				t.Fatalf("条目 %s 意外失败: %s", r.EntryID, r.Reason)
			}
		}
		return s.Stats()
	}
	small := runBatch(500)
	large := runBatch(1000)
	if small.AdjacencyScans != 0 || large.AdjacencyScans != 0 {
		t.Fatal("导入路径不应发生邻接遍历")
	}
	// 每条受约束链接落地恰好 2 次索引访问（联合计数查询 + 插入），对象条目为 0。
	if small.IndexOps != 2*500 || large.IndexOps != 2*1000 {
		t.Fatalf("索引访问非线性: 500链接->%d, 1000链接->%d", small.IndexOps, large.IndexOps)
	}
	t.Logf("索引访问: 500 链接 -> %d 次, 1000 链接 -> %d 次（严格线性）",
		small.IndexOps, large.IndexOps)
}

// BestEffort：部分条目失败不影响其余条目最终落地。
func TestBestEffortPartialFailure(t *testing.T) {
	s := newSchemaStore(t)
	imp := NewImporter(s, testLogger(t))
	rep := imp.Import(Request{
		Mode: BestEffort,
		Entries: []Entry{
			{ID: "ok1", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "A"}},
			{ID: "bad", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": 42}},
			{ID: "ok2", Kind: EntryObject, ObjectTypeRID: "Team",
				Properties: map[string]any{"title": "T"}},
		},
	})
	if rep.RolledBack {
		t.Fatal("BestEffort 模式不应回滚")
	}
	if v := verdictOf(t, rep, "bad"); v != VerdictValidationFailed {
		t.Fatalf("bad 判定=%s，期望 VALIDATION_FAILED", v)
	}
	if _, ok := s.GetObject("obj-ok1"); !ok {
		t.Fatal("ok1 应不受 bad 影响而落地")
	}
	if _, ok := s.GetObject("obj-ok2"); !ok {
		t.Fatal("ok2 应不受 bad 影响而落地")
	}
}

// Atomic：任一失败触发整体回滚，图状态恢复到导入前。
func TestAtomicRollbackRestoresState(t *testing.T) {
	s := newSchemaStore(t)
	mustNoErr(t, s.SeedObject(ObjectInstance{RID: "Ext", TypeRID: "Person",
		Properties: map[string]any{"name": "Ext"}}))
	beforeObjs, beforeLinks := s.Snapshot()
	imp := NewImporter(s, testLogger(t))
	rep := imp.Import(Request{
		Mode: Atomic,
		Entries: []Entry{
			{ID: "o1", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "A"}},
			{ID: "o2", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "B", "mentor": EntryRef("o1")}},
			{ID: "l1", Kind: EntryLink, LinkTypeRID: "Manages",
				Source: ExistingRef("Ext"), Target: EntryRef("o1")},
			{ID: "bad", Kind: EntryObject, ObjectTypeRID: "NoSuchType"},
		},
	})
	if !rep.RolledBack {
		t.Fatal("Atomic 模式发生失败但未回滚")
	}
	for _, id := range []string{"o1", "o2", "l1"} {
		if v := verdictOf(t, rep, id); v != VerdictRolledBack {
			t.Fatalf("条目 %s 判定=%s，期望 ROLLED_BACK", id, v)
		}
	}
	if v := verdictOf(t, rep, "bad"); v != VerdictValidationFailed {
		t.Fatalf("bad 判定=%s，期望 VALIDATION_FAILED", v)
	}
	afterObjs, afterLinks := s.Snapshot()
	if len(afterObjs) != len(beforeObjs) || len(afterLinks) != len(beforeLinks) {
		t.Fatalf("回滚后图状态未恢复: 对象 %d->%d, 链接 %d->%d",
			len(beforeObjs), len(afterObjs), len(beforeLinks), len(afterLinks))
	}
	if got := s.LinkCount("Ext", "Manages"); got != 0 {
		t.Fatalf("基数索引未随回滚恢复: Ext 的 Manages=%d", got)
	}
}

// 回滚按落地逆序进行；单条逆操作失败不中止，继续处理并汇总。
func TestRollbackReverseOrderAndPartialUndoFailure(t *testing.T) {
	s := newSchemaStore(t)
	var undoOrder []string
	s.SetHooks(Hooks{
		UndoError: func(rid string) error {
			undoOrder = append(undoOrder, rid)
			if rid == "obj-e2" {
				return errInjected
			}
			return nil
		},
	})
	imp := NewImporter(s, testLogger(t))
	rep := imp.Import(Request{
		Mode: Atomic,
		Entries: []Entry{
			{ID: "e1", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "A"}},
			{ID: "e2", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "B", "mentor": EntryRef("e1")}},
			{ID: "e3", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "C", "mentor": EntryRef("e2")}},
			{ID: "e4", Kind: EntryLink, LinkTypeRID: "Manages",
				Source: EntryRef("e1"), Target: EntryRef("e3")},
			{ID: "e5", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{}}, // 触发整体回滚
		},
	})
	// 落地顺序 e1,e2,e3,e4，回滚必须严格逆序。
	want := []string{"lnk-e4", "obj-e3", "obj-e2", "obj-e1"}
	if strings.Join(undoOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("回滚顺序=%v，期望逆序 %v", undoOrder, want)
	}
	// obj-e2 逆操作失败：流程未中止，obj-e1 仍被继续撤销。
	if len(rep.RollbackFailures) != 1 || rep.RollbackFailures[0].EntryID != "e2" {
		t.Fatalf("回滚失败汇总=%+v，期望仅 e2", rep.RollbackFailures)
	}
	if v := verdictOf(t, rep, "e2"); v != VerdictSucceeded {
		t.Fatalf("逆操作失败的 e2 判定=%s，期望保持 SUCCEEDED（实例仍在图中）", v)
	}
	if _, ok := s.GetObject("obj-e2"); !ok {
		t.Fatal("obj-e2 逆操作失败，应仍存在于图中")
	}
	if _, ok := s.GetObject("obj-e1"); ok {
		t.Fatal("obj-e1 应已被撤销（e2 的失败不得中止后续回滚）")
	}
	for _, id := range []string{"e1", "e3", "e4"} {
		if v := verdictOf(t, rep, id); v != VerdictRolledBack {
			t.Fatalf("条目 %s 判定=%s，期望 ROLLED_BACK", id, v)
		}
	}
}

// 五类判定两两可区分：一个 Atomic 批次同时产生全部五类。
func TestFiveVerdictsPairwiseDistinguishable(t *testing.T) {
	s := newSchemaStore(t)
	for _, rid := range []string{"S", "T1", "T2"} {
		mustNoErr(t, s.SeedObject(ObjectInstance{RID: rid, TypeRID: "Person",
			Properties: map[string]any{"name": rid}}))
	}
	s.SetHooks(Hooks{
		UndoError: func(rid string) error {
			if rid == "obj-keep" {
				return errInjected
			}
			return nil
		},
	})
	imp := NewImporter(s, testLogger(t))
	rep := imp.Import(Request{
		Mode: Atomic,
		Entries: []Entry{
			{ID: "ok", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "A"}}, // -> ROLLED_BACK
			{ID: "bad", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": 1}}, // -> VALIDATION_FAILED
			{ID: "dep", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "C", "mentor": EntryRef("bad")}}, // -> PROPAGATED_FAILED
			{ID: "l1", Kind: EntryLink, LinkTypeRID: "Manages",
				Source: ExistingRef("S"), Target: ExistingRef("T1")}, // -> ROLLED_BACK
			{ID: "l2", Kind: EntryLink, LinkTypeRID: "Manages",
				Source: ExistingRef("S"), Target: ExistingRef("T2")}, // -> CARDINALITY_CONFLICT
			{ID: "keep", Kind: EntryObject, ObjectTypeRID: "Person",
				Properties: map[string]any{"name": "K"}}, // 逆操作失败 -> SUCCEEDED
		},
	})
	want := map[string]Verdict{
		"ok":   VerdictRolledBack,
		"bad":  VerdictValidationFailed,
		"dep":  VerdictPropagatedFailed,
		"l1":   VerdictRolledBack,
		"l2":   VerdictCardinalityConflict,
		"keep": VerdictSucceeded,
	}
	seen := map[Verdict]bool{}
	for id, w := range want {
		got := verdictOf(t, rep, id)
		if got != w {
			t.Fatalf("条目 %s 判定=%s，期望 %s", id, got, w)
		}
		seen[got] = true
	}
	if len(seen) != 5 {
		t.Fatalf("五类判定未全部出现且两两可区分，实际=%v", seen)
	}
	if len(rep.RollbackFailures) != 1 || rep.RollbackFailures[0].EntryID != "keep" {
		t.Fatalf("回滚失败汇总=%+v，期望仅 keep", rep.RollbackFailures)
	}
}
