package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// 随机批次生成器：混合合法/非法条目、批次内外引用、显式 RID、
// 偶发引用环、两种模式与逆操作故障注入。
type batchGen struct {
	rng *rand.Rand
}

func (g *batchGen) ref(entryIDs []string, external []string) Ref {
	switch g.rng.Intn(10) {
	case 0, 1:
		return ExistingRef(external[g.rng.Intn(len(external))])
	case 2:
		return EntryRef("ghost-" + itoa(g.rng.Intn(100))) // 未知条目
	case 3:
		return Ref{} // 空引用
	default:
		if len(entryIDs) == 0 {
			return ExistingRef(external[g.rng.Intn(len(external))])
		}
		return EntryRef(entryIDs[g.rng.Intn(len(entryIDs))])
	}
}

func (g *batchGen) objectEntry(id string, entryIDs, externalPersons []string) Entry {
	e := Entry{ID: id, Kind: EntryObject, Properties: map[string]any{}}
	switch g.rng.Intn(10) {
	case 0:
		e.ObjectTypeRID = "Ghost" // 未知类型
	case 1:
		e.ObjectTypeRID = "Team"
	default:
		e.ObjectTypeRID = "Person"
	}
	if g.rng.Intn(10) < 8 {
		if g.rng.Intn(10) == 0 {
			e.Properties["name"] = g.rng.Intn(100) // 错误类型
		} else {
			e.Properties["name"] = "n-" + id
		}
	}
	if g.rng.Intn(5) == 0 {
		if g.rng.Intn(4) == 0 {
			e.Properties["age"] = "not-an-int"
		} else {
			e.Properties["age"] = g.rng.Intn(100)
		}
	}
	if g.rng.Intn(3) == 0 {
		e.Properties["mentor"] = g.ref(entryIDs, externalPersons)
	}
	if g.rng.Intn(20) == 0 {
		e.Properties["bogus"] = 1 // 未声明属性
	}
	if g.rng.Intn(10) == 0 {
		e.RID = "shared-rid" // 显式 RID，可能相互冲突
	}
	return e
}

func (g *batchGen) linkEntry(id string, entryIDs []string, external []string) Entry {
	e := Entry{ID: id, Kind: EntryLink}
	switch g.rng.Intn(10) {
	case 0:
		e.LinkTypeRID = "NoLink" // 未知链接类型
	case 1, 2, 3:
		e.LinkTypeRID = "MemberOf"
	default:
		e.LinkTypeRID = "Manages"
	}
	e.Source = g.ref(entryIDs, external)
	e.Target = g.ref(entryIDs, external)
	return e
}

// genRequest 生成随机请求与配套的逆操作故障注入集合。
func (g *batchGen) genRequest() (Request, map[string]bool) {
	n := 1 + g.rng.Intn(20)
	var entries []Entry
	var ids []string
	external := []string{"E0", "E1", "E2", "G0"}
	externalPersons := []string{"E0", "E1", "E2"}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("e%d", i)
		if g.rng.Intn(10) < 6 {
			entries = append(entries, g.objectEntry(id, ids, externalPersons))
		} else {
			entries = append(entries, g.linkEntry(id, ids, external))
		}
		ids = append(ids, id)
	}
	// 偶发注入引用环：随机两个对象条目互相 mentor。
	if n >= 2 && g.rng.Intn(10) == 0 {
		i, j := g.rng.Intn(n), g.rng.Intn(n)
		if i != j && entries[i].Kind == EntryObject && entries[j].Kind == EntryObject {
			entries[i].Properties["mentor"] = EntryRef(entries[j].ID)
			entries[j].Properties["mentor"] = EntryRef(entries[i].ID)
		}
	}
	mode := BestEffort
	if g.rng.Intn(2) == 0 {
		mode = Atomic
	}
	// 故障注入：随机若干条目的逆操作失败（按确定性 RID 注入）。
	undoFail := map[string]bool{}
	for i := 0; i < n; i++ {
		if g.rng.Intn(15) == 0 {
			e := entries[i]
			if e.Kind == EntryObject {
				undoFail[assignedObjectRID(&e)] = true
			} else {
				undoFail["lnk-"+e.ID] = true
			}
		}
	}
	return Request{Mode: mode, Entries: entries}, undoFail
}

// seedExternal 为对拍双方准备完全一致的批次外数据。
func seedExternal(t *testing.T, s *Store) {
	t.Helper()
	for _, rid := range []string{"E0", "E1", "E2"} {
		mustNoErr(t, s.SeedObject(ObjectInstance{RID: rid, TypeRID: "Person",
			Properties: map[string]any{"name": rid}}))
	}
	mustNoErr(t, s.SeedObject(ObjectInstance{RID: "G0", TypeRID: "Team"}))
	// E0 已有一条受约束链接，用于触发联合基数冲突。
	mustNoErr(t, s.SeedLink(LinkInstance{RID: "L0", TypeRID: "Manages",
		SourceRID: "E0", TargetRID: "E1"}))
}

func verdictString(rep Report) string {
	var parts []string
	for _, r := range rep.Results {
		parts = append(parts, r.EntryID+":"+r.Verdict.String())
	}
	sort.Strings(parts)
	return fmt.Sprint(parts)
}

// 对拍：优化实现 vs 独立朴素模型，随机批次 + 故障注入。
func TestDifferentialRandomBatches(t *testing.T) {
	const seeds = 2000
	for seed := int64(0); seed < seeds; seed++ {
		g := &batchGen{rng: rand.New(rand.NewSource(seed))}
		req, undoFail := g.genRequest()

		hooks := Hooks{UndoError: func(rid string) error {
			if undoFail[rid] {
				return errInjected
			}
			return nil
		}}

		s1 := newSchemaStore(t)
		seedExternal(t, s1)
		s1.SetHooks(hooks)
		var imp1 *Importer
		if seed < 3 { // 抽样打印完整逐条目日志
			imp1 = NewImporter(s1, testLogger(t))
		} else {
			imp1 = NewImporter(s1, nil)
		}

		s2 := newSchemaStore(t)
		seedExternal(t, s2)
		s2.SetHooks(hooks)

		rep1 := imp1.Import(req)
		rep2 := naiveImport(s2, req)

		fail := func(format string, args ...any) {
			t.Logf("种子=%d 模式=%v 故障注入=%v", seed, req.Mode, undoFail)
			for i, e := range req.Entries {
				t.Logf("  条目[%d]=%+v", i, e)
			}
			t.Logf("优化实现: %+v", rep1)
			t.Logf("朴素模型: %+v", rep2)
			t.Fatalf(format, args...)
		}

		if rep1.Rejected != rep2.Rejected {
			fail("种子=%d: 拒绝判定不一致 optimized=%v naive=%v", seed, rep1.Rejected, rep2.Rejected)
		}
		if rep1.Rejected {
			continue
		}
		if len(rep1.Results) != len(rep2.Results) {
			fail("种子=%d: 结果数不一致", seed)
		}
		for i := range rep1.Results {
			if rep1.Results[i].Verdict != rep2.Results[i].Verdict {
				fail("种子=%d: 条目 %s 判定不一致 optimized=%s naive=%s",
					seed, rep1.Results[i].EntryID, rep1.Results[i].Verdict, rep2.Results[i].Verdict)
			}
		}
		if rep1.RolledBack != rep2.RolledBack {
			fail("种子=%d: 回滚标记不一致", seed)
		}
		if len(rep1.RollbackFailures) != len(rep2.RollbackFailures) {
			fail("种子=%d: 回滚失败数不一致 optimized=%d naive=%d",
				seed, len(rep1.RollbackFailures), len(rep2.RollbackFailures))
		}
		for i := range rep1.RollbackFailures {
			if rep1.RollbackFailures[i].EntryID != rep2.RollbackFailures[i].EntryID {
				fail("种子=%d: 回滚失败条目不一致", seed)
			}
		}
		objs1, links1 := s1.Snapshot()
		objs2, links2 := s2.Snapshot()
		if fmt.Sprint(objs1) != fmt.Sprint(objs2) || fmt.Sprint(links1) != fmt.Sprint(links2) {
			fail("种子=%d: 最终图状态不一致\noptimized=%v %v\nnaive=%v %v",
				seed, objs1, links1, objs2, links2)
		}
		if seed < 3 {
			t.Logf("种子=%d 判定序列: %s", seed, verdictString(rep1))
		}
	}
}
