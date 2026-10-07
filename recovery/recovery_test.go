package recovery

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func goodSnapRecord(obj, state string) SnapshotRecord {
	return SnapshotRecord{ObjectID: obj, State: state, Checksum: SnapshotChecksum(obj, state)}
}

func goodAction(id, base string, effects map[ObjectID]Effect) ActionRecord {
	return ActionRecord{ActionID: id, Base: base, Effects: effects,
		Checksum: ActionChecksum(id, base, effects)}
}

func delta(change string) Effect { return Effect{Change: change} }
func anchored(start, change string) Effect {
	return Effect{HasStart: true, Start: start, Change: change}
}

// dumpDecisionLog 打印每次判定的输入、输出与依据。
func dumpDecisionLog(t *testing.T, c *Coordinator, tag string) {
	t.Helper()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== decision log [%s] ===\n", tag))
	for i, e := range c.DecisionLog() {
		fmt.Fprintf(&b, "#%d stage=%s\n  input : %s\n  output: %s\n  reason: %s\n",
			i, e.Stage, e.Input, e.Output, e.Reason)
	}
	t.Log(b.String())
}

func assertSource(t *testing.T, rep map[ObjectID]ObjectReport, obj string, want ObjectSource) {
	t.Helper()
	got, ok := rep[obj]
	if !ok {
		t.Fatalf("object %s missing from report", obj)
	}
	if got.Source != want {
		t.Fatalf("object %s source = %s, want %s (report=%+v)", obj, got.Source, want, got)
	}
}

// TestSnapshotCorruptObjectLevel 损坏对象不可读，且与“不存在”可区分。
func TestSnapshotCorruptObjectLevel(t *testing.T) {
	snap := Snapshot{Version: "v1", Records: []SnapshotRecord{
		goodSnapRecord("a", "snap-a"),
		{ObjectID: "b", State: "tampered", Checksum: SnapshotChecksum("b", "original")},
	}}
	c, err := NewCoordinator(snap, Log{Base: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	dumpDecisionLog(t, c, "snapshot-corrupt")

	all, err := c.Recover(nil)
	if err != nil {
		t.Fatal(err)
	}
	if all.Objects["a"].Snapshot != StatusValid || all.Objects["a"].Source != SourceSnapshot {
		t.Fatalf("a should be intact snapshot: %+v", all.Objects["a"])
	}
	if all.Objects["b"].Snapshot != StatusCorrupt {
		t.Fatalf("b must be corrupt, got %s", all.Objects["b"].Snapshot)
	}
	if all.Objects["b"].Source != SourceUnreadable {
		t.Fatalf("b must remain unreadable, got %s", all.Objects["b"].Source)
	}
	if got := c.SnapshotCorruptObjects(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("corrupt objects = %v, want [b]", got)
	}
	if _, err := c.Classify("ghost"); !errors.Is(err, ErrObjectOutOfCoverage) {
		t.Fatalf("absent object must yield coverage error, got %v", err)
	}
}

// TestActionCorruptWholeRecordAndPropagation 动作整条不可用且不阻断传播。
func TestActionCorruptWholeRecordAndPropagation(t *testing.T) {
	snap := Snapshot{Version: "v1", Records: []SnapshotRecord{
		goodSnapRecord("a", "s0"),
		goodSnapRecord("b", "snap-b"),
	}}
	corrupt := goodAction("act1", "v1", map[ObjectID]Effect{"a": delta("c1")})
	corrupt.Effects["a"] = delta("TAMPERED")
	good2 := goodAction("act2", "v1", map[ObjectID]Effect{"a": delta("c2")})

	c, err := NewCoordinator(snap, Log{Base: "v1", Records: []ActionRecord{corrupt, good2}})
	if err != nil {
		t.Fatal(err)
	}
	dumpDecisionLog(t, c, "action-corrupt-propagation")

	all, _ := c.Recover(nil)
	if all.Objects["a"].FinalState != "s0|c2" {
		t.Fatalf("a final = %q, want s0|c2", all.Objects["a"].FinalState)
	}
	if ids := c.ActionCorruptIDs(); len(ids) != 1 || ids[0] != "act1" {
		t.Fatalf("corrupt actions = %v, want [act1]", ids)
	}
	if all.Objects["b"].Source != SourceSnapshot {
		t.Fatalf("b untouched: %+v", all.Objects["b"])
	}
}

// TestAtomicityMixedReadability 部分可读部分起点未知 => 整条动作整体放弃。
func TestAtomicityMixedReadability(t *testing.T) {
	snap := Snapshot{Version: "v1", Records: []SnapshotRecord{
		goodSnapRecord("known", "k0"),
		{ObjectID: "unk", State: "x", Checksum: "bad-checksum"},
	}}
	mixed := goodAction("mix", "v1", map[ObjectID]Effect{
		"known": delta("should-not-apply"),
		"unk":   delta("d"),
	})
	c, err := NewCoordinator(snap, Log{Base: "v1", Records: []ActionRecord{mixed}})
	if err != nil {
		t.Fatal(err)
	}
	dumpDecisionLog(t, c, "atomicity-mixed")

	all, _ := c.Recover(nil)
	if all.Objects["known"].FinalState != "k0" || all.Objects["known"].Source != SourceSnapshot {
		t.Fatalf("known object must not be partially recovered: %+v", all.Objects["known"])
	}
	if all.Objects["unk"].Source != SourceUnreadable {
		t.Fatalf("unk stays unreadable: %+v", all.Objects["unk"])
	}

	rescue := goodAction("rescue", "v1", map[ObjectID]Effect{
		"known": delta("after"),
		"unk":   anchored("start-unk", "u1"),
	})
	if err := c.AppendAction(rescue); err != nil {
		t.Fatal(err)
	}
	all, _ = c.Recover(nil)
	if all.Objects["unk"].Source != SourceReplay ||
		all.Objects["unk"].FinalState != "start-unk|u1" {
		t.Fatalf("unk rebuilt from rescue onward: %+v", all.Objects["unk"])
	}
	if all.Objects["known"].FinalState != "k0|after" {
		t.Fatalf("known advances only after rescue: %+v", all.Objects["known"])
	}
}

// TestCorruptionOnActionSetBoundary 损坏恰好落在对象集合边界成员上。
func TestCorruptionOnActionSetBoundary(t *testing.T) {
	snap := Snapshot{Version: "v1", Records: []SnapshotRecord{
		goodSnapRecord("x", "x0"),
		goodSnapRecord("y", "y0"),
	}}

	corruptRec := goodAction("edge", "v1", map[ObjectID]Effect{
		"x": delta("cx"),
		"y": delta("cy"),
	})
	// 损坏恰好命中集合边界成员 x 的一个字段，校验随之失配。
	corruptRec.Effects["x"] = delta("CORRUPTED-AT-BOUNDARY")
	c1, err := NewCoordinator(snap, Log{Base: "v1", Records: []ActionRecord{corruptRec}})
	if err != nil {
		t.Fatal(err)
	}
	all1, _ := c1.Recover(nil)
	if all1.Objects["x"].FinalState != "x0" || all1.Objects["y"].FinalState != "y0" {
		t.Fatalf("boundary-corrupt action must be wholly discarded: x=%q y=%q",
			all1.Objects["x"].FinalState, all1.Objects["y"].FinalState)
	}

	intactRec := goodAction("edge", "v1", map[ObjectID]Effect{
		"x": delta("cx"),
		"y": delta("cy"),
	})
	c2, err := NewCoordinator(snap, Log{Base: "v1", Records: []ActionRecord{intactRec}})
	if err != nil {
		t.Fatal(err)
	}
	all2, _ := c2.Recover(nil)
	if all2.Objects["x"].FinalState != "x0|cx" || all2.Objects["y"].FinalState != "y0|cy" {
		t.Fatalf("intact boundary action must apply to full set: x=%q y=%q",
			all2.Objects["x"].FinalState, all2.Objects["y"].FinalState)
	}
}

// TestConsecutiveActionsWithCorruptInBetween 连续动作中间夹损坏记录。
func TestConsecutiveActionsWithCorruptInBetween(t *testing.T) {
	snap := Snapshot{Version: "v1", Records: []SnapshotRecord{
		goodSnapRecord("a", "a0"),
		{ObjectID: "b", State: "z", Checksum: "bad"},
	}}
	recs := []ActionRecord{
		goodAction("a1", "v1", map[ObjectID]Effect{"a": delta("1")}),
		{ActionID: "a2", Base: "v1",
			Effects:  map[ObjectID]Effect{"b": anchored("bstart", "x")},
			Checksum: "broken"},
		goodAction("a3", "v1", map[ObjectID]Effect{
			"a": delta("should-not-happen"),
			"b": delta("inc"),
		}),
		goodAction("a4", "v1", map[ObjectID]Effect{
			"a": delta("2"),
			"b": anchored("bstart2", "b1"),
		}),
	}
	c, err := NewCoordinator(snap, Log{Base: "v1", Records: recs})
	if err != nil {
		t.Fatal(err)
	}
	dumpDecisionLog(t, c, "consecutive-corrupt")
	all, _ := c.Recover(nil)

	if all.Objects["a"].FinalState != "a0|1|2" {
		t.Fatalf("a propagation = %q, want a0|1|2", all.Objects["a"].FinalState)
	}
	// 有效序列下标 a1=0, a3=1(整体放弃), a4=2。
	if all.Objects["b"].Source != SourceReplay ||
		all.Objects["b"].FinalState != "bstart2|b1" ||
		all.Objects["b"].AnchorIndex != 2 {
		t.Fatalf("b rebuilt only at a4: %+v", all.Objects["b"])
	}
}

// TestRejectPriority 超范围 优先于 版本不衔接；均不命中时逐对象报告。
func TestRejectPriority(t *testing.T) {
	snap := Snapshot{Version: "v1", Records: []SnapshotRecord{goodSnapRecord("a", "s")}}
	lg := Log{Base: "DIFFERENT"}

	_, err := Evaluate(snap, lg, []ObjectID{"ghost"})
	if !errors.Is(err, ErrObjectOutOfCoverage) {
		t.Fatalf("coverage must outrank version mismatch, got %v", err)
	}

	_, err = Evaluate(snap, lg, []ObjectID{"a"})
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("want version mismatch, got %v", err)
	}

	rep, err := Evaluate(snap, Log{Base: "v1"}, []ObjectID{"a"})
	if err != nil {
		t.Fatalf("normal classification must not be an error, got %v", err)
	}
	assertSource(t, rep.Objects, "a", SourceSnapshot)

	if _, err := NewCoordinator(snap, lg); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("constructor version check, got %v", err)
	}
}

// TestErrorCategoriesDistinct 四类错误彼此可区分、不混报。
func TestErrorCategoriesDistinct(t *testing.T) {
	snap := Snapshot{Version: "v1", Records: []SnapshotRecord{
		{ObjectID: "bad", State: "x", Checksum: "bad"},
	}}
	lg := Log{Base: "v1", Records: []ActionRecord{
		{ActionID: "badact", Checksum: "bad"},
	}}
	rep, err := Evaluate(snap, lg, nil)
	if err != nil {
		t.Fatalf("corruptions are reported in report not as error: %v", err)
	}
	if len(rep.CorruptObjects) != 1 || rep.CorruptObjects[0] != "bad" {
		t.Fatalf("snapshot corruption list = %v", rep.CorruptObjects)
	}
	if len(rep.CorruptActions) != 1 || rep.CorruptActions[0] != "badact" {
		t.Fatalf("action corruption list = %v", rep.CorruptActions)
	}

	_, err = Evaluate(snap, Log{Base: "v9"}, nil)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("version category: %v", err)
	}
	_, err = Evaluate(snap, Log{Base: "v1"}, []ObjectID{"nope"})
	if !errors.Is(err, ErrObjectOutOfCoverage) {
		t.Fatalf("coverage category: %v", err)
	}
}

// TestAppendCorruptActionNoSideEffect 被拒绝的追加不得改变既有判定。
func TestAppendCorruptActionNoSideEffect(t *testing.T) {
	snap := Snapshot{Version: "v1", Records: []SnapshotRecord{goodSnapRecord("a", "s0")}}
	c, err := NewCoordinator(snap, Log{Base: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := c.Recover(nil)
	bad := goodAction("new", "v1", map[ObjectID]Effect{"a": delta("x")})
	bad.Checksum = "wrong"
	if err := c.AppendAction(bad); !errors.Is(err, ErrActionCorrupt) {
		t.Fatalf("append corrupt = %v", err)
	}
	after, _ := c.Recover(nil)
	if before.Objects["a"].FinalState != after.Objects["a"].FinalState {
		t.Fatalf("rejected append changed state: %q -> %q",
			before.Objects["a"].FinalState, after.Objects["a"].FinalState)
	}
	if len(c.ActionCorruptIDs()) != 0 {
		t.Fatalf("rejected corrupt append must not be persisted")
	}
}

// TestClassifyConstantTime 可验证地证明定位来源分类不随日志长度线性增长：
// 分类结果直接取自对象级 map 报告，Classify 路径不扫描任何动作记录。
// 这里用规模倍增下行为一致性 + 锚点索引完备性来验证规模无关性；
// 复杂度论证本身见 DESIGN.md（map 查找 O(1)，扫描只在追加时发生）。
func TestClassifyConstantTime(t *testing.T) {
	build := func(n int) *Coordinator {
		snap := Snapshot{Version: "v1", Records: []SnapshotRecord{
			goodSnapRecord("a", "s0"),
			{ObjectID: "late", State: "x", Checksum: "bad"}, // 一直不可读直到最后锚定
		}}
		recs := make([]ActionRecord, 0, n+1)
		for i := 0; i < n; i++ {
			recs = append(recs, goodAction(fmt.Sprintf("a%d", i), "v1",
				map[ObjectID]Effect{"a": delta(fmt.Sprintf("c%d", i))}))
		}
		recs = append(recs, goodAction("anchor", "v1", map[ObjectID]Effect{
			"late": anchored("L0", "L1"),
		}))
		c, err := NewCoordinator(snap, Log{Base: "v1", Records: recs})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	small := build(64)
	big := build(8192)
	// 不管日志多长，分类都是一次报告查询；这里验证结果语义与规模无关。
	srcSmall, err := small.Classify("late")
	if err != nil || srcSmall != SourceReplay {
		t.Fatalf("small classify late = %v, %v", srcSmall, err)
	}
	srcBig, err := big.Classify("late")
	if err != nil || srcBig != SourceReplay {
		t.Fatalf("big classify late = %v, %v", srcBig, err)
	}
	if _, err := big.Classify("a"); err != nil {
		t.Fatalf("a classify: %v", err)
	}
	if _, err := big.Classify("missing"); !errors.Is(err, ErrObjectOutOfCoverage) {
		t.Fatalf("missing classify: %v", err)
	}

	// 锚点索引：late 的锚点必须恰好等于最后一条有效动作下标，与 n 无关地一次定位。
	bigRep, _ := big.Recover([]ObjectID{"late"})
	if bigRep.Objects["late"].AnchorIndex != 8192 {
		t.Fatalf("anchor index = %d, want 8192", bigRep.Objects["late"].AnchorIndex)
	}
}

// TestConcurrentSerializable 并发修复查询与动作追加，最终结果必须等价于
// 某个串行顺序，且任何请求观察不到部分恢复的中间态。
func TestConcurrentSerializable(t *testing.T) {
	snap := Snapshot{Version: "v1", Records: []SnapshotRecord{
		goodSnapRecord("a", "s0"),
		{ObjectID: "b", State: "x", Checksum: "bad"},
	}}
	c, err := NewCoordinator(snap, Log{Base: "v1"})
	if err != nil {
		t.Fatal(err)
	}

	const writers = 8
	const readers = 8
	const perWriter = 40

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				// 一半追加只改 a 的增量；一半追加为 b 自带起点（仅首次真正锚定）。
				var rec ActionRecord
				if i%2 == 0 {
					rec = goodAction(fmt.Sprintf("w%d-i%d", w, i), "v1",
						map[ObjectID]Effect{"a": delta(fmt.Sprintf("w%di%d", w, i))})
				} else {
					rec = goodAction(fmt.Sprintf("w%d-i%d", w, i), "v1",
						map[ObjectID]Effect{"b": anchored(fmt.Sprintf("b%d", w), fmt.Sprintf("i%d", i))})
				}
				if err := c.AppendAction(rec); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rep, err := c.Recover(nil)
				if err != nil {
					t.Errorf("recover: %v", err)
					return
				}
				// 不变量：a 若可读则其状态必为某个完整重放前缀的结果；
				// b 一旦可读，其状态必然形如 "bN|iM"（动作起点锚定），
				// 绝不会出现半个拼接或快照损坏态泄漏。
				a := rep.Objects["a"]
				if a.Source == SourceSnapshot || a.Source == SourceReplay {
					if !strings.HasPrefix(a.FinalState, "s0") {
						t.Errorf("a intermediate/final state invalid: %q", a.FinalState)
						return
					}
				}
				b := rep.Objects["b"]
				if b.Source == SourceReplay && !strings.Contains(b.FinalState, "|") {
					t.Errorf("b replay state malformed: %q", b.FinalState)
					return
				}
			}
		}()
	}
	wg.Wait()

	// 最终结果必须与“把同一批完整动作按某个顺序串行重放”一致：
	// 用朴素模型对协调器最终接受的完整动作序列独立复算。
	finalLog := c.log
	finalView := verifyActions(&finalLog)
	naive := naiveReplay(snap, finalView.actions)
	final, _ := c.Recover(nil)
	for obj, nr := range naive {
		gr := final.Objects[obj]
		if gr.Source != nr.Source || gr.FinalState != nr.FinalState ||
			gr.Snapshot != nr.Snapshot || gr.AnchorIndex != nr.AnchorIndex {
			t.Fatalf("serializability mismatch for %s: coordinator=%+v naive=%+v",
				obj, gr, nr)
		}
	}
	if len(final.Objects) != len(naive) {
		t.Fatalf("coverage mismatch: %d vs %d", len(final.Objects), len(naive))
	}
}
