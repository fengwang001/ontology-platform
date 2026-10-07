package ontology

import (
	"strings"
	"sync"
	"testing"
)

// sliceLogger 收集判定日志供断言。
type sliceLogger struct {
	mu      sync.Mutex
	entries []DecisionLog
}

func (l *sliceLogger) LogDecision(e DecisionLog) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
}

func (l *sliceLogger) forRecord(index int) []DecisionLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []DecisionLog
	for _, e := range l.entries {
		if e.Scope == "record" && e.Index == index {
			out = append(out, e)
		}
	}
	return out
}

// newFixture 构造基础夹具：主体 alice；类型 Ticket 必需属性 title；
// alice 可写 title/summary，不可写 secret。
func newFixture() (*Store, *sliceLogger) {
	store := NewStore()
	store.AddSubject("alice")
	store.AddObjectType(ObjectType{Name: "Ticket", RequiredProps: []string{"title"}})
	store.SetPermission(PermissionEntry{Subject: "alice", ObjectType: "Ticket", Property: "title", Writable: true})
	store.SetPermission(PermissionEntry{Subject: "alice", ObjectType: "Ticket", Property: "summary", Writable: true})
	store.SetPermission(PermissionEntry{Subject: "alice", ObjectType: "Ticket", Property: "secret", Writable: false})
	logger := &sliceLogger{}
	return store, logger
}

func mustFail(t *testing.T, r RecordResult, cat FailureCategory) {
	t.Helper()
	if r.Status != StatusFailed || r.FailCategory != cat {
		t.Fatalf("期望失败类别 %s，得到 %+v", cat, r)
	}
}

// 整批级拒绝优先级：模式参数缺失或非法 > 发起主体不存在。
func TestBatchRejectsInvalidModeBeforeUnknownSubject(t *testing.T) {
	store := NewStore() // 无任何主体
	imp := NewBatchImporter(store, nil)

	res := imp.Execute(BatchRequest{Mode: "", Subject: "ghost", Records: []Record{{ObjectType: "Ticket"}}})
	if !res.Rejected || !strings.Contains(res.RejectReason, "模式") {
		t.Fatalf("期望模式非法优先拒绝，得到 %+v", res)
	}

	res = imp.Execute(BatchRequest{Mode: ModeAtomic, Subject: "ghost", Records: []Record{{ObjectType: "Ticket"}}})
	if !res.Rejected || !strings.Contains(res.RejectReason, "主体") {
		t.Fatalf("期望主体不存在拒绝，得到 %+v", res)
	}
}

// 相同输入在两种模式下产生不同结果：原子模式整条失败，宽松模式部分成功。
func TestSameInputDifferentResultAcrossModes(t *testing.T) {
	run := func(mode ImportMode) (*Store, BatchResult) {
		store, _ := newFixture()
		imp := NewBatchImporter(store, nil)
		res := imp.Execute(BatchRequest{
			Mode:    mode,
			Subject: "alice",
			Records: []Record{{
				ObjectType: "Ticket",
				ObjectID:   "T1",
				Semantic:   SemanticCreate,
				Fields:     map[string]string{"title": "hello", "secret": "x"},
			}},
		})
		return store, res
	}

	atomicStore, atomicRes := run(ModeAtomic)
	mustFail(t, atomicRes.Results[0], FailPermissionDenied)
	if _, exists := atomicStore.GetObject("Ticket", "T1"); exists {
		t.Fatal("原子模式失败不得写入任何字段，对象不应存在")
	}

	lenientStore, lenientRes := run(ModeLenient)
	got := lenientRes.Results[0]
	if got.Status != StatusPartial {
		t.Fatalf("宽松模式期望部分成功，得到 %+v", got)
	}
	if len(got.Skipped) != 1 || got.Skipped[0] != "secret" {
		t.Fatalf("跳过字段清单应为 [secret]，得到 %v", got.Skipped)
	}
	obj, exists := lenientStore.GetObject("Ticket", "T1")
	if !exists || obj.Props["title"] != "hello" {
		t.Fatalf("可写字段应已写入，得到 %+v exists=%v", obj, exists)
	}
	if _, leaked := obj.Props["secret"]; leaked {
		t.Fatal("被跳过字段不得写入")
	}
}

// 宽松模式必需属性二次判定·反面：必需属性被跳过且无旧值可沿用，整条失败。
func TestLenientRequiredConstraintNegative(t *testing.T) {
	store, _ := newFixture()
	// title 为必需属性，但 alice 对 title 的写权限被收回。
	store.SetPermission(PermissionEntry{Subject: "alice", ObjectType: "Ticket", Property: "title", Writable: false})
	imp := NewBatchImporter(store, nil)

	res := imp.Execute(BatchRequest{
		Mode:    ModeLenient,
		Subject: "alice",
		Records: []Record{{
			ObjectType: "Ticket",
			ObjectID:   "T1",
			Semantic:   SemanticCreate,
			Fields:     map[string]string{"title": "hello", "summary": "s"},
		}},
	})
	mustFail(t, res.Results[0], FailRequiredConstraint)
	if _, exists := store.GetObject("Ticket", "T1"); exists {
		t.Fatal("必需属性不满足时须整体回退，对象不应存在")
	}
}

// 宽松模式必需属性二次判定·正面：更新语义下被跳过的必需属性有旧值可沿用。
func TestLenientRequiredConstraintPositiveWithOldValue(t *testing.T) {
	store, _ := newFixture()
	store.PutObject(Object{TypeName: "Ticket", ID: "T1", Props: map[string]string{"title": "old"}})
	store.SetPermission(PermissionEntry{Subject: "alice", ObjectType: "Ticket", Property: "title", Writable: false})
	imp := NewBatchImporter(store, nil)

	res := imp.Execute(BatchRequest{
		Mode:    ModeLenient,
		Subject: "alice",
		Records: []Record{{
			ObjectType: "Ticket",
			ObjectID:   "T1",
			Semantic:   SemanticUpdate,
			Fields:     map[string]string{"title": "new", "summary": "s"},
		}},
	})
	got := res.Results[0]
	if got.Status != StatusPartial || len(got.Skipped) != 1 || got.Skipped[0] != "title" {
		t.Fatalf("期望部分成功且跳过 title，得到 %+v", got)
	}
	obj, _ := store.GetObject("Ticket", "T1")
	if obj.Props["title"] != "old" || obj.Props["summary"] != "s" {
		t.Fatalf("旧值应沿用且可写字段应写入，得到 %+v", obj.Props)
	}
}

// 创建与更新语义的旧值沿用差异：同样的跳过，更新可沿用旧值，创建直接失败。
func TestCreateVsUpdateOldValueReuse(t *testing.T) {
	run := func(semantic RecordSemantic) RecordResult {
		store, _ := newFixture()
		store.SetPermission(PermissionEntry{Subject: "alice", ObjectType: "Ticket", Property: "title", Writable: false})
		if semantic == SemanticUpdate {
			store.PutObject(Object{TypeName: "Ticket", ID: "T1", Props: map[string]string{"title": "old"}})
		}
		imp := NewBatchImporter(store, nil)
		res := imp.Execute(BatchRequest{
			Mode:    ModeLenient,
			Subject: "alice",
			Records: []Record{{
				ObjectType: "Ticket",
				ObjectID:   "T1",
				Semantic:   semantic,
				Fields:     map[string]string{"title": "new", "summary": "s"},
			}},
		})
		return res.Results[0]
	}

	created := run(SemanticCreate)
	mustFail(t, created, FailRequiredConstraint)

	updated := run(SemanticUpdate)
	if updated.Status != StatusPartial {
		t.Fatalf("更新语义应可沿用旧值而部分成功，得到 %+v", updated)
	}
}

// 记录级优先级与语义不匹配：对不存在对象声明更新、对已存在对象声明创建。
func TestSemanticMismatchAndTypeNotFound(t *testing.T) {
	store, _ := newFixture()
	store.PutObject(Object{TypeName: "Ticket", ID: "T9", Props: map[string]string{"title": "old"}})
	imp := NewBatchImporter(store, nil)

	res := imp.Execute(BatchRequest{
		Mode:    ModeAtomic,
		Subject: "alice",
		Records: []Record{
			{ObjectType: "Ghost", ObjectID: "G1", Semantic: SemanticCreate, Fields: map[string]string{"title": "x"}},
			{ObjectType: "Ticket", ObjectID: "T1", Semantic: SemanticUpdate, Fields: map[string]string{"title": "x"}},
			{ObjectType: "Ticket", ObjectID: "T9", Semantic: SemanticCreate, Fields: map[string]string{"title": "x"}},
			{ObjectType: "Ticket", ObjectID: "T2", Semantic: SemanticCreate, Fields: map[string]string{"title": "ok"}},
		},
	})
	mustFail(t, res.Results[0], FailTypeNotFound)
	mustFail(t, res.Results[1], FailSemanticMismatch)
	mustFail(t, res.Results[2], FailSemanticMismatch)
	// 批内记录独立判定：前三条失败不得传染第四条。
	if res.Results[3].Status != StatusSuccess {
		t.Fatalf("记录间不得传染失败，得到 %+v", res.Results[3])
	}
	if _, exists := store.GetObject("Ticket", "T2"); !exists {
		t.Fatal("独立记录应成功写入")
	}
}

// 权限快照：批执行期间收回权限，已开始处理的批次仍按发起时刻快照判定。
func TestPermissionSnapshotImmuneToMidRunChanges(t *testing.T) {
	store, _ := newFixture()
	imp := NewBatchImporter(store, nil)

	req := BatchRequest{
		Mode:    ModeAtomic,
		Subject: "alice",
		Records: []Record{
			{ObjectType: "Ticket", ObjectID: "T1", Semantic: SemanticCreate, Fields: map[string]string{"title": "a"}},
			{ObjectType: "Ticket", ObjectID: "T2", Semantic: SemanticCreate, Fields: map[string]string{"title": "b"}},
		},
	}
	// 第一条记录处理完后收回 title 写权限；第二条仍须按快照判定为可写。
	hook := func(recordIndex int) {
		if recordIndex == 0 {
			store.SetPermission(PermissionEntry{Subject: "alice", ObjectType: "Ticket", Property: "title", Writable: false})
		}
	}
	// 单线程测试直接调用内核（不加锁），hook 内可安全变更权限。
	res := imp.executeLocked(req, hook)

	for i, r := range res.Results {
		if r.Status != StatusSuccess {
			t.Fatalf("记录 %d 应按发起时刻快照判定为成功，得到 %+v", i, r)
		}
	}
	obj, exists := store.GetObject("Ticket", "T2")
	if !exists || obj.Props["title"] != "b" {
		t.Fatalf("快照内权限应仍然有效，得到 %+v exists=%v", obj, exists)
	}
}

// 日志：每次判定都须打印输入、输出与依据。
func TestDecisionLogCoversEveryRecord(t *testing.T) {
	store, logger := newFixture()
	imp := NewBatchImporter(store, logger)

	res := imp.Execute(BatchRequest{
		Mode:    ModeLenient,
		Subject: "alice",
		Records: []Record{
			{ObjectType: "Ticket", ObjectID: "T1", Semantic: SemanticCreate, Fields: map[string]string{"title": "a", "secret": "x"}},
			{ObjectType: "Ghost", ObjectID: "G1", Semantic: SemanticCreate, Fields: map[string]string{"title": "a"}},
		},
	})
	for i := range res.Results {
		entries := logger.forRecord(i)
		if len(entries) != 1 {
			t.Fatalf("记录 %d 应恰好有一条判定日志，得到 %d 条", i, len(entries))
		}
		e := entries[0]
		if e.Input == "" || e.Output == "" || e.Basis == "" {
			t.Fatalf("记录 %d 日志缺字段: %+v", i, e)
		}
	}
}

// 性能：单条记录判定访问的权限条目数与批规模、历史条目总数无关。
// 通过快照的访问计数器直接验证：访问次数 == 进入权限判定阶段的字段总数。
func TestPermissionLookupCountIndependentOfScale(t *testing.T) {
	accessesFor := func(history, records int) int {
		store := NewStore()
		store.AddSubject("alice")
		store.AddObjectType(ObjectType{Name: "Ticket"})
		// 堆积大量历史权限条目（其它主体/其它属性）。
		for i := 0; i < history; i++ {
			store.SetPermission(PermissionEntry{
				Subject:    "someone-else",
				ObjectType: "Ticket",
				Property:   "prop-" + strings.Repeat("p", i%7) + "-" + string(rune('a'+i%26)) + "-" + strings.Repeat("q", i%5),
				Writable:   i%2 == 0,
			})
		}
		store.SetPermission(PermissionEntry{Subject: "alice", ObjectType: "Ticket", Property: "title", Writable: true})
		store.SetPermission(PermissionEntry{Subject: "alice", ObjectType: "Ticket", Property: "summary", Writable: true})

		recs := make([]Record, records)
		for i := range recs {
			recs[i] = Record{
				ObjectType: "Ticket",
				ObjectID:   "T" + strings.Repeat("x", i%3) + "-" + string(rune('0'+i%10)) + "-" + strings.Repeat("y", i%4),
				Semantic:   SemanticCreate,
				Fields:     map[string]string{"title": "a", "summary": "b"},
			}
		}
		// 保证 ObjectID 唯一。
		seen := map[string]bool{}
		for i := range recs {
			for seen[recs[i].ObjectID] {
				recs[i].ObjectID += "z"
			}
			seen[recs[i].ObjectID] = true
		}

		imp := NewBatchImporter(store, nil)
		snap := store.snapshotPermissionsLocked()
		for i := range recs {
			imp.processRecord(BatchRequest{Mode: ModeAtomic, Subject: "alice"}, snap, recs[i], i)
		}
		got := snap.Accesses()
		return got
	}

	// 每条记录 2 个字段，每条恰好 2 次条目访问，与历史条目数、批规模均无关。
	for _, history := range []int{10, 1000, 100000} {
		for _, records := range []int{1, 50, 500} {
			if got, want := accessesFor(history, records), 2*records; got != want {
				t.Fatalf("history=%d records=%d: 访问条目数 %d，期望 %d", history, records, got, want)
			}
		}
	}
}

// 并发：多个批次并发执行的最终可观察结果须等价于某个串行顺序。
// 各批次操作互不相交的对象，故所有串行顺序的最终状态一致，可直接比对。
func TestConcurrentBatchesSerializable(t *testing.T) {
	store, _ := newFixture()
	imp := NewBatchImporter(store, nil)

	const batches = 8
	const perBatch = 10
	reqs := make([]BatchRequest, batches)
	for b := 0; b < batches; b++ {
		recs := make([]Record, perBatch)
		for i := range recs {
			recs[i] = Record{
				ObjectType: "Ticket",
				ObjectID:   "B" + strings.Repeat("b", b) + "-" + strings.Repeat("i", i+1),
				Semantic:   SemanticCreate,
				Fields:     map[string]string{"title": "t"},
			}
		}
		reqs[b] = BatchRequest{Mode: ModeAtomic, Subject: "alice", Records: recs}
	}

	var wg sync.WaitGroup
	results := make([]BatchResult, batches)
	for b := 0; b < batches; b++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = imp.Execute(reqs[idx])
		}(b)
	}
	wg.Wait()

	for b := 0; b < batches; b++ {
		for i, r := range results[b].Results {
			if r.Status != StatusSuccess {
				t.Fatalf("批 %d 记录 %d 应成功，得到 %+v", b, i, r)
			}
		}
		for _, rec := range reqs[b].Records {
			obj, exists := store.GetObject(rec.ObjectType, rec.ObjectID)
			if !exists || obj.Props["title"] != "t" {
				t.Fatalf("对象 %s 应已写入，得到 %+v exists=%v", rec.ObjectID, obj, exists)
			}
		}
	}
}
