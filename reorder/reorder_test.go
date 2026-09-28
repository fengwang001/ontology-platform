package reorder

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// testEnv 包装一个 Buffer 与日志收集器，测试结束后把输入、两路输出与组件日志一并打印。
type testEnv struct {
	t      *testing.T
	b      *Buffer
	logBuf bytes.Buffer
}

func newTestEnv(t *testing.T, capacity int, lag int64) *testEnv {
	t.Helper()
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	b, err := NewBuffer(capacity, lag, logger)
	if err != nil {
		t.Fatalf("NewBuffer: unexpected error: %v", err)
	}
	return &testEnv{t: t, b: b, logBuf: logBuf}
}

// dump 打印输入序列、主/旁路输出、待发缓冲与组件内部日志（含每次判定依据）。
func (e *testEnv) dump(input []Event) {
	e.t.Helper()
	e.t.Logf("input: %s", formatEvents(input))
	e.t.Logf("main output (sorted by time,seq): %s", formatEvents(e.b.MainOutput()))
	e.t.Logf("side output (late, arrival order): %s", formatEvents(e.b.SideOutput()))
	e.t.Logf("pending: %s watermark: %d", formatEvents(e.b.Pending()), e.b.Watermark())
	e.t.Logf("component log:\n%s", e.logBuf.String())
}

func formatEvents(es []Event) string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = fmt.Sprintf("{%s t=%d}", e.ID, e.Time)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// acceptAll 顺序接受全部事件，返回每个事件的错误（nil 表示接受）。
func acceptAll(b *Buffer, es []Event) []error {
	errs := make([]error, len(es))
	for i, e := range es {
		_, errs[i] = b.Accept(e)
	}
	return errs
}

// TestStableOrderSameTime 相同事件时间的事件必须按到达序号稳定排序输出。
func TestStableOrderSameTime(t *testing.T) {
	env := newTestEnv(t, 10, 10)
	input := []Event{
		{ID: "a", Time: 5},
		{ID: "b", Time: 5},
		{ID: "c", Time: 5},
		{ID: "d", Time: 15}, // 推进水位线到 5，释放 a,b,c
	}
	errs := acceptAll(env.b, input)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("event %s unexpectedly rejected: %v", input[i].ID, err)
		}
	}
	env.b.Flush()

	main := env.b.MainOutput()
	gotIDs := make([]string, len(main))
	for i, e := range main {
		gotIDs[i] = e.ID
	}
	wantIDs := []string{"a", "b", "c", "d"}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("same-time events not stably ordered by seq: got %v want %v", gotIDs, wantIDs)
	}
	if side := env.b.SideOutput(); len(side) != 0 {
		t.Fatalf("expected no side output, got %s", formatEvents(side))
	}
	env.dump(input)
}

// TestLateSideOutput 时间不超过水位线的事件立即进入旁路，且不进主输出。
func TestLateSideOutput(t *testing.T) {
	env := newTestEnv(t, 10, 5) // watermark = maxEventTime - 5
	input := []Event{
		{ID: "a", Time: 10}, // watermark -> 5，a 缓冲（10 > 5）
		{ID: "b", Time: 5},  // 5 <= 5：迟到，旁路
		{ID: "c", Time: 4},  // 4 <= 5：迟到，旁路
		{ID: "d", Time: 11}, // watermark -> 6，a(10) 仍未到期
		{ID: "e", Time: 6},  // 6 <= 6：迟到，旁路
	}
	acceptAll(env.b, input)
	env.b.Flush()

	main := env.b.MainOutput()
	side := env.b.SideOutput()
	wantMain := []string{"a", "d"}
	wantSide := []string{"b", "c", "e"}
	if idsOf(main); !equalStrings(idsOf(main), wantMain) {
		t.Fatalf("main output = %v, want %v", idsOf(main), wantMain)
	}
	if !equalStrings(idsOf(side), wantSide) {
		t.Fatalf("side output = %v, want %v", idsOf(side), wantSide)
	}
	env.dump(input)
}

// TestInvalidConstruction 构造参数非法必须返回 ErrInvalidParam。
func TestInvalidConstruction(t *testing.T) {
	for _, tc := range []struct {
		name     string
		capacity int
		lag      int64
	}{
		{"zero capacity", 0, 0},
		{"negative capacity", -1, 0},
		{"negative lag", 4, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := NewBuffer(tc.capacity, tc.lag, nil)
			if !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("err = %v, want ErrInvalidParam", err)
			}
			if b != nil {
				t.Fatalf("buffer should be nil on error, got %#v", b)
			}
		})
	}
}

// TestEmptyID 空标识拒绝。
func TestEmptyID(t *testing.T) {
	env := newTestEnv(t, 10, 0)
	input := []Event{{ID: "", Time: 1}}
	_, err := env.b.Accept(input[0])
	if !errors.Is(err, ErrEmptyID) {
		t.Fatalf("err = %v, want ErrEmptyID", err)
	}
	if ReasonOf(err) != ReasonEmptyID {
		t.Fatalf("reason = %q, want %q", ReasonOf(err), ReasonEmptyID)
	}
	if len(env.b.MainOutput()) != 0 || len(env.b.SideOutput()) != 0 {
		t.Fatalf("rejected event must not appear in any output")
	}
	env.dump(input)
}

// TestNegativeTime 负事件时间拒绝。
func TestNegativeTime(t *testing.T) {
	env := newTestEnv(t, 10, 0)
	input := []Event{{ID: "x", Time: -1}}
	_, err := env.b.Accept(input[0])
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("err = %v, want ErrInvalidParam", err)
	}
	if ReasonOf(err) != ReasonInvalidParam {
		t.Fatalf("reason = %q, want %q", ReasonOf(err), ReasonInvalidParam)
	}
	env.dump(input)
}

// TestDuplicateID 重复标识拒绝，且即使重复事件本身已迟到，仍以“重复”而非“迟到”拒绝。
func TestDuplicateID(t *testing.T) {
	env := newTestEnv(t, 10, 0)
	input := []Event{
		{ID: "a", Time: 10}, // 水位线 -> 10
		{ID: "a", Time: 1},  // 既重复又迟到：重复优先判定
	}
	if _, err := env.b.Accept(input[0]); err != nil {
		t.Fatal(err)
	}
	_, err := env.b.Accept(input[1])
	if !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("err = %v, want ErrDuplicateID", err)
	}
	if ReasonOf(err) != ReasonDuplicateID {
		t.Fatalf("reason = %q, want %q", ReasonOf(err), ReasonDuplicateID)
	}
	// 旁路只允许出现真正被接受的迟到事件，重复事件不得混入。
	if len(env.b.SideOutput()) != 0 {
		t.Fatalf("duplicate event leaked into side output: %s", formatEvents(env.b.SideOutput()))
	}
	env.dump(input)
}

// TestBufferFull 非迟到事件需要进缓冲但容量不足时拒绝，且任何状态不变（含水线与序号）。
func TestBufferFull(t *testing.T) {
	env := newTestEnv(t, 2, 5) // watermark = maxEventTime - 5
	input := []Event{
		{ID: "e1", Time: 10}, // wm->5，e1 缓冲
		{ID: "e2", Time: 11}, // wm->6，e1/e2 均缓冲（10,11 > 6），缓冲满
		{ID: "e3", Time: 12}, // wm 试算->7，e1/e2 仍未到期（10,11 > 7）：超限拒绝
	}
	if _, err := env.b.Accept(input[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := env.b.Accept(input[1]); err != nil {
		t.Fatal(err)
	}
	_, err := env.b.Accept(input[2])
	if !errors.Is(err, ErrBufferFull) {
		t.Fatalf("err = %v, want ErrBufferFull", err)
	}
	if ReasonOf(err) != ReasonBufferFull {
		t.Fatalf("reason = %q, want %q", ReasonOf(err), ReasonBufferFull)
	}

	pending := env.b.Pending()
	if len(pending) != 2 || pending[0].ID != "e1" || pending[1].ID != "e2" {
		t.Fatalf("buffer mutated after rejection: %s", formatEvents(pending))
	}
	if env.b.Watermark() != 6 {
		t.Fatalf("watermark changed after rejection: %d, want 6", env.b.Watermark())
	}

	// 序号与水线都不得被消耗：一个足够大的事件时间释放 e1/e2 后，
	// 新事件应拿到被拒绝调用本应消耗的序号 3。
	seq, err := env.b.Accept(Event{ID: "e4", Time: 20}) // wm->15：e1(10)、e2(11) 到期释放
	if err != nil {
		t.Fatalf("accept after rejected call failed: %v", err)
	}
	if seq != 3 {
		t.Fatalf("seq = %d, want 3 (rejected call must not consume a seq)", seq)
	}
	main := env.b.MainOutput()
	if !equalStrings(idsOf(main), []string{"e1", "e2"}) {
		t.Fatalf("released main = %v, want [e1 e2]", idsOf(main))
	}
	env.dump(input)
}

// TestRejectionLeavesStateUntouched 汇总检查：每类拒绝前后，水位线、序号、缓冲与两路输出完全一致。
func TestRejectionLeavesStateUntouched(t *testing.T) {
	env := newTestEnv(t, 2, 5)
	seed := []Event{
		{ID: "a", Time: 10}, // 水位线 -> 5，a 缓冲
		{ID: "b", Time: 11}, // 缓冲满（2 个）
	}
	acceptAll(env.b, seed)

	snapshot := func() string {
		return fmt.Sprintf("wm=%d pending=%s main=%s side=%s",
			env.b.Watermark(),
			formatEvents(env.b.Pending()),
			formatEvents(env.b.MainOutput()),
			formatEvents(env.b.SideOutput()))
	}

	before := snapshot()
	if _, err := env.b.Accept(Event{ID: "", Time: 1}); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("want ErrEmptyID, got %v", err)
	}
	if got := snapshot(); got != before {
		t.Fatalf("state changed after empty-id rejection:\nbefore %s\nafter  %s", before, got)
	}

	if _, err := env.b.Accept(Event{ID: "x", Time: -1}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam, got %v", err)
	}
	if got := snapshot(); got != before {
		t.Fatalf("state changed after invalid-param rejection:\nbefore %s\nafter  %s", before, got)
	}

	if _, err := env.b.Accept(Event{ID: "a", Time: 100}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("want ErrDuplicateID, got %v", err)
	}
	if got := snapshot(); got != before {
		t.Fatalf("state changed after duplicate-id rejection:\nbefore %s\nafter  %s", before, got)
	}

	if _, err := env.b.Accept(Event{ID: "c", Time: 12}); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("want ErrBufferFull, got %v", err)
	}
	if got := snapshot(); got != before {
		t.Fatalf("state changed after buffer-full rejection:\nbefore %s\nafter  %s", before, got)
	}
	env.dump(seed)
}

// TestNoLossNoDuplicate 乱序序列下，主输出 + 旁路输出恰好覆盖每个被接受事件一次。
func TestNoLossNoDuplicate(t *testing.T) {
	env := newTestEnv(t, 20, 3)
	input := []Event{
		{ID: "1", Time: 5},
		{ID: "2", Time: 9},
		{ID: "3", Time: 2}, // wm=6 后到达：迟到
		{ID: "4", Time: 7},
		{ID: "5", Time: 6}, // 6 <= 6：迟到
		{ID: "6", Time: 15},
		{ID: "7", Time: 10},
		{ID: "8", Time: 1}, // 迟到
	}
	acceptAll(env.b, input)
	env.b.Flush()
	env.dump(input)

	main := env.b.MainOutput()
	side := env.b.SideOutput()
	all := append(append([]Event{}, main...), side...)
	if len(all) != len(input) {
		t.Fatalf("accepted=%d but total emitted=%d (loss or duplicate)", len(input), len(all))
	}
	seen := map[string]int{}
	for _, e := range all {
		seen[e.ID]++
	}
	for _, in := range input {
		if seen[in.ID] != 1 {
			t.Fatalf("event %s emitted %d times, want exactly 1", in.ID, seen[in.ID])
		}
	}
	assertMainOrdered(t, env.b)
}

// TestOutOfOrderReleasesSorted 水位线推进释放的一批事件必须整体按（时间, 序号）有序。
func TestOutOfOrderReleasesSorted(t *testing.T) {
	env := newTestEnv(t, 50, 100)
	// 大 lag 下全部缓冲，最后一个事件把水位线推高，一次性释放此前全部事件。
	times := []int64{50, 10, 40, 10, 30, 20, 50, 40}
	input := make([]Event, len(times))
	for i, tm := range times {
		input[i] = Event{ID: fmt.Sprintf("e%d", i), Time: tm}
	}
	acceptAll(env.b, input)
	// 触发大幅推进水位线：time=300 -> wm=200，释放全部缓冲事件。
	if _, err := env.b.Accept(Event{ID: "trigger", Time: 300}); err != nil {
		t.Fatal(err)
	}
	env.b.Flush()

	main := env.b.MainOutput()
	env.dump(append(input, Event{ID: "trigger", Time: 300}))
	assertMainOrdered(t, env.b)

	// 同时间 10 的 e1、e3，以及同时间 40/50 的事件，相对顺序必须等于到达顺序。
	pos := map[string]int{}
	for i, e := range main {
		pos[e.ID] = i
	}
	if !(pos["e1"] < pos["e3"]) {
		t.Fatalf("same-time tie not stable: e1@%d e3@%d", pos["e1"], pos["e3"])
	}
	if !(pos["e0"] < pos["e6"]) {
		t.Fatalf("same-time tie not stable: e0@%d e6@%d", pos["e0"], pos["e6"])
	}
	if !(pos["e2"] < pos["e7"]) {
		t.Fatalf("same-time tie not stable: e2@%d e7@%d", pos["e2"], pos["e7"])
	}
}

// TestDeterministicReplay 同一输入序列在独立 Buffer 上反复计算，输出必须逐字节一致。
func TestDeterministicReplay(t *testing.T) {
	input := []Event{
		{ID: "1", Time: 5}, {ID: "2", Time: 9}, {ID: "3", Time: 2},
		{ID: "4", Time: 7}, {ID: "5", Time: 6}, {ID: "6", Time: 15},
		{ID: "7", Time: 10}, {ID: "8", Time: 1}, {ID: "9", Time: 9},
	}
	run := func() string {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		b, err := NewBuffer(20, 3, logger)
		if err != nil {
			t.Fatal(err)
		}
		acceptAll(b, input)
		b.Flush()
		return fmt.Sprintf("wm=%d\nmain=%s\nside=%s\npending=%s",
			b.Watermark(), formatEvents(b.MainOutput()),
			formatEvents(b.SideOutput()), formatEvents(b.Pending()))
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); got != first {
			t.Fatalf("non-deterministic replay on iteration %d:\nfirst:\n%s\ngot:\n%s", i, first, got)
		}
	}
	t.Logf("deterministic result:\n%s", first)
}

// TestConcurrentExactlyOnceAndOrdered 并发提交下：被接受事件序号连续唯一，
// 主输出严格（时间, 序号）有序，每个事件在主/旁路中恰好出现一次。
func TestConcurrentExactlyOnceAndOrdered(t *testing.T) {
	const goroutines = 16
	const perG = 200
	const total = goroutines * perG

	env := newTestEnv(t, total, 25) // lag=25：小时间范围内同时压测缓冲释放与迟到旁路
	rng := rand.New(rand.NewSource(42))

	// 预生成每 goroutine 的事件，ID 全局唯一；时间在小范围内制造乱序与迟到。
	makeInput := func() [][]Event {
		all := make([][]Event, goroutines)
		for g := 0; g < goroutines; g++ {
			all[g] = make([]Event, perG)
			for i := 0; i < perG; i++ {
				all[g][i] = Event{
					ID:   fmt.Sprintf("g%d-%d", g, i),
					Time: rng.Int63n(50),
				}
			}
		}
		return all
	}
	input := makeInput()

	var wg sync.WaitGroup
	var mu sync.Mutex
	seqs := make([]uint64, 0, total)
	rejected := 0
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(evs []Event) {
			defer wg.Done()
			for _, e := range evs {
				seq, err := env.b.Accept(e)
				mu.Lock()
				if err != nil {
					rejected++
				} else {
					seqs = append(seqs, seq)
				}
				mu.Unlock()
			}
		}(input[g])
	}
	wg.Wait()
	env.b.Flush()

	if rejected != 0 {
		t.Fatalf("unique-id concurrent accepts should not be rejected, got %d", rejected)
	}

	// 序号必须是 1..total 的一个排列：连续、无缺、无重。
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	if len(seqs) != total {
		t.Fatalf("accepted seqs = %d, want %d", len(seqs), total)
	}
	for i, s := range seqs {
		if s != uint64(i+1) {
			t.Fatalf("seq gap/duplicate at position %d: got %d want %d", i, s, i+1)
		}
	}

	main := env.b.MainOutput()
	side := env.b.SideOutput()
	assertMainOrdered(t, env.b)

	// 主 + 旁路恰好包含全部输入 ID 一次。
	counts := make(map[string]int, total)
	for _, e := range main {
		counts[e.ID]++
	}
	for _, e := range side {
		counts[e.ID]++
	}
	if len(main)+len(side) != total {
		t.Fatalf("emitted=%d want %d", len(main)+len(side), total)
	}
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perG; i++ {
			id := fmt.Sprintf("g%d-%d", g, i)
			if counts[id] != 1 {
				t.Fatalf("event %s emitted %d times under concurrency", id, counts[id])
			}
		}
	}
	t.Logf("concurrent: main=%d side=%d, both disjoint and exactly-once", len(main), len(side))
}

// TestConcurrentRejectsAreSafe 并发混入重复/空标识/缓冲超限，拒绝不得破坏状态不变量。
func TestConcurrentRejectsAreSafe(t *testing.T) {
	const cap = 64
	env := newTestEnv(t, cap, 1_000_000) // 极大 lag：只缓冲不释放，很快打满容量
	var wg sync.WaitGroup
	var accepted, dup, empty, full int64
	var mu sync.Mutex

	// cap 个唯一事件把缓冲填满。
	for i := 0; i < cap; i++ {
		if _, err := env.b.Accept(Event{ID: fmt.Sprintf("fill-%d", i), Time: int64(i)}); err != nil {
			t.Fatal(err)
		}
	}

	worker := func(kind string) {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			var e Event
			switch kind {
			case "dup":
				e = Event{ID: "fill-0", Time: int64(10000 + i)}
			case "empty":
				e = Event{ID: "", Time: int64(i)}
			case "full":
				e = Event{ID: fmt.Sprintf("extra-%s-%d", kind, i), Time: int64(10000 + i)}
			}
			_, err := env.b.Accept(e)
			mu.Lock()
			switch {
			case errors.Is(err, ErrDuplicateID):
				dup++
			case errors.Is(err, ErrEmptyID):
				empty++
			case errors.Is(err, ErrBufferFull):
				full++
			case err == nil:
				accepted++
			default:
				t.Errorf("unexpected error: %v", err)
			}
			mu.Unlock()
		}
	}
	for _, kind := range []string{"dup", "empty", "full"} {
		wg.Add(1)
		go worker(kind)
	}
	wg.Wait()

	if accepted != 0 {
		t.Fatalf("no accept should succeed once buffer is held full, got %d", accepted)
	}
	if dup == 0 || empty == 0 || full == 0 {
		t.Fatalf("expected all reject kinds observed, got dup=%d empty=%d full=%d", dup, empty, full)
	}
	if pending := env.b.Pending(); len(pending) != cap {
		t.Fatalf("buffer size changed under concurrent rejections: %d", len(pending))
	}
	t.Logf("concurrent rejects: dup=%d empty=%d full=%d, buffer intact at %d", dup, empty, full, cap)
}

// TestSeqUnifiedAcrossOutputs 旁路事件与缓冲事件共用一套连续到达序号。
func TestSeqUnifiedAcrossOutputs(t *testing.T) {
	env := newTestEnv(t, 10, 0)
	events := []Event{
		{ID: "a", Time: 1}, // seq1，立即释放，wm=1
		{ID: "b", Time: 1}, // seq2，迟到 -> 旁路
		{ID: "c", Time: 5}, // seq3，立即释放，wm=5
		{ID: "d", Time: 2}, // seq4，迟到 -> 旁路
	}
	seqs := make([]uint64, len(events))
	for i, e := range events {
		s, err := env.b.Accept(e)
		if err != nil {
			t.Fatal(err)
		}
		seqs[i] = s
	}
	for i, s := range seqs {
		if s != uint64(i+1) {
			t.Fatalf("seq for event %s = %d, want %d", events[i].ID, s, i+1)
		}
	}
	main := env.b.MainOutput()
	side := env.b.SideOutput()
	if !equalStrings(idsOf(main), []string{"a", "c"}) {
		t.Fatalf("main = %v want [a c]", idsOf(main))
	}
	if !equalStrings(idsOf(side), []string{"b", "d"}) {
		t.Fatalf("side = %v want [b d]", idsOf(side))
	}
	env.dump(events)
}

// assertMainOrdered 断言主输出严格按（事件时间, 到达序号）升序。
func assertMainOrdered(t *testing.T, b *Buffer) {
	t.Helper()
	b.mu.Lock()
	es := slices.Clone(b.mainOutput)
	b.mu.Unlock()
	for i := 1; i < len(es); i++ {
		prev, cur := es[i-1], es[i]
		if cur.event.Time < prev.event.Time ||
			(cur.event.Time == prev.event.Time && cur.seq <= prev.seq) {
			t.Fatalf("main output not strictly ordered by (time,seq) at %d: {%s t=%d seq=%d} after {%s t=%d seq=%d}",
				i, cur.event.ID, cur.event.Time, cur.seq, prev.event.ID, prev.event.Time, prev.seq)
		}
	}
}

func idsOf(es []Event) []string {
	ids := make([]string, len(es))
	for i, e := range es {
		ids[i] = e.ID
	}
	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
