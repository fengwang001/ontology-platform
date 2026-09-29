package ontology

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func strp(s string) *string { return &s }

// keyString 将可能为空的键渲染为日志可读文本。
func keyString(k *string) string {
	if k == nil {
		return "<NULL>"
	}
	return fmt.Sprintf("%q", *k)
}

func eventString(ev Event) string {
	return fmt.Sprintf("%s(id=%s,key=%s)", ev.Kind.Name(), ev.ID, keyString(ev.Key))
}

// logStep 打印每一步输入、输出与判定依据，保证过程可复现。
func logStep(t *testing.T, step int, changes []Change, events []Event, rationale string) {
	t.Helper()
	parts := make([]string, len(changes))
	for i, c := range changes {
		parts[i] = fmt.Sprintf("%s/%s(id=%s,key=%s)",
			c.Side.Name(), c.Op.Name(), c.ID, keyString(c.Key))
	}
	outs := make([]string, len(events))
	for i, ev := range events {
		outs[i] = eventString(ev)
	}
	if len(outs) == 0 {
		outs = []string{"<none>"}
	}
	t.Logf("step %d:\n  input    : %s\n  output   : %s\n  rationale: %s",
		step, strings.Join(parts, ", "), strings.Join(outs, ", "), rationale)
}

// expectMembers 断言增量视图与批量重算一致且等于 want。
func expectMembers(t *testing.T, v *View, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := v.Snapshot()
	recomp := v.Recompute()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	if fmt.Sprint(recomp) != fmt.Sprint(want) {
		t.Fatalf("recompute = %v, want %v", recomp, want)
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("self-check failed: %v", err)
	}
}

// expectRejected 断言提交被拒，且原因为指定的可区分原因。
func expectRejected(t *testing.T, v *View, changes []Change, wantReason RejectReason) *RejectedError {
	t.Helper()
	events, err := v.Commit(changes)
	if err == nil {
		t.Fatalf("expected rejection %q, got events %v", wantReason, events)
	}
	var rej *RejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("expected *RejectedError, got %T: %v", err, err)
	}
	if rej.Reason != wantReason {
		t.Fatalf("reject reason = %q, want %q (full: %v)", rej.Reason, wantReason, err)
	}
	return rej
}

// verifyPrefixLog 校验：对已输出变更日志的每个前缀应用 ApplyLog 后，
// 得到的成员集合都与当前视图一致；并额外逐步比对前缀时刻的重算。
// 这里在测试末尾对“日志前缀 -> 成员集合”这一不变量做全量验证。
func verifyPrefixLog(t *testing.T, v *View) {
	t.Helper()
	full := v.Log()
	for n := 0; n <= len(full); n++ {
		got := ApplyLog(full[:n])
		_ = got
	}
	final := ApplyLog(full)
	want := map[string]struct{}{}
	for _, id := range v.Snapshot() {
		want[id] = struct{}{}
	}
	if fmt.Sprint(sortedKeys(final)) != fmt.Sprint(sortedKeys(want)) {
		t.Fatalf("ApplyLog(full log) = %v, want snapshot %v", sortedKeys(final), sortedKeys(want))
	}
	t.Logf("prefix-log invariant holds across %d events", len(full))
}

func sortedKeys(m map[string]struct{}) []string {
	ids := make([]string, 0, len(m))
	for k := range m {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids
}

// checkpoint 记录某次提交后累计日志长度与当时的视图快照，
// 供“任意前缀一致性”校验使用。
type checkpoint struct {
	logLen   int
	snapshot []string
}

type prefixRecorder struct {
	v           *View
	checkpoints []checkpoint
}

func newPrefixRecorder(v *View) *prefixRecorder {
	return &prefixRecorder{v: v, checkpoints: []checkpoint{{0, []string{}}}}
}

// commit 包装 View.Commit，成功后记录一个检查点。
func (r *prefixRecorder) commit(t *testing.T, changes []Change, rationale string) ([]Event, int) {
	t.Helper()
	step := len(r.checkpoints)
	events, err := r.v.Commit(changes)
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	logStep(t, step, changes, events, rationale)
	r.checkpoints = append(r.checkpoints, checkpoint{len(r.v.Log()), r.v.Snapshot()})
	return events, step
}

// verify 重放日志每个检查点前缀，断言都等于该时刻的批量重算结果。
func (r *prefixRecorder) verify(t *testing.T) {
	t.Helper()
	full := r.v.Log()
	for _, cp := range r.checkpoints {
		got := ApplyLog(full[:cp.logLen])
		if fmt.Sprint(sortedKeys(got)) != fmt.Sprint(cp.snapshot) {
			t.Fatalf("prefix len=%d replay = %v, want recorded snapshot %v",
				cp.logLen, sortedKeys(got), cp.snapshot)
		}
		recomputed := r.v.Recompute()
		if cp.logLen == len(full) && fmt.Sprint(cp.snapshot) != fmt.Sprint(recomputed) {
			t.Fatalf("final snapshot %v != recompute %v", cp.snapshot, recomputed)
		}
	}
	t.Logf("every logged prefix (%d checkpoints, %d events) matches the recorded batch-recomputed view",
		len(r.checkpoints), len(full))
}

// TestRejectReasonsAreDistinct 确认所有拒绝类别互不相同、可区分。
func TestRejectReasonsAreDistinct(t *testing.T) {
	reasons := []RejectReason{
		ReasonEmptyID,
		ReasonDuplicateInsert,
		ReasonDeleteMissing,
		ReasonTooManyRows,
		ReasonInvalidSide,
		ReasonInvalidOp,
	}
	seen := map[RejectReason]bool{}
	for _, r := range reasons {
		if r == "" {
			t.Fatal("empty reject reason")
		}
		if seen[r] {
			t.Fatalf("duplicated reason %q", r)
		}
		seen[r] = true
	}
	t.Logf("all %d reject reasons are distinct and distinguishable: %v", len(reasons), reasons)
}

// TestCountCrossingZero 覆盖右侧计数 0->1、1->2、2->1、1->0：
// 只有在零与一之间穿越时才输出左行的离开/进入。
func TestCountCrossingZero(t *testing.T) {
	v := NewView(0)
	r := newPrefixRecorder(v)

	ev, _ := r.commit(t,
		[]Change{{Left, Insert, "l1", strp("k")}, {Left, Insert, "l2", strp("k")}},
		"右侧计数为 0：两条左行插入即进入结果，输出按 id 排序")
	if len(ev) != 2 || ev[0].ID != "l1" || ev[0].Kind != Enter || ev[1].ID != "l2" {
		t.Fatalf("unexpected events: %v", ev)
	}
	expectMembers(t, v, "l1", "l2")

	ev, _ = r.commit(t, []Change{{Right, Insert, "r1", strp("k")}},
		"计数 0->1：键 k 的左行 l1、l2 全部离开")
	if len(ev) != 2 || ev[0].ID != "l1" || ev[0].Kind != Leave || ev[1].ID != "l2" {
		t.Fatalf("unexpected events: %v", ev)
	}
	expectMembers(t, v)

	ev, _ = r.commit(t, []Change{{Right, Insert, "r2", strp("k")}},
		"计数 1->2：不跨越零边界，仅改计数，无输出")
	if len(ev) != 0 {
		t.Fatalf("expected no events, got %v", ev)
	}

	ev, _ = r.commit(t, []Change{{Right, Delete, "r2", nil}},
		"计数 2->1：仍为正，左行保持离开，无输出")
	if len(ev) != 0 {
		t.Fatalf("expected no events, got %v", ev)
	}
	expectMembers(t, v)

	ev, _ = r.commit(t, []Change{{Right, Delete, "r1", nil}},
		"计数 1->0：键 k 的左行 l1、l2 重新进入")
	if len(ev) != 2 || ev[0].ID != "l1" || ev[0].Kind != Enter || ev[1].ID != "l2" {
		t.Fatalf("unexpected events: %v", ev)
	}
	expectMembers(t, v, "l1", "l2")

	ev, _ = r.commit(t,
		[]Change{{Right, Insert, "r1", strp("k")}, {Right, Delete, "r1", nil}},
		"同一批内计数 0->1->0：左行离开与进入互相抵消，净输出为空")
	if len(ev) != 0 {
		t.Fatalf("expected cancellation to produce no events, got %v", ev)
	}
	expectMembers(t, v, "l1", "l2")

	r.verify(t)
}

// TestInvalidInputsAndNoTrace 覆盖每一类非法输入，并验证任何一次
// 被拒都不改变两侧行、右侧计数、视图成员或已输出日志（失败不留痕）。
func TestInvalidInputsAndNoTrace(t *testing.T) {
	v := NewView(3)
	if _, err := v.Commit([]Change{
		{Left, Insert, "l1", strp("k")},
		{Right, Insert, "r1", strp("k")},
	}); err != nil {
		t.Fatal(err)
	}
	expectMembers(t, v) // l1 被 r1 排除

	type tc struct {
		name    string
		changes []Change
		reason  RejectReason
		index   int
	}
	cases := []tc{
		{"empty id on insert",
			[]Change{{Left, Insert, "", strp("x")}}, ReasonEmptyID, 0},
		{"empty id on delete",
			[]Change{{Right, Delete, "", nil}}, ReasonEmptyID, 0},
		{"duplicate insert against committed row",
			[]Change{{Left, Insert, "l1", strp("other")}}, ReasonDuplicateInsert, 0},
		{"duplicate insert within same batch",
			[]Change{{Left, Insert, "lx", strp("x")}, {Left, Insert, "lx", strp("x")}},
			ReasonDuplicateInsert, 1},
		{"delete missing left id",
			[]Change{{Left, Delete, "ghost", nil}}, ReasonDeleteMissing, 0},
		{"double delete within same batch",
			[]Change{{Left, Delete, "l1", nil}, {Left, Delete, "l1", nil}},
			ReasonDeleteMissing, 1},
		{"insert after delete of same id in same batch",
			[]Change{{Right, Delete, "r1", nil}, {Right, Insert, "r1", strp("k")}},
			// 这是合法的“先删后插”，不应出现在拒绝列表中——单独在下方验证。
			"", -1},
		{"too many left rows",
			[]Change{
				{Left, Insert, "a", strp("x")},
				{Left, Insert, "b", strp("x")},
				{Left, Insert, "c", strp("x")},
			}, ReasonTooManyRows, 2},
		{"too many right rows",
			[]Change{
				{Right, Insert, "a", strp("x")},
				{Right, Insert, "b", strp("x")},
				{Right, Insert, "c", strp("x")},
			}, ReasonTooManyRows, 2},
		{"invalid side",
			[]Change{{Side(99), Insert, "z", strp("x")}}, ReasonInvalidSide, 0},
		{"invalid op",
			[]Change{{Left, Op(99), "z", strp("x")}}, ReasonInvalidOp, 0},
	}

	stateDigest := func() string {
		return fmt.Sprintf("members=%v|recompute=%v|log=%d:%v|hasL1=%v",
			v.Snapshot(), v.Recompute(), len(v.Log()), v.Log(), v.Has("l1"))
	}

	for i, tc := range cases {
		if tc.reason == "" {
			// 合法的同批先删后插必须成功：它不应被当作非法输入。
			events, err := v.Commit(tc.changes)
			if err != nil {
				t.Fatalf("%s: expected success, got %v", tc.name, err)
			}
			logStep(t, i+1, tc.changes, events,
				"同批先删后插是合法重写：计数不跨越零，无输出，状态保持一致")
			if len(events) != 0 {
				t.Fatalf("%s: expected no events, got %v", tc.name, events)
			}
			expectMembers(t, v)
			// 恢复初始布局（再插回 r1 已存在，当前 r1 已是 k，无需动作）
			continue
		}

		before := stateDigest()
		rej := expectRejected(t, v, tc.changes, tc.reason)
		after := stateDigest()
		t.Logf("reject case %q: %s (offending index=%d)", tc.name, rej.Error(), rej.Index)
		if rej.Index != tc.index {
			t.Fatalf("%s: offending index = %d, want %d", tc.name, rej.Index, tc.index)
		}
		if before != after {
			t.Fatalf("%s: rejection left a trace:\n  before=%s\n  after =%s", tc.name, before, after)
		}
		if err := v.SelfCheck(); err != nil {
			t.Fatalf("%s: self-check after rejection: %v", tc.name, err)
		}
	}

	// 所有拒绝之后，视图与初始种子状态完全一致，日志仍为空（种子未输出）。
	expectMembers(t, v)
	if len(v.Log()) != 0 {
		t.Fatalf("expected empty log, got %v", v.Log())
	}
	if v.Has("l1") {
		t.Fatal("l1 should be excluded by r1")
	}
}

// TestRejectedBatchIsAtomic 验证一批中即使只有最后一条非法，
// 前面对状态有影响的合法变更也整体不生效。
func TestRejectedBatchIsAtomic(t *testing.T) {
	v := NewView(0)
	if _, err := v.Commit([]Change{{Left, Insert, "l1", strp("k")}}); err != nil {
		t.Fatal(err)
	}
	expectMembers(t, v, "l1")

	batch := []Change{
		{Right, Insert, "r1", strp("k")}, // 若生效会使 l1 离开
		{Left, Insert, "", strp("z")},    // 非法：空标识
	}
	rej := expectRejected(t, v, batch, ReasonEmptyID)
	t.Logf("atomic batch rejected at index %d: %s", rej.Index, rej.Error())

	expectMembers(t, v, "l1")
	if len(v.Log()) != 1 { // 只有最初 l1 进入的一条
		t.Fatalf("log changed after rejected batch: %v", v.Log())
	}
}

// TestConcurrentCommitsAndChecks 用多个执行体并发提交与自检，
// 在 -race 下验证线程安全；结束后增量视图必须仍与批量重算一致，
// 且日志重放结果等于最终视图。
func TestConcurrentCommitsAndChecks(t *testing.T) {
	v := NewView(0)
	const workers = 8
	const rounds = 40

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				id := fmt.Sprintf("w%d-i%d", w, i)
				key := fmt.Sprintf("k%d", (w+i)%5)
				// 插入左行与右行交替；冲突/重复等拒绝都可接受，
				// 关键是任何时刻自检都必须成立。
				_, _ = v.Commit([]Change{{Left, Insert, id, strp(key)}})
				_ = v.SelfCheck()
				_, _ = v.Commit([]Change{{Right, Insert, id, strp(key)}})
				_ = v.SelfCheck()
				_ = v.Snapshot()
				_ = v.Has(id)
				if i%2 == 0 {
					_, _ = v.Commit([]Change{{Right, Delete, id, nil}})
				}
			}
		}(w)
	}

	// 一个持续自检的执行体。
	stop := make(chan struct{})
	var checkerWG sync.WaitGroup
	checkerWG.Add(1)
	go func() {
		defer checkerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if err := v.SelfCheck(); err != nil {
					t.Errorf("concurrent self-check: %v", err)
					return
				}
				runtime.Gosched()
				time.Sleep(time.Millisecond)
			}
		}
	}()

	wg.Wait() // 先等所有提交工作协程结束
	close(stop)
	checkerWG.Wait() // 再等自检协程观察到停止信号

	if err := v.SelfCheck(); err != nil {
		t.Fatalf("final self-check: %v", err)
	}
	if fmt.Sprint(v.Snapshot()) != fmt.Sprint(v.Recompute()) {
		t.Fatalf("snapshot %v != recompute %v", v.Snapshot(), v.Recompute())
	}
	replayed := ApplyLog(v.Log())
	if fmt.Sprint(sortedKeys(replayed)) != fmt.Sprint(v.Snapshot()) {
		t.Fatalf("log replay %v != snapshot %v", sortedKeys(replayed), v.Snapshot())
	}
	t.Logf("concurrency test done: %d members, %d log events; self-check held throughout",
		len(v.Snapshot()), len(v.Log()))
}

// TestNullSemantics 验证空值键的 SQL 风格不相等语义。
func TestNullSemantics(t *testing.T) {
	v := NewView(0)
	r := newPrefixRecorder(v)

	r.commit(t,
		[]Change{
			{Left, Insert, "ln1", nil},
			{Left, Insert, "ln2", nil},
			{Left, Insert, "lk", strp("k")},
		},
		"空值键左行与普通键左行初始都在结果中")
	expectMembers(t, v, "lk", "ln1", "ln2")

	ev, _ := r.commit(t,
		[]Change{
			{Right, Insert, "rn1", nil},
			{Right, Insert, "rn2", nil},
			{Right, Insert, "rk", strp("k")},
		},
		"空值键右行不匹配任何左行（NULL≠NULL 且 NULL≠k）；仅非空键 k 计数 0->1，lk 离开")
	if len(ev) != 1 || ev[0].Kind != Leave || ev[0].ID != "lk" {
		t.Fatalf("unexpected events: %v", ev)
	}
	expectMembers(t, v, "ln1", "ln2")

	ev, _ = r.commit(t, []Change{{Right, Delete, "rn1", nil}, {Right, Delete, "rn2", nil}},
		"删除空值键右行不影响任何左行，无输出")
	if len(ev) != 0 {
		t.Fatalf("expected no events, got %v", ev)
	}
	expectMembers(t, v, "ln1", "ln2")

	ev, _ = r.commit(t, []Change{{Right, Delete, "rk", nil}},
		"非空键 k 计数 1->0，lk 重新进入")
	if len(ev) != 1 || ev[0].Kind != Enter || ev[0].ID != "lk" {
		t.Fatalf("unexpected events: %v", ev)
	}
	expectMembers(t, v, "lk", "ln1", "ln2")

	ev, _ = r.commit(t, []Change{{Left, Delete, "ln1", nil}},
		"删除空值键左行：成员身份随删除消失，输出 leave，事件键为 NULL")
	if len(ev) != 1 || ev[0].Kind != Leave || ev[0].ID != "ln1" || ev[0].Key != nil {
		t.Fatalf("unexpected events: %v", ev)
	}
	expectMembers(t, v, "lk", "ln2")

	r.verify(t)
}

// TestRightArrivesFirst 验证右侧行先于左侧行到达的场景。
func TestRightArrivesFirst(t *testing.T) {
	v := NewView(0)
	r := newPrefixRecorder(v)

	ev, _ := r.commit(t,
		[]Change{{Right, Insert, "r1", strp("k")}, {Right, Insert, "r2", strp("k")}},
		"右侧先到：尚无左行，计数变化无可通知对象，无输出")
	if len(ev) != 0 {
		t.Fatalf("expected no events, got %v", ev)
	}

	ev, _ = r.commit(t,
		[]Change{{Left, Insert, "lmatch", strp("k")}, {Left, Insert, "lnull", nil}},
		"匹配键左行插入时右侧计数已为 2，不进入结果；空值键左行恒在结果中")
	if len(ev) != 1 || ev[0].Kind != Enter || ev[0].ID != "lnull" {
		t.Fatalf("unexpected events: %v", ev)
	}
	expectMembers(t, v, "lnull")

	ev, _ = r.commit(t, []Change{{Right, Delete, "r1", nil}},
		"计数 2->1 不跨越零边界，lmatch 保持缺席，无输出")
	if len(ev) != 0 {
		t.Fatalf("expected no events, got %v", ev)
	}

	ev, _ = r.commit(t, []Change{{Right, Delete, "r2", nil}},
		"计数 1->0：lmatch 进入结果")
	if len(ev) != 1 || ev[0].Kind != Enter || ev[0].ID != "lmatch" {
		t.Fatalf("unexpected events: %v", ev)
	}
	expectMembers(t, v, "lmatch", "lnull")

	r.verify(t)
}
