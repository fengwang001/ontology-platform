package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand"
	"sync"
	"testing"
)

// ---- 测试构造辅助 ----

type attrSpec struct {
	name      string
	value     string
	writtenAt uint64
}

func at(name, value string, writtenAt uint64) attrSpec {
	return attrSpec{name, value, writtenAt}
}

func obj(id string, attrs ...attrSpec) ObjectEntry {
	e := ObjectEntry{ObjectID: id, Attributes: map[string]AttributeValue{}}
	for _, a := range attrs {
		e.Attributes[a.name] = AttributeValue{Value: a.value, WrittenAt: a.writtenAt}
	}
	return e
}

func snap(id string, priority, position uint64, objs ...ObjectEntry) Snapshot {
	return Snapshot{ReplicaID: id, Priority: priority, Position: position, Objects: objs}
}

func encodeAll(snaps ...Snapshot) [][]byte {
	blobs := make([][]byte, len(snaps))
	for i, s := range snaps {
		blobs[i] = EncodeSnapshot(s)
	}
	return blobs
}

// corruptWhole 使头部校验失败，模拟快照整体不可读。
func corruptWhole(t *testing.T, s Snapshot) []byte {
	t.Helper()
	var env envelopeJSON
	if err := json.Unmarshal(EncodeSnapshot(s), &env); err != nil {
		t.Fatal(err)
	}
	env.Header.Position++
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// corruptEntry 篡改指定对象条目的取值但不更新校验和，模拟局部不可读。
func corruptEntry(t *testing.T, s Snapshot, objectID string) []byte {
	t.Helper()
	var env envelopeJSON
	if err := json.Unmarshal(EncodeSnapshot(s), &env); err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range env.Entries {
		if env.Entries[i].ObjectID != objectID {
			continue
		}
		for name, a := range env.Entries[i].Attributes {
			a.Value += "-tampered"
			env.Entries[i].Attributes[name] = a
		}
		found = true
	}
	if !found {
		t.Fatalf("对象 %s 不存在", objectID)
	}
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func canonical(t *testing.T, res *Result) string {
	t.Helper()
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func reconcileOrFail(t *testing.T, blobs [][]byte) *Result {
	t.Helper()
	res, err := NewReconciler().Reconcile(blobs)
	if err != nil {
		t.Fatalf("和解失败: %v", err)
	}
	return res
}

func findIssue(res *Result, objectID, attr string) *ObjectIssue {
	for i := range res.Report.Issues {
		issue := &res.Report.Issues[i]
		if issue.ObjectID == objectID && issue.Attribute == attr {
			return issue
		}
	}
	return nil
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---- 位点基准 ----

func TestBaselineIsEarliestPosition(t *testing.T) {
	r1 := snap("r1", 10, 5, obj("o1", at("a", "x", 5)))
	r2 := snap("r2", 20, 9, obj("o1", at("a", "y", 7), at("b", "keep", 3)))

	res := reconcileOrFail(t, encodeAll(r1, r2))

	if res.Baseline != 5 {
		t.Fatalf("基准应为最早位点 5，实际 %d", res.Baseline)
	}
	// r2 优先级更高，但其 o1.a 写于位点 7，晚于基准 5，不得计入。
	if got := res.State["o1"]["a"]; got != "x" {
		t.Fatalf("o1.a 应为基准位点上的 x，实际 %q", got)
	}
	// r2 的 o1.b 写于位点 3，早于基准，应计入。
	if got := res.State["o1"]["b"]; got != "keep" {
		t.Fatalf("o1.b 应为 keep，实际 %q", got)
	}
	if got := res.Report.Stats.EntriesExcludedByBaseline; got != 1 {
		t.Fatalf("应有 1 条取值因晚于基准被排除，实际 %d", got)
	}
}

// ---- 冲突裁决 ----

func TestPriorityAdjudicationStable(t *testing.T) {
	r1 := snap("r1", 10, 5, obj("o1", at("a", "x", 5)))
	r2 := snap("r2", 20, 5, obj("o1", at("a", "y", 5)))
	r3 := snap("r3", 15, 5, obj("o1", at("a", "z", 5)))

	blobs := encodeAll(r1, r2, r3)
	res := reconcileOrFail(t, blobs)
	if got := res.State["o1"]["a"]; got != "y" {
		t.Fatalf("优先级最高的 r2 应胜出，实际 %q", got)
	}

	// 到达顺序不影响裁决结果。
	want := canonical(t, res)
	perms := [][]int{{2, 1, 0}, {1, 2, 0}, {2, 0, 1}, {0, 2, 1}, {1, 0, 2}}
	for _, p := range perms {
		got := canonical(t, reconcileOrFail(t, [][]byte{blobs[p[0]], blobs[p[1]], blobs[p[2]]}))
		if got != want {
			t.Fatalf("排列 %v 的结果与基准不一致", p)
		}
	}

	// 副本数量变化不改变既有裁决。
	res2 := reconcileOrFail(t, encodeAll(r1, r2))
	if got := res2.State["o1"]["a"]; got != "y" {
		t.Fatalf("去掉 r3 后裁决不应变化，实际 %q", got)
	}
}

func TestConflictTieIsUnreconcilable(t *testing.T) {
	r1 := snap("r1", 10, 5, obj("o1", at("a", "x", 5), at("b", "ok", 5)))
	r2 := snap("r2", 10, 5, obj("o1", at("a", "y", 5), at("b", "ok", 5)))
	r3 := snap("r3", 5, 5, obj("o1", at("a", "z", 5), at("b", "ok", 5)))

	blobs := encodeAll(r1, r2, r3)
	res := reconcileOrFail(t, blobs)

	// 最高优先级（10）上 x 与 y 并列：o1.a 不可和解，不得出现在结果中。
	if _, ok := res.State["o1"]["a"]; ok {
		t.Fatalf("并列属性 o1.a 不应出现在和解结果中")
	}
	issue := findIssue(res, "o1", "a")
	if issue == nil || issue.Reason != ReasonConflictTie {
		t.Fatalf("o1.a 应报告为 conflict_tie 不可和解，实际 %+v", issue)
	}
	// 无冲突属性不受影响。
	if got := res.State["o1"]["b"]; got != "ok" {
		t.Fatalf("o1.b 应正常裁决为 ok，实际 %q", got)
	}
	// 并列时不得退回到达顺序裁决：两种顺序结果完全一致。
	reversed := [][]byte{blobs[2], blobs[1], blobs[0]}
	if got := canonical(t, reconcileOrFail(t, reversed)); got != canonical(t, res) {
		t.Fatalf("并列情形下到达顺序不应影响结果")
	}
}

// ---- 损坏隔离 ----

func TestWholeCorruptionIsolated(t *testing.T) {
	r1 := snap("r1", 10, 5, obj("o1", at("a", "bad", 5)))
	r2 := snap("r2", 10, 5, obj("o1", at("a", "good", 5)))
	r3 := snap("r3", 10, 5, obj("o1", at("a", "good", 5)))

	blobs := [][]byte{corruptWhole(t, r1), EncodeSnapshot(r2), EncodeSnapshot(r3)}
	res := reconcileOrFail(t, blobs)

	if got := res.State["o1"]["a"]; got != "good" {
		t.Fatalf("损坏副本不得参与裁决，o1.a 应为 good，实际 %q", got)
	}
	if len(res.Report.Faults) != 1 || res.Report.Faults[0].Kind != CorruptWhole {
		t.Fatalf("应记录 1 条整体损坏，实际 %+v", res.Report.Faults)
	}
	if len(res.Report.Participants) != 2 {
		t.Fatalf("应有 2 个参与副本，实际 %v", res.Report.Participants)
	}
}

func TestPartialCorruptionIsolatedAndInsufficient(t *testing.T) {
	r1 := snap("r1", 10, 5, obj("o1", at("a", "x", 5)), obj("solo", at("s", "v", 5)))
	r2 := snap("r2", 20, 5, obj("o1", at("a", "x", 5)))

	blobs := [][]byte{corruptEntry(t, r1, "o1"), EncodeSnapshot(r2)}
	res := reconcileOrFail(t, blobs)

	// 局部损坏同样导致整个副本被隔离，但不影响其余副本和解。
	if len(res.Report.Faults) != 1 || res.Report.Faults[0].Kind != CorruptPartial {
		t.Fatalf("应记录 1 条局部损坏，实际 %+v", res.Report.Faults)
	}
	if res.Report.Faults[0].ReplicaID != "r1" {
		t.Fatalf("局部损坏应能识别副本 ID，实际 %q", res.Report.Faults[0].ReplicaID)
	}
	if got := res.State["o1"]["a"]; got != "x" {
		t.Fatalf("o1.a 应由 r2 正常裁决为 x，实际 %q", got)
	}
	// solo 仅存在于被隔离副本中：来源不足，不可和解，且与冲突并列区分。
	issue := findIssue(res, "solo", "")
	if issue == nil || issue.Reason != ReasonInsufficientParticipants {
		t.Fatalf("solo 应报告为 insufficient_participants，实际 %+v", issue)
	}
	if _, ok := res.State["solo"]; ok {
		t.Fatalf("solo 不应出现在和解结果中")
	}
}

func TestCorruptionAndConflictCoexist(t *testing.T) {
	r1 := snap("r1", 99, 5, obj("o1", at("a", "bad", 5)))
	r2 := snap("r2", 10, 5, obj("o1", at("a", "x", 5)))
	r3 := snap("r3", 20, 5, obj("o1", at("a", "y", 5)))

	blobs := [][]byte{corruptWhole(t, r1), EncodeSnapshot(r2), EncodeSnapshot(r3)}
	res := reconcileOrFail(t, blobs)

	// 损坏副本优先级最高但被隔离；r2/r3 之间按优先级正常裁决。
	if got := res.State["o1"]["a"]; got != "y" {
		t.Fatalf("o1.a 应由优先级 20 的 r3 胜出为 y，实际 %q", got)
	}
	if len(res.Report.Faults) != 1 {
		t.Fatalf("应记录 1 条损坏，实际 %+v", res.Report.Faults)
	}
	if len(res.Report.Issues) != 0 {
		t.Fatalf("不应有不可和解项，实际 %+v", res.Report.Issues)
	}
}

// ---- 错误分类与优先级 ----

func TestInsufficientReplicas(t *testing.T) {
	if _, err := NewReconciler().Reconcile(nil); !errors.Is(err, ErrInsufficientReplicas) {
		t.Fatalf("空输入应返回 ErrInsufficientReplicas，实际 %v", err)
	}

	r1 := snap("r1", 10, 5, obj("o1", at("a", "x", 5)))
	if _, err := NewReconciler().Reconcile([][]byte{corruptWhole(t, r1)}); !errors.Is(err, ErrInsufficientReplicas) {
		t.Fatalf("全部损坏应返回 ErrInsufficientReplicas，实际 %v", err)
	}

	// 单个可读副本即可确定位点基准。
	res := reconcileOrFail(t, encodeAll(r1))
	if res.Baseline != 5 || res.State["o1"]["a"] != "x" {
		t.Fatalf("单副本和解应成功，实际 baseline=%d state=%v", res.Baseline, res.State)
	}
}

// ---- 并发与确定性 ----

// conflictFixture 构造一组包含冲突、并列与不同位点的多副本输入。
func conflictFixture() [][]byte {
	return encodeAll(
		snap("r1", 10, 5,
			obj("o1", at("a", "x", 5), at("b", "keep", 4)),
			obj("o2", at("a", "p", 5)),
			obj("o3", at("a", "t1", 5))),
		snap("r2", 20, 5,
			obj("o1", at("a", "y", 5), at("b", "keep", 4)),
			obj("o2", at("a", "q", 5)),
			obj("o3", at("a", "t2", 5))),
		snap("r3", 20, 8,
			obj("o1", at("a", "y", 5), at("b", "late", 7)),
			obj("o2", at("a", "q", 5)),
			obj("o3", at("a", "t2", 5))),
		snap("r4", 5, 5,
			obj("o1", at("a", "w", 5)),
			obj("o4", at("z", "only-r4", 5))),
	)
}

func TestConcurrentReconcileIdentical(t *testing.T) {
	blobs := conflictFixture()
	want := canonical(t, reconcileOrFail(t, blobs))

	digests := make([]string, len(blobs))
	for i, b := range blobs {
		digests[i] = digestOf(b)
	}

	const workers = 64
	errs := make(chan string, workers)
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := NewReconciler().Reconcile(blobs)
			if err != nil {
				errs <- err.Error()
				return
			}
			data, err := json.Marshal(res)
			if err != nil {
				errs <- err.Error()
				return
			}
			if string(data) != want {
				errs <- "并发调用结果不一致"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}

	// 和解过程不得修改任何原始快照输入。
	for i, b := range blobs {
		if got := digestOf(b); got != digests[i] {
			t.Errorf("输入快照 %d 被修改", i)
		}
	}
}

func TestDeterministicAcrossOrderings(t *testing.T) {
	blobs := conflictFixture()
	want := canonical(t, reconcileOrFail(t, blobs))

	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 50; i++ {
		perm := rng.Perm(len(blobs))
		shuffled := make([][]byte, len(blobs))
		for j, p := range perm {
			shuffled[j] = blobs[p]
		}
		if got := canonical(t, reconcileOrFail(t, shuffled)); got != want {
			t.Fatalf("第 %d 次乱序和解的结果发生漂移", i)
		}
	}
}

// ---- 工作量可复核性 ----

func TestAdjudicationCostScalesWithConflictsOnly(t *testing.T) {
	// 构造 objects 个对象、其中 2 个存在冲突的三副本输入。
	build := func(objects int) [][]byte {
		snaps := make([]Snapshot, 3)
		for r := 0; r < 3; r++ {
			s := Snapshot{
				ReplicaID: string(rune('a' + r)),
				Priority:  uint64(10 * (r + 1)),
				Position:  1,
			}
			for i := 0; i < objects; i++ {
				id := string(rune(i))
				value := "same"
				if i < 2 {
					// 前两个对象在各副本间取值不同，构成真实冲突。
					value = string(rune('v' + r))
				}
				s.Objects = append(s.Objects, obj(id, at("a", value, 1)))
			}
			snaps[r] = s
		}
		return encodeAll(snaps...)
	}

	small := reconcileOrFail(t, build(10))
	large := reconcileOrFail(t, build(5000))

	if small.Report.Stats.AdjudicationsRun != 2 || large.Report.Stats.AdjudicationsRun != 2 {
		t.Fatalf("裁决次数应等于真实冲突数 2，实际 small=%d large=%d",
			small.Report.Stats.AdjudicationsRun, large.Report.Stats.AdjudicationsRun)
	}
	if small.Report.Stats.PriorityComparisons != large.Report.Stats.PriorityComparisons {
		t.Fatalf("优先级比较次数不应随无冲突对象数增长，small=%d large=%d",
			small.Report.Stats.PriorityComparisons, large.Report.Stats.PriorityComparisons)
	}
	if large.Report.Stats.EntriesScanned <= small.Report.Stats.EntriesScanned {
		t.Fatalf("索引扫描应随条目数线性增长，small=%d large=%d",
			small.Report.Stats.EntriesScanned, large.Report.Stats.EntriesScanned)
	}
}
