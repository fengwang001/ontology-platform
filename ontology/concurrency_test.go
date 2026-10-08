package ontology

import (
	"sync"
	"testing"
)

// 并发批次互不相交：并发执行结果等价于按某个串行顺序执行。
func TestConcurrentDisjointBatches(t *testing.T) {
	buildBatch := func(prefix string) Request {
		var entries []Entry
		for i := 0; i < 10; i++ {
			p := prefix + "-p" + itoa(i)
			q := prefix + "-q" + itoa(i)
			entries = append(entries,
				Entry{ID: p, Kind: EntryObject, ObjectTypeRID: "Person",
					Properties: map[string]any{"name": p}},
				Entry{ID: q, Kind: EntryObject, ObjectTypeRID: "Person",
					Properties: map[string]any{"name": q, "mentor": EntryRef(p)}},
				Entry{ID: prefix + "-lk" + itoa(i), Kind: EntryLink, LinkTypeRID: "Manages",
					Source: EntryRef(p), Target: EntryRef(q)},
			)
		}
		return Request{Mode: BestEffort, Entries: entries}
	}
	reqA, reqB := buildBatch("a"), buildBatch("b")

	// 并发执行（共享同一存储与导入器）。
	s := newSchemaStore(t)
	imp := NewImporter(s, testLogger(t))
	var wg sync.WaitGroup
	var repA, repB Report
	wg.Add(2)
	go func() { defer wg.Done(); repA = imp.Import(reqA) }()
	go func() { defer wg.Done(); repB = imp.Import(reqB) }()
	wg.Wait()
	for _, rep := range []Report{repA, repB} {
		for _, r := range rep.Results {
			if r.Verdict != VerdictSucceeded {
				t.Fatalf("不相交批次中条目 %s 意外失败: %s", r.EntryID, r.Reason)
			}
		}
	}

	// 串行参照：A 后 B。
	ref := newSchemaStore(t)
	refImp := NewImporter(ref, nil)
	refImp.Import(reqA)
	refImp.Import(reqB)

	gotObjs, gotLinks := s.Snapshot()
	wantObjs, wantLinks := ref.Snapshot()
	if len(gotObjs) != len(wantObjs) || len(gotLinks) != len(wantLinks) {
		t.Fatalf("并发结果与串行不等价: 对象 %d/%d, 链接 %d/%d",
			len(gotObjs), len(wantObjs), len(gotLinks), len(wantLinks))
	}
	for rid := range wantObjs {
		if _, ok := gotObjs[rid]; !ok {
			t.Fatalf("并发结果缺少对象 %s", rid)
		}
	}
	for rid := range wantLinks {
		if _, ok := gotLinks[rid]; !ok {
			t.Fatalf("并发结果缺少链接 %s", rid)
		}
	}
}

// 并发批次争用同一既有实例的受约束链接：只有一个请求生效。
func TestConcurrentContendedSameInstance(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		s := newSchemaStore(t)
		for _, rid := range []string{"S", "Ta", "Tb"} {
			mustNoErr(t, s.SeedObject(ObjectInstance{RID: rid, TypeRID: "Person",
				Properties: map[string]any{"name": rid}}))
		}
		imp := NewImporter(s, nil)
		reqA := Request{Mode: BestEffort, Entries: []Entry{
			{ID: "a1", Kind: EntryLink, LinkTypeRID: "Manages",
				Source: ExistingRef("S"), Target: ExistingRef("Ta")},
		}}
		reqB := Request{Mode: BestEffort, Entries: []Entry{
			{ID: "b1", Kind: EntryLink, LinkTypeRID: "Manages",
				Source: ExistingRef("S"), Target: ExistingRef("Tb")},
		}}
		var wg sync.WaitGroup
		var repA, repB Report
		wg.Add(2)
		go func() { defer wg.Done(); repA = imp.Import(reqA) }()
		go func() { defer wg.Done(); repB = imp.Import(reqB) }()
		wg.Wait()

		va := verdictOf(t, repA, "a1")
		vb := verdictOf(t, repB, "b1")
		succeeded := 0
		for _, v := range []Verdict{va, vb} {
			if v == VerdictSucceeded {
				succeeded++
			} else if v != VerdictCardinalityConflict {
				t.Fatalf("第 %d 轮: 争用失败应为 CARDINALITY_CONFLICT，实际=%s", trial, v)
			}
		}
		if succeeded != 1 {
			t.Fatalf("第 %d 轮: 成功数=%d，期望恰好 1（a=%s b=%s）", trial, succeeded, va, vb)
		}
		if got := s.LinkCount("S", "Manages"); got != 1 {
			t.Fatalf("第 %d 轮: S 的 Manages 链接数=%d，期望 1", trial, got)
		}
	}
}

// Atomic 模式的隔离边界：整个导入是一个临界区，并发读者只能看到
// 导入前或导入后的状态，绝不可能读到将被回滚的中间条目。
func TestAtomicIsolationBoundary(t *testing.T) {
	s := newSchemaStore(t)
	landed := make(chan struct{})
	s.SetHooks(Hooks{AfterLand: func(rid string) {
		// Atomic 模式下该钩子在持有写锁期间触发。
		if rid == "obj-e1" {
			close(landed)
		}
	}})
	imp := NewImporter(s, testLogger(t))
	done := make(chan struct{})
	go func() {
		defer close(done)
		imp.Import(Request{
			Mode: Atomic,
			Entries: []Entry{
				{ID: "e1", Kind: EntryObject, ObjectTypeRID: "Person",
					Properties: map[string]any{"name": "A"}},
				{ID: "bad", Kind: EntryObject, ObjectTypeRID: "Person",
					Properties: map[string]any{}}, // 触发回滚
			},
		})
	}()
	<-landed // 此时 obj-e1 已落地但将被回滚，且写锁仍被导入持有
	readerDone := make(chan bool, 1)
	go func() {
		_, found := s.GetObject("obj-e1") // 读锁被阻塞，直到导入结束
		readerDone <- found
	}()
	<-done
	found := <-readerDone
	if found {
		t.Fatal("读者观察到了将被回滚的中间条目，违反 Atomic 隔离边界")
	}
}

// BestEffort 模式的可见性：单条目落地即对外可见，并发读者可能读到
// 中间态（本测试确定性地观察到 e1 已落地而 e2 尚未落地）。
func TestBestEffortIntermediateVisibility(t *testing.T) {
	s := newSchemaStore(t)
	landed := make(chan struct{})
	proceed := make(chan struct{})
	s.SetHooks(Hooks{AfterLand: func(rid string) {
		// BestEffort 模式下该钩子在释放写锁之后触发。
		if rid == "obj-e1" {
			close(landed)
			<-proceed
		}
	}})
	imp := NewImporter(s, testLogger(t))
	done := make(chan struct{})
	go func() {
		defer close(done)
		imp.Import(Request{
			Mode: BestEffort,
			Entries: []Entry{
				{ID: "e1", Kind: EntryObject, ObjectTypeRID: "Person",
					Properties: map[string]any{"name": "A"}},
				{ID: "e2", Kind: EntryObject, ObjectTypeRID: "Person",
					Properties: map[string]any{"name": "B"}},
			},
		})
	}()
	<-landed
	if _, found := s.GetObject("obj-e1"); !found {
		t.Fatal("BestEffort 下已落地条目应对读者可见")
	}
	if _, found := s.GetObject("obj-e2"); found {
		t.Fatal("e2 尚未落地，不应可见")
	}
	close(proceed)
	<-done
	if _, found := s.GetObject("obj-e2"); !found {
		t.Fatal("导入结束后 e2 应可见")
	}
}
