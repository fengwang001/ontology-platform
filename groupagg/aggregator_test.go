package groupagg

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// ---- 辅助函数 ----

func mustApply(t *testing.T, a *Aggregator, ops ...Op) *BatchResult {
	t.Helper()
	res, err := a.Apply(ops)
	if err != nil {
		t.Fatalf("Apply(%v) unexpected error: %v", ops, err)
	}
	return res
}

func expectReject(t *testing.T, a *Aggregator, ops []Op, want RejectReason, wantIdx int) {
	t.Helper()
	_, err := a.Apply(ops)
	if err == nil {
		t.Fatalf("Apply(%v) expected rejection, got success", ops)
	}
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("error is not *RejectError: %T", err)
	}
	if re.Reason != want {
		t.Fatalf("reason = %s, want %s (op[%d])", re.Reason, want, wantIdx)
	}
	if re.OpIndex != wantIdx {
		t.Fatalf("opIndex = %d, want %d", re.OpIndex, wantIdx)
	}
}

// assertInvariants 在锁内直接核对：groups 与按行表批量重算的结果完全一致，
// 且视图中不存在计数为 0 的组。
func assertInvariants(t *testing.T, a *Aggregator) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	for g, agg := range a.groups {
		if agg.Count <= 0 {
			t.Fatalf("group %q in view with non-positive count %d", g, agg.Count)
		}
	}
	want := recomputeLocked(a.rows)
	if len(want) != len(a.groups) {
		t.Fatalf("group count mismatch: incremental %d, recompute %d", len(a.groups), len(want))
	}
	for g, w := range want {
		got, ok := a.groups[g]
		if !ok {
			t.Fatalf("group %q missing incrementally (recompute has %+v)", g, w)
		}
		if got != w {
			t.Fatalf("group %q incremental %+v != recompute %+v", g, got, w)
		}
	}
}

func recomputeLocked(rows map[string]Row) map[string]GroupAgg {
	out := map[string]GroupAgg{}
	for _, r := range rows {
		agg := out[r.GroupKey]
		agg.Sum += r.Value
		agg.Count++
		out[r.GroupKey] = agg
	}
	return out
}

func recomputeView(rows map[string]Row) []GroupView {
	m := map[string]GroupAgg{}
	for _, r := range rows {
		agg := m[r.GroupKey]
		agg.Sum += r.Value
		agg.Count++
		m[r.GroupKey] = agg
	}
	out := make([]GroupView, 0, len(m))
	for g, agg := range m {
		out = append(out, GroupView{Group: g, Sum: agg.Sum, Count: agg.Count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

// replay 按日志顺序应用条目，重建下游视图。
// 语义：RETRACT 移除该分组旧值；PUT 在 count>0 时设置、否则（墓碑）移除。
func replay(entries []ChangeEntry) map[string]GroupAgg {
	view := map[string]GroupAgg{}
	for _, e := range entries {
		switch e.Kind {
		case ChangeRetract:
			delete(view, e.Group)
		case ChangePut:
			if e.Count > 0 {
				view[e.Group] = GroupAgg{Sum: e.Sum, Count: e.Count}
			} else {
				delete(view, e.Group)
			}
		}
	}
	return view
}

func viewsEqual(x, y map[string]GroupAgg) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}

func ins(id, g string, v int64) Op { return Op{Kind: OpInsert, ID: id, GroupKey: g, Value: v} }
func upd(id, g string, v int64) Op { return Op{Kind: OpUpdate, ID: id, GroupKey: g, Value: v} }
func del(id string) Op             { return Op{Kind: OpDelete, ID: id} }

// ---- 基础聚合 ----

func TestInsertAndAggregate(t *testing.T) {
	a := New(0, nil)
	mustApply(t, a, ins("r1", "a", 10), ins("r2", "a", 5), ins("r3", "b", 7))
	assertInvariants(t, a)
	got := a.Snapshot()
	want := map[string]GroupAgg{"a": {Sum: 15, Count: 2}, "b": {Sum: 7, Count: 1}}
	if !viewsEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
}

// 求和为零但计数为正的组必须保留。
func TestZeroSumPositiveCountKept(t *testing.T) {
	a := New(0, nil)
	mustApply(t, a, ins("r1", "a", 5), ins("r2", "a", -5))
	assertInvariants(t, a)
	view := a.View()
	if len(view) != 1 || view[0].Group != "a" || view[0].Sum != 0 || view[0].Count != 2 {
		t.Fatalf("zero-sum group must be retained, got %+v", view)
	}
	// 删掉一行后 sum=-5，组仍在；再删最后一行后组消失。
	mustApply(t, a, del("r1"))
	if v := a.View(); len(v) != 1 || v[0].Sum != -5 || v[0].Count != 1 {
		t.Fatalf("after one delete, got %+v", v)
	}
	mustApply(t, a, del("r2"))
	if len(a.View()) != 0 {
		t.Fatalf("empty group must not appear in view, got %+v", a.View())
	}
}

// 同组更新只产生该组的净变化：先撤回旧聚合、再写入新聚合，计数不变。
func TestSameGroupUpdateNetChange(t *testing.T) {
	a := New(0, nil)
	mustApply(t, a, ins("r1", "a", 10), ins("r2", "a", 20))
	res := mustApply(t, a, upd("r1", "a", 25))
	assertInvariants(t, a)

	if len(res.Entries) != 2 {
		t.Fatalf("same-group update should emit retract+put, got %d entries", len(res.Entries))
	}
	r, p := res.Entries[0], res.Entries[1]
	if r.Kind != ChangeRetract || r.Sum != 30 || r.Count != 2 {
		t.Fatalf("retract = %+v, want retract{sum=30,count=2}", r)
	}
	if p.Kind != ChangePut || p.Sum != 45 || p.Count != 2 {
		t.Fatalf("put = %+v, want put{sum=45,count=2}", p)
	}
	if got := a.Snapshot()["a"]; got != (GroupAgg{Sum: 45, Count: 2}) {
		t.Fatalf("group a = %+v", got)
	}
}

// 改分组键：先输出旧组（撤回/写入）再输出新组（撤回/写入）；
// 旧组仅剩该行时从视图消失，新组聚合被加入。
func TestRekeyRetractThenJoin(t *testing.T) {
	a := New(0, nil)
	mustApply(t, a, ins("r1", "a", 10), ins("r2", "a", 5), ins("r3", "b", 7))
	res := mustApply(t, a, upd("r1", "b", 10))
	assertInvariants(t, a)

	wantSeq := []ChangeEntry{
		{RowID: "r1", Kind: ChangeRetract, Group: "a", Sum: 15, Count: 2},
		{RowID: "r1", Kind: ChangePut, Group: "a", Sum: 5, Count: 1},
		{RowID: "r1", Kind: ChangeRetract, Group: "b", Sum: 7, Count: 1},
		{RowID: "r1", Kind: ChangePut, Group: "b", Sum: 17, Count: 2},
	}
	if len(res.Entries) != len(wantSeq) {
		t.Fatalf("entries = %d, want %d: %+v", len(res.Entries), len(wantSeq), res.Entries)
	}
	for i, w := range wantSeq {
		got := res.Entries[i]
		if got.RowID != w.RowID || got.Kind != w.Kind || got.Group != w.Group ||
			got.Sum != w.Sum || got.Count != w.Count {
			t.Fatalf("entry[%d] = %+v, want %+v", i, got, w)
		}
	}
	if got := a.Snapshot(); !viewsEqual(got, map[string]GroupAgg{
		"a": {Sum: 5, Count: 1},
		"b": {Sum: 17, Count: 2},
	}) {
		t.Fatalf("snapshot after rekey = %v", got)
	}

	// 旧组仅剩该行：移动后旧组消失，Put(0,0) 为墓碑。
	res2 := mustApply(t, a, upd("r2", "b", 5))
	tomb := res2.Entries[1]
	if tomb.Group != "a" || tomb.Kind != ChangePut || tomb.Sum != 0 || tomb.Count != 0 {
		t.Fatalf("expect tombstone Put a{0,0}, got %+v", tomb)
	}
	if res2.Entries[0].Group != "a" || res2.Entries[2].Group != "b" {
		t.Fatalf("old group must be emitted before new group: %+v", res2.Entries)
	}
	assertInvariants(t, a)
	if _, ok := a.Snapshot()["a"]; ok {
		t.Fatalf("group a must disappear after last row moved out")
	}
}

// 删除最后一行：组从视图消失，日志以 Put(0,0) 收尾。
func TestDeleteLastRow(t *testing.T) {
	a := New(0, nil)
	mustApply(t, a, ins("r1", "a", 10))
	res := mustApply(t, a, del("r1"))
	assertInvariants(t, a)
	if len(a.View()) != 0 || len(a.Rows()) != 0 {
		t.Fatalf("expected empty view and rows, got %+v / %v", a.View(), a.Rows())
	}
	last := res.Entries[len(res.Entries)-1]
	if last.Kind != ChangePut || last.Count != 0 || last.Sum != 0 {
		t.Fatalf("delete-last entry should be Put{0,0}, got %+v", last)
	}
}

// ---- 非法输入：每种原因可区分，且整批无副作用 ----

func TestRejections(t *testing.T) {
	t.Run("duplicate insert", func(t *testing.T) {
		a := New(0, nil)
		mustApply(t, a, ins("r1", "a", 1))
		before := len(a.Changelog())
		expectReject(t, a, []Op{ins("r1", "b", 2)}, ReasonDuplicateInsert, 0)
		expectReject(t, a, []Op{ins("x", "a", 1), ins("x", "b", 2)}, ReasonDuplicateInsert, 1)
		assertUnchanged(t, a, before, 1)
	})
	t.Run("update missing", func(t *testing.T) {
		a := New(0, nil)
		before := len(a.Changelog())
		expectReject(t, a, []Op{upd("nope", "a", 1)}, ReasonUpdateMissing, 0)
		assertUnchanged(t, a, before, 0)
	})
	t.Run("delete missing", func(t *testing.T) {
		a := New(0, nil)
		mustApply(t, a, ins("r1", "a", 1))
		before := len(a.Changelog())
		expectReject(t, a, []Op{del("r1"), del("r1")}, ReasonDeleteMissing, 1)
		expectReject(t, a, []Op{del("ghost")}, ReasonDeleteMissing, 0)
		assertUnchanged(t, a, before, 1)
	})
	t.Run("empty group key", func(t *testing.T) {
		a := New(0, nil)
		mustApply(t, a, ins("r1", "a", 1))
		before := len(a.Changelog())
		expectReject(t, a, []Op{ins("r2", "", 1)}, ReasonEmptyGroupKey, 0)
		expectReject(t, a, []Op{upd("r1", "", 1)}, ReasonEmptyGroupKey, 0)
		// 删除操作忽略分组键，空分组键合法。
		mustApply(t, a, Op{Kind: OpDelete, ID: "r1"})
		assertInvariants(t, a)
		_ = before
	})
	t.Run("empty id", func(t *testing.T) {
		a := New(0, nil)
		expectReject(t, a, []Op{{Kind: OpInsert, GroupKey: "a"}}, ReasonEmptyID, 0)
		expectReject(t, a, []Op{del("")}, ReasonEmptyID, 0)
	})
	t.Run("unknown op", func(t *testing.T) {
		a := New(0, nil)
		expectReject(t, a, []Op{{Kind: OpKind(99), ID: "r1", GroupKey: "a"}}, ReasonUnknownOp, 0)
	})
	t.Run("too many groups", func(t *testing.T) {
		a := New(2, nil)
		mustApply(t, a, ins("r1", "a", 1), ins("r2", "b", 1))
		before := len(a.Changelog())
		expectReject(t, a, []Op{ins("r3", "c", 1)}, ReasonTooManyGroups, 0)
		// 改组到第 3 个新组、且旧组不会消失：超限拒绝。
		expectReject(t, a, []Op{ins("r4", "a", 1), upd("r4", "c", 1)}, ReasonTooManyGroups, 1)
		assertUnchanged(t, a, before, 2)
		// 改键是“一减一加”，组数不增：上限为 1 时 a -> b 仍合法。
		a1 := New(1, nil)
		mustApply(t, a1, ins("s1", "a", 1))
		mustApply(t, a1, upd("s1", "b", 1))
		if v := a1.View(); len(v) != 1 || v[0].Group != "b" {
			t.Fatalf("rekey with zero net group change should be allowed, got %+v", v)
		}
	})
}

// assertUnchanged 核对被拒绝批次没有改动行表、聚合与日志长度。
func assertUnchanged(t *testing.T, a *Aggregator, logLen, rowCount int) {
	t.Helper()
	if len(a.Changelog()) != logLen {
		t.Fatalf("changelog changed after rejection: %d != %d", len(a.Changelog()), logLen)
	}
	if len(a.Rows()) != rowCount {
		t.Fatalf("rows changed after rejection: %d != %d", len(a.Rows()), rowCount)
	}
	assertInvariants(t, a)
}

// 混合批次中只要有一条非法，前面合法的操作也不得生效（原子性）。
func TestBatchAtomicity(t *testing.T) {
	a := New(0, nil)
	mustApply(t, a, ins("r1", "a", 1))
	before := a.Changelog()
	view := a.View()

	expectReject(t, a,
		[]Op{ins("r2", "b", 2), ins("r3", "c", 3), upd("r99", "x", 9)},
		ReasonUpdateMissing, 2)

	if len(a.Changelog()) != len(before) {
		t.Fatal("changelog must not grow on rejected batch")
	}
	got := a.View()
	if fmt.Sprint(got) != fmt.Sprint(view) {
		t.Fatalf("view changed after rejected batch: %v != %v", got, view)
	}
	if _, ok := a.Rows()["r2"]; ok {
		t.Fatal("row from rejected batch must not exist")
	}
}

// ---- 日志回放与批量重算一致 ----

func TestChangelogReplayMatchesRecompute(t *testing.T) {
	var log bytes.Buffer
	a := New(3, &log)
	seq := []Op{
		ins("r1", "a", 10),
		ins("r2", "b", -4),
		ins("r3", "a", -10), // a: sum=0 count=2，零和保留
		upd("r2", "a", -4),  // b 消失；a: sum=-4 count=3
		ins("r4", "c", 100),
		upd("r1", "c", 10), // a: sum=-14 count=2；c: sum=110 count=2
		del("r3"),          // a: sum=-4 count=1
		upd("r4", "c", 1),  // 同组净变化 c: 11
	}
	mustApply(t, a, seq...)

	entries := a.Changelog()
	// Seq 从 1 开始严格递增（每个操作受影响组各产出 retract+put）。
	for i, e := range entries {
		if e.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d: %+v", i, e)
		}
	}
	replayed := replay(entries)
	snap := a.Snapshot()
	if !viewsEqual(replayed, snap) {
		t.Fatalf("replay %v != snapshot %v", replayed, snap)
	}
	br := recomputeView(a.Rows())
	gotView := a.View()
	if fmt.Sprint(br) != fmt.Sprint(gotView) {
		t.Fatalf("recompute %v != incremental view %v", br, gotView)
	}
	if !viewsEqual(replayed, map[string]GroupAgg{
		"a": {Sum: -4, Count: 1},
		"c": {Sum: 11, Count: 2},
	}) {
		t.Fatalf("unexpected final replay: %v", replayed)
	}
	t.Logf("diagnostic log for accepted sequence:\n%s", log.String())
}

// 同一输入序列反复计算，输出（日志、视图）完全相同。
func TestDeterministicAcrossRuns(t *testing.T) {
	run := func() ([]ChangeEntry, []GroupView) {
		a := New(0, nil)
		ops := []Op{
			ins("r1", "a", 1), ins("r2", "a", 2), ins("r3", "b", 3),
			upd("r1", "b", 1), del("r2"), ins("r4", "a", -2),
		}
		mustApply(t, a, ops...)
		return a.Changelog(), a.View()
	}
	e1, v1 := run()
	e2, v2 := run()
	if fmt.Sprint(e1) != fmt.Sprint(e2) {
		t.Fatalf("changelog not deterministic:\n%v\n%v", e1, e2)
	}
	if fmt.Sprint(v1) != fmt.Sprint(v2) {
		t.Fatalf("view not deterministic:\n%v\n%v", v1, v2)
	}
}

// 拒绝日志也打印输入与原因；验证日志内容包含输入、输出条目与判定依据。
func TestDiagnosticLogContents(t *testing.T) {
	var buf bytes.Buffer
	a := New(0, &buf)
	mustApply(t, a, ins("r1", "a", 10))
	mustApply(t, a, upd("r1", "b", 10))
	_, _ = a.Apply([]Op{ins("r1", "c", 1)}) // 重复插入，拒绝
	out := buf.String()
	for _, want := range []string{"[ACCEPT]", "INSERT", "UPDATE", "basis:", "RETRACT", "PUT", "[REJECT]", "DUPLICATE_INSERT"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("diagnostic log missing %q\n--- log ---\n%s", want, out)
		}
	}
	t.Logf("diagnostic log:\n%s", out)
}

// ---- 并发读 ----

func TestConcurrentReaders(t *testing.T) {
	a := New(0, nil)
	mustApply(t, a, ins("seed", "g0", 1))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, v := range a.View() {
					if v.Count <= 0 {
						t.Errorf("zero-count group observed: %+v", v)
						return
					}
				}
				_ = a.Snapshot()
				_ = a.Changelog()
				_ = a.Rows()
			}
		}()
	}

	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("r%d", i)
		g := fmt.Sprintf("g%d", i%5)
		mustApply(t, a, ins(id, g, int64(i%7-3)))
		if i%3 == 0 {
			mustApply(t, a, upd(id, fmt.Sprintf("g%d", (i+2)%5), int64(i%5-2)))
		}
		if i%4 == 0 {
			mustApply(t, a, del(id))
		}
	}
	close(stop)
	wg.Wait()

	// 最终：增量视图、日志回放、批量重算三者一致。
	if !viewsEqual(replay(a.Changelog()), a.Snapshot()) {
		t.Fatal("replay != snapshot after concurrent run")
	}
	if fmt.Sprint(recomputeView(a.Rows())) != fmt.Sprint(a.View()) {
		t.Fatal("recompute != view after concurrent run")
	}
}
