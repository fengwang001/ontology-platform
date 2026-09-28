package snapshotlog

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"
)

// newTestLogger 把日志写入内存缓冲，便于断言“输入、输出与判定依据”。
func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h)
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("期望错误 %v，实际 %v", target, err)
	}
}

// viewKeys 返回视图排序后的键，便于稳定断言。
func viewKeys(t *testing.T, x *Syncer) []int64 {
	t.Helper()
	keys := make([]int64, 0)
	for k := range x.View() {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// TestBoundaryKeys 覆盖键恰好落在范围边界（含端点）、相邻范围不相交。
func TestBoundaryKeys(t *testing.T) {
	var buf bytes.Buffer
	src := NewSource()
	x := NewSyncer(src, WithLogger(newTestLogger(&buf)))

	if _, err := x.BeginSnapshot(KeyRange{Start: 10, End: 20}); err != nil {
		t.Fatalf("BeginSnapshot: %v", err)
	}
	// 端点 10/20 与范围外 9/21 同时写入。
	src.Put(10, "a") // seq1 范围内（左边界）
	src.Put(20, "b") // seq2 范围内（右边界）
	src.Put(9, "c")  // seq3 范围外
	src.Put(21, "d") // seq4 范围外

	rows, snapSeq, err := x.ReadSnapshot(KeyRange{Start: 10, End: 20})
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if snapSeq != 4 || len(rows) != 2 || rows[10] != "a" || rows[20] != "b" {
		t.Fatalf("快照边界过滤错误: snapSeq=%d rows=%v", snapSeq, rows)
	}

	if _, err := x.FinishSnapshot(KeyRange{Start: 10, End: 20}); err != nil {
		t.Fatalf("FinishSnapshot: %v", err)
	}
	view := x.View()
	if len(view) != 2 || view[10] != "a" || view[20] != "b" {
		t.Fatalf("基线视图应恰好包含边界键 10/20，实际 %v", view)
	}

	// 轮询阶段：更新左边界、删除右边界；范围外写入必须被过滤。
	src.Put(10, "a2") // seq5
	src.Delete(20)    // seq6
	src.Put(9, "x")   // seq7 范围外
	src.Put(21, "y")  // seq8 范围外
	events, err := x.Poll()
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 2 || events[0].Entry.Seq != 5 || events[1].Entry.Seq != 6 {
		t.Fatalf("轮询应只输出边界上的 seq5/seq6，实际 %+v", events)
	}
	view = x.View()
	if len(view) != 1 || view[10] != "a2" {
		t.Fatalf("轮询后视图错误: %v", view)
	}

	// 相邻范围 [21,30] 与 [10,20] 不相交（20 < 21），应被接受。
	if _, err := x.BeginSnapshot(KeyRange{Start: 21, End: 30}); err != nil {
		t.Fatalf("相邻不相交范围被拒绝: %v", err)
	}

	log := buf.String()
	for _, want := range []string{"BeginSnapshot 接受", "FinishSnapshot 接受", "Poll"} {
		if !strings.Contains(log, want) {
			t.Fatalf("日志缺少 %q；完整日志:\n%s", want, log)
		}
	}
}

// TestSnapshotDuringWrites 覆盖快照期间持续写入：基线修正与高水位后追平，
// 并验证输出不重复、位置不回退。
func TestSnapshotDuringWrites(t *testing.T) {
	src := NewSource()
	x := NewSyncer(src, WithLogger(newTestLogger(&bytes.Buffer{})))

	r := KeyRange{Start: 1, End: 100}
	src.Put(1, "v1") // seq1：低水位之前的旧数据

	low, err := x.BeginSnapshot(r)
	if err != nil || low != 1 {
		t.Fatalf("lowSeq 应为 1，实际 %d err=%v", low, err)
	}

	// 低水位之后、读快照之前持续写入。
	src.Put(2, "v2") // seq2
	src.Put(3, "v3") // seq3

	rows, snapSeq, err := x.ReadSnapshot(r)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if snapSeq != 3 || rows[2] != "v2" || rows[3] != "v3" {
		t.Fatalf("快照应反映 seq3 时刻状态，snapSeq=%d rows=%v", snapSeq, rows)
	}
	// 重复读快照必须返回同一份冻结内容。
	if rows2, _, err := x.ReadSnapshot(r); err != nil || len(rows2) != len(rows) {
		t.Fatalf("重复读快照结果不一致: %v err=%v", rows2, err)
	}

	// 读快照之后、记高水位之前继续写入。
	src.Put(2, "v2b") // seq4：快照期间的更新
	src.Delete(3)     // seq5：快照期间的删除

	events, err := x.FinishSnapshot(r)
	if err != nil {
		t.Fatalf("FinishSnapshot: %v", err)
	}
	// (lowSeq=1, highSeq=5] 落在范围内的日志为 seq2..5，按序修正。
	gotSeqs := []int64{}
	for _, e := range events {
		gotSeqs = append(gotSeqs, e.Entry.Seq)
	}
	if fmt.Sprint(gotSeqs) != "[2 3 4 5]" {
		t.Fatalf("修正日志序号应为 [2 3 4 5]，实际 %v", gotSeqs)
	}
	view := x.View()
	if len(view) != 2 || view[1] != "v1" || view[2] != "v2b" {
		t.Fatalf("修正后视图应为 {1:v1,2:v2b}（k3 已删），实际 %v", view)
	}
	if x.AppliedSeq(r) != 5 {
		t.Fatalf("完成后处理位置应为高水位 5，实际 %d", x.AppliedSeq(r))
	}

	// 高水位之后的写入才能在轮询中输出；seq<=5 绝不重复输出。
	src.Put(4, "v4") // seq6
	ev1, err := x.Poll()
	if err != nil {
		t.Fatalf("Poll1: %v", err)
	}
	if len(ev1) != 1 || ev1[0].Entry.Seq != 6 {
		t.Fatalf("首次轮询应只输出 seq6，实际 %+v", ev1)
	}
	prev := x.AppliedSeq(r)
	ev2, _ := x.Poll()
	if len(ev2) != 0 {
		t.Fatalf("无新写入时轮询必须为空（不重复），实际 %+v", ev2)
	}
	if x.AppliedSeq(r) != prev {
		t.Fatalf("空轮询导致处理位置回退/变动: %d -> %d", prev, x.AppliedSeq(r))
	}

	src.Put(1, "v1x") // seq7
	ev3, _ := x.Poll()
	if len(ev3) != 1 || ev3[0].Entry.Seq != 7 {
		t.Fatalf("二次轮询应只输出 seq7，实际 %+v", ev3)
	}
	view = x.View()
	if len(view) != 3 || view[1] != "v1x" || view[2] != "v2b" || view[4] != "v4" {
		t.Fatalf("追平后视图与源表不一致: %v", view)
	}

	// 与源表范围内当前状态逐键比对。
	want := src.snapshot(r).rows
	if len(view) != len(want) {
		t.Fatalf("视图行数 %d 与源表 %d 不一致", len(view), len(want))
	}
	for k, v := range want {
		if view[k] != v {
			t.Fatalf("键 %d 视图值=%q 源表值=%q", k, view[k], v)
		}
	}
}

// TestInvalidRange 覆盖非法键范围及其“无副作用”。
func TestInvalidRange(t *testing.T) {
	var buf bytes.Buffer
	src := NewSource()
	x := NewSyncer(src, WithLogger(newTestLogger(&buf)))

	bad := KeyRange{Start: 5, End: 1}
	if _, err := x.BeginSnapshot(bad); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("应拒绝 start>end，实际 %v", err)
	}
	if x.AppliedSeq(bad) != 0 || len(x.View()) != 0 {
		t.Fatal("非法范围被拒绝后不得登记范围或改变视图")
	}
	// 被拒范围不占用键空间：合法范围仍可建立。
	good := KeyRange{Start: 1, End: 5}
	if _, err := x.BeginSnapshot(good); err != nil {
		t.Fatalf("合法范围建立失败: %v", err)
	}
	// 对不存在的范围读快照/完成都属于阶段顺序错误。
	if _, _, err := x.ReadSnapshot(KeyRange{Start: 90, End: 99}); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("未开始就读快照应报阶段错误，实际 %v", err)
	}
	if _, err := x.FinishSnapshot(KeyRange{Start: 90, End: 99}); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("未开始就完成应报阶段错误，实际 %v", err)
	}
	if !strings.Contains(buf.String(), "invalid_range") {
		t.Fatalf("日志缺少非法范围判定依据:\n%s", buf.String())
	}
}

// TestRangeOverlap 覆盖范围相交（含仅边界接触）与已完成范围相交。
func TestRangeOverlap(t *testing.T) {
	src := NewSource()
	x := NewSyncer(src, WithLogger(newTestLogger(&bytes.Buffer{})))

	r1 := KeyRange{Start: 1, End: 10}
	if _, err := x.BeginSnapshot(r1); err != nil {
		t.Fatalf("r1: %v", err)
	}
	for _, r := range []KeyRange{
		{Start: 10, End: 20}, // 右边界 10 与 r1 接触
		{Start: 0, End: 1},   // 左边界 1 与 r1 接触
		{Start: 5, End: 6},   // 完全包含于 r1
		{Start: 1, End: 10},  // 完全相同
	} {
		if _, err := x.BeginSnapshot(r); !errors.Is(err, ErrRangeOverlap) {
			t.Fatalf("范围 %s 应判相交，实际 err=%v", r.String(), err)
		}
	}
	// 相邻但不相交 [11,20] 必须接受。
	r2 := KeyRange{Start: 11, End: 20}
	if _, err := x.BeginSnapshot(r2); err != nil {
		t.Fatalf("相邻范围不应判相交: %v", err)
	}

	// 完成 r1 后，与其重叠的新范围仍必须被拒绝（已完成范围占位）。
	if _, _, err := x.ReadSnapshot(r1); err != nil {
		t.Fatalf("ReadSnapshot r1: %v", err)
	}
	if _, err := x.FinishSnapshot(r1); err != nil {
		t.Fatalf("FinishSnapshot r1: %v", err)
	}
	before := len(x.View())
	appliedBefore := x.AppliedSeq(r1)
	if _, err := x.BeginSnapshot(KeyRange{Start: 5, End: 15}); !errors.Is(err, ErrRangeOverlap) {
		t.Fatalf("与已完成范围相交应拒绝，实际 %v", err)
	}
	if len(x.View()) != before || x.AppliedSeq(r1) != appliedBefore {
		t.Fatal("相交被拒后不得改变视图或处理位置")
	}
}

// TestPhaseOrder 覆盖三步流程的所有错误顺序。
func TestPhaseOrder(t *testing.T) {
	var buf bytes.Buffer
	src := NewSource()
	x := NewSyncer(src, WithLogger(newTestLogger(&buf)))
	r := KeyRange{Start: 1, End: 10}

	// 未开始 → 读/完成。
	if _, _, err := x.ReadSnapshot(r); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("实际 %v", err)
	}
	if _, err := x.FinishSnapshot(r); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("实际 %v", err)
	}
	// 已开始但未读快照 → 完成。
	if _, err := x.BeginSnapshot(r); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := x.FinishSnapshot(r); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("未读快照就完成应拒绝，实际 %v", err)
	}
	// 正常走完三步。
	if _, _, err := x.ReadSnapshot(r); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := x.FinishSnapshot(r); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	// 已完成 → 再读/再完成。
	if _, _, err := x.ReadSnapshot(r); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("完成后再读快照应拒绝，实际 %v", err)
	}
	if _, err := x.FinishSnapshot(r); !errors.Is(err, ErrPhaseOrder) {
		t.Fatalf("重复完成应拒绝，实际 %v", err)
	}
	if !strings.Contains(buf.String(), "phase_order") {
		t.Fatal("日志缺少阶段顺序判定依据")
	}
}

// TestViewLimitSnapshot 覆盖完成阶段行数超限：拒绝且不提交，之后可重试。
func TestViewLimitSnapshot(t *testing.T) {
	var buf bytes.Buffer
	src := NewSource()
	x := NewSyncer(src, WithLogger(newTestLogger(&buf)), WithMaxViewRows(2))
	r := KeyRange{Start: 1, End: 100}

	if _, err := x.BeginSnapshot(r); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	src.Put(1, "a")
	src.Put(2, "b")
	src.Put(3, "c") // 并入后将有 3 行 > 上限 2
	if _, _, err := x.ReadSnapshot(r); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := x.FinishSnapshot(r); !errors.Is(err, ErrViewLimitExceeded) {
		t.Fatalf("应判行数超限，实际 %v", err)
	}
	if len(x.View()) != 0 {
		t.Fatalf("超限被拒后视图必须保持为空，实际 %v", x.View())
	}
	if x.AppliedSeq(r) != 0 {
		t.Fatal("超限被拒后不得记录高水位/处理位置")
	}
	// 范围仍停留在快照阶段：删掉一键后重试应成功，且不丢数据。
	src.Delete(3)
	events, err := x.FinishSnapshot(r)
	if err != nil {
		t.Fatalf("回落至上限内后重试应成功: %v", err)
	}
	if len(x.View()) != 2 {
		t.Fatalf("重试后视图应有 2 行，实际 %v", x.View())
	}
	// 重试的高水位应包含此前被拒时的全部日志，输出不回退。
	lastSeq := events[len(events)-1].Entry.Seq
	if x.AppliedSeq(r) < lastSeq || lastSeq < 4 {
		t.Fatalf("重试后处理位置异常: applied=%d last=%d", x.AppliedSeq(r), lastSeq)
	}
	if !strings.Contains(buf.String(), "view_limit_exceeded") {
		t.Fatal("日志缺少行数超限判定依据")
	}
}

// TestViewLimitPoll 覆盖轮询阶段超限：整批原子回滚，位置不前进，重试不漏。
func TestViewLimitPoll(t *testing.T) {
	src := NewSource()
	x := NewSyncer(src, WithLogger(newTestLogger(&bytes.Buffer{})), WithMaxViewRows(2))
	r := KeyRange{Start: 1, End: 100}

	src.Put(1, "a")
	src.Put(2, "b")
	if _, err := x.BeginSnapshot(r); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.ReadSnapshot(r); err != nil {
		t.Fatal(err)
	}
	if _, err := x.FinishSnapshot(r); err != nil {
		t.Fatalf("2 行恰好达到上限应接受: %v", err)
	}
	high := x.AppliedSeq(r)

	// 一批里既有更新（不增行）又有新增（超 2 行）：整批必须拒绝。
	src.Put(1, "a2") // seq high+1，更新
	src.Put(3, "c")  // seq high+2，新增 → 3 行超限
	viewBefore := x.View()
	if _, err := x.Poll(); !errors.Is(err, ErrViewLimitExceeded) {
		t.Fatalf("轮询应判超限，实际 %v", err)
	}
	if x.AppliedSeq(r) != high {
		t.Fatalf("拒绝后处理位置不得前进: %d != %d", x.AppliedSeq(r), high)
	}
	if x.View()[1] != viewBefore[1] || len(x.View()) != 2 {
		t.Fatalf("拒绝后视图不得部分生效: %v", x.View())
	}

	// 同一条更新日志在重试批次中必须仍被应用（不漏、不重）。
	src.Delete(2) // 删除使最终行数回落至 2
	events, err := x.Poll()
	if err != nil {
		t.Fatalf("行数回落后续轮询应成功: %v", err)
	}
	seqs := []int64{}
	for _, e := range events {
		seqs = append(seqs, e.Entry.Seq)
	}
	if fmt.Sprint(seqs) != fmt.Sprint([]int64{high + 1, high + 2, high + 3}) {
		t.Fatalf("重试批次应包含此前被回滚的全部序号，实际 %v", seqs)
	}
	view := x.View()
	if len(view) != 2 || view[1] != "a2" || view[3] != "c" {
		t.Fatalf("重试后视图应为 {1:a2,3:c}，实际 %v", view)
	}
}

// TestNegativeKeys 验证负整数键范围与边界过滤同样成立。
func TestNegativeKeys(t *testing.T) {
	src := NewSource()
	x := NewSyncer(src, WithLogger(newTestLogger(&bytes.Buffer{})))
	r := KeyRange{Start: -10, End: -1}
	if r.String() != "[-10,-1]" {
		t.Fatalf("范围字符串格式错误: %s", r.String())
	}
	src.Put(-10, "lo") // 左边界
	src.Put(-1, "hi")  // 右边界
	src.Put(0, "out")  // 范围外
	if _, err := x.BeginSnapshot(r); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.ReadSnapshot(r); err != nil {
		t.Fatal(err)
	}
	if _, err := x.FinishSnapshot(r); err != nil {
		t.Fatal(err)
	}
	view := x.View()
	if len(view) != 2 || view[-10] != "lo" || view[-1] != "hi" {
		t.Fatalf("负数键范围视图错误: %v", view)
	}
	src.Put(-5, "mid")
	if evs, err := x.Poll(); err != nil || len(evs) != 1 || evs[0].Entry.Key != -5 {
		t.Fatalf("负数键轮询错误: evs=%+v err=%v", evs, err)
	}
}

// TestDeterminism 同一输入序列反复计算，输出（视图、事件序号、位置）必须完全相同。
func TestDeterminism(t *testing.T) {
	type result struct {
		view    string
		polls   string
		applied int64
	}
	run := func() result {
		src := NewSource()
		x := NewSyncer(src, WithLogger(newTestLogger(&bytes.Buffer{})))
		r := KeyRange{Start: 1, End: 50}
		src.Put(1, "init")
		_, _ = x.BeginSnapshot(r)
		src.Put(2, "two")
		_, _, _ = x.ReadSnapshot(r)
		src.Put(2, "two2")
		src.Put(3, "three")
		_, _ = x.FinishSnapshot(r)
		var pollSeqs []int64
		for i := 0; i < 3; i++ {
			src.Put(int64(10+i), "v")
			evs, _ := x.Poll()
			for _, e := range evs {
				pollSeqs = append(pollSeqs, e.Entry.Seq)
			}
		}
		evs, _ := x.Poll()
		if len(evs) != 0 {
			t.Fatal("确定性场景中末次空轮询应为空")
		}
		v := x.View()
		ks := viewKeys(t, x)
		parts := make([]string, 0, len(ks))
		for _, k := range ks {
			parts = append(parts, fmt.Sprintf("%d=%s", k, v[k]))
		}
		return result{
			view:    strings.Join(parts, ","),
			polls:   fmt.Sprint(pollSeqs),
			applied: x.AppliedSeq(r),
		}
	}
	first := run()
	for i := 0; i < 10; i++ {
		if got := run(); got != first {
			t.Fatalf("第 %d 次运行结果不一致:\nfirst=%+v\ngot  =%+v", i, first, got)
		}
	}
}
