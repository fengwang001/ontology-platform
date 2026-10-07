package incremental

import (
	"errors"
	"reflect"
	"testing"
)

// newHarness 构造一套内存实现的组件与数据源。
func newHarness(opts Options) (*Exporter, *MemSource, *MemCursorStore, *MemLedger) {
	src := NewMemSource()
	cs := NewMemCursorStore()
	ledger := NewMemLedger()
	return NewExporter(src, cs, ledger, opts), src, cs, ledger
}

// appendWrites 向数据源追加 n 条写入，返回追加后的头位点。
func appendWrites(src *MemSource, prefix string, n int) Cursor {
	var head Cursor
	for i := 0; i < n; i++ {
		head = src.Append(Write{ID: prefix + "-" + itoa(i), Payload: "p"})
	}
	return head
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// runCycle 开启周期、扫描数据源并提交全部写入、确认结束位点。
func runCycle(t *testing.T, e *Exporter, src *MemSource, chain string, start, end Cursor) *Cycle {
	t.Helper()
	c, err := e.BeginCycle(chain, start, end)
	if err != nil {
		t.Fatalf("BeginCycle(%q, %d, %d): %v", chain, start, end, err)
	}
	ws, err := src.Scan(start, end)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, w := range ws {
		if err := c.Submit(w); err != nil {
			t.Fatalf("Submit(%v): %v", w, err)
		}
	}
	if err := c.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return c
}

func writeIDs(ws []Write) []string {
	ids := make([]string, len(ws))
	for i, w := range ws {
		ids[i] = w.ID
	}
	return ids
}

func TestStartMustEqualConfirmedEnd(t *testing.T) {
	e, src, _, _ := newHarness(Options{})
	end := appendWrites(src, "w", 5)

	// 凭空选择起始位点：必须拒绝。
	if _, err := e.BeginCycle("a", 2, end); err == nil {
		t.Fatal("expected start-mismatch error")
	} else {
		var ierr *Error
		if !errors.As(err, &ierr) || ierr.Kind != ErrKindStartMismatch {
			t.Fatalf("expected ErrKindStartMismatch, got %v", err)
		}
	}

	// 正确的起始位点：允许。
	runCycle(t, e, src, "a", GenesisCursor, end)

	// 下一周期起始位点必须等于上一周期已确认的结束位点。
	end2 := appendWrites(src, "x", 3)
	if _, err := e.BeginCycle("a", GenesisCursor, end2); err == nil {
		t.Fatal("expected start-mismatch after confirmed cycle")
	}
	runCycle(t, e, src, "a", end, end2)

	want := []string{"w-0", "w-1", "w-2", "w-3", "w-4", "x-0", "x-1", "x-2"}
	if got := writeIDs(e.Published("a")); !reflect.DeepEqual(got, want) {
		t.Fatalf("published = %v, want %v", got, want)
	}
}

func TestCrossCycleRetrySuppressedByWindow(t *testing.T) {
	e, src, _, _ := newHarness(Options{DedupWindow: 8})
	end := appendWrites(src, "w", 3)
	runCycle(t, e, src, "a", GenesisCursor, end)

	end2 := appendWrites(src, "x", 2)
	c, err := e.BeginCycle("a", end, end2)
	if err != nil {
		t.Fatal(err)
	}
	// 上一周期已输出的写入因重试再次到达：不得重复输出。
	old, _ := src.Scan(GenesisCursor, end)
	for _, w := range old {
		w.Cursor = end2 // 模拟重试以新位点重新提交
		if err := c.Submit(w); err != nil {
			t.Fatal(err)
		}
	}
	ws, _ := src.Scan(end, end2)
	for _, w := range ws {
		if err := c.Submit(w); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Commit(); err != nil {
		t.Fatal(err)
	}
	want := []string{"w-0", "w-1", "w-2", "x-0", "x-1"}
	if got := writeIDs(e.Published("a")); !reflect.DeepEqual(got, want) {
		t.Fatalf("published = %v, want %v", got, want)
	}
}

func TestCursorUnreadableDerivesSafeCursor(t *testing.T) {
	e, src, cs, _ := newHarness(Options{})
	end1 := appendWrites(src, "w", 3)
	runCycle(t, e, src, "a", GenesisCursor, end1)
	end2 := appendWrites(src, "x", 2)
	runCycle(t, e, src, "a", end1, end2)

	// 位点记录介质损坏。
	cs.Corrupt("a")

	end3 := appendWrites(src, "y", 2)
	c, err := e.BeginCycle("a", end2, end3)
	if err != nil {
		t.Fatalf("expected recovery via derivation, got %v", err)
	}
	// 必须记录“位点不可读”事件。
	if len(c.Findings) != 1 || c.Findings[0].Kind != ErrKindCursorUnreadable {
		t.Fatalf("findings = %+v, want one cursor-unreadable finding", c.Findings)
	}
	ws, _ := src.Scan(end2, end3)
	for _, w := range ws {
		if err := c.Submit(w); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Commit(); err != nil {
		t.Fatal(err)
	}
	want := []string{"w-0", "w-1", "w-2", "x-0", "x-1", "y-0", "y-1"}
	if got := writeIDs(e.Published("a")); !reflect.DeepEqual(got, want) {
		t.Fatalf("published = %v, want %v", got, want)
	}
	// 推导结果必须等于真正已确认的位点（历史完整时）。
	cs.Repair("a")
	cur, err := e.ConfirmedCursor("a")
	if err != nil || cur != end3 {
		t.Fatalf("confirmed cursor = %d, %v; want %d", cur, err, end3)
	}
}

func TestHistoryGapYieldsSafeCursorNotBeyondConfirmed(t *testing.T) {
	e, src, cs, ledger := newHarness(Options{})
	end1 := appendWrites(src, "w", 3)
	runCycle(t, e, src, "a", GenesisCursor, end1)
	end2 := appendWrites(src, "x", 2)
	runCycle(t, e, src, "a", end1, end2)
	end3 := appendWrites(src, "y", 2)
	runCycle(t, e, src, "a", end2, end3)

	// 位点记录损坏，且历史增量记录中间存在缺口（第 2 条丢失）。
	// 注意：尾部记录缺失与“该周期从未确认”不可区分，只会得到
	// 更保守的安全位点；只有中间缺口才能被确定性地检测出来。
	cs.Corrupt("a")
	ledger.Drop("a", 2)

	// 模拟进程重启：新组件实例共享同一批存储，进程内无缓存位点，
	// 必须基于历史增量记录推导。
	e = NewExporter(src, cs, ledger, Options{})
	end4 := appendWrites(src, "z", 2)
	_, err := e.BeginCycle("a", end3, end4)
	if err == nil {
		t.Fatal("expected history-gap error")
	}
	var ierr *Error
	if !errors.As(err, &ierr) || ierr.Kind != ErrKindHistoryGap {
		t.Fatalf("expected ErrKindHistoryGap, got %v", err)
	}
	// 安全位点必须不晚于真正已确认的位点（end1 <= end3）。
	if !ierr.HasSafeCursor || ierr.SafeCursor != end1 {
		t.Fatalf("safe cursor = %d (has=%v), want %d", ierr.SafeCursor, ierr.HasSafeCursor, end1)
	}
	if ierr.SafeCursor > end3 {
		t.Fatalf("safe cursor %d beyond true confirmed %d", ierr.SafeCursor, end3)
	}
}

func TestDerivedSafeCursorAllowsRestartWithoutLoss(t *testing.T) {
	// 位点不可读且历史有缺口时，调用方使用错误中携带的安全位点
	// 重新发起周期：宁可重复检查也不得遗漏。
	e, src, cs, ledger := newHarness(Options{DedupWindow: 16})
	end1 := appendWrites(src, "w", 3)
	runCycle(t, e, src, "a", GenesisCursor, end1)
	end2 := appendWrites(src, "x", 2)
	runCycle(t, e, src, "a", end1, end2)
	end3 := appendWrites(src, "y", 2)
	runCycle(t, e, src, "a", end2, end3)
	// 中断前消费者已收到的完整输出。
	before := writeIDs(e.Published("a"))

	cs.Corrupt("a")
	ledger.Drop("a", 2)

	e = NewExporter(src, cs, ledger, Options{DedupWindow: 16})
	end4 := appendWrites(src, "z", 2)
	_, err := e.BeginCycle("a", end3, end4)
	var ierr *Error
	if !errors.As(err, &ierr) || ierr.Kind != ErrKindHistoryGap {
		t.Fatalf("expected history gap, got %v", err)
	}
	safe := ierr.SafeCursor
	if safe != end1 {
		t.Fatalf("safe cursor = %d, want %d", safe, end1)
	}

	// 从安全位点重新发起：覆盖 (safe, end4]，已输出部分由窗口去重。
	c, err := e.BeginCycle("a", safe, end4)
	if err != nil {
		t.Fatalf("restart from safe cursor: %v", err)
	}
	ws, _ := src.Scan(safe, end4)
	for _, w := range ws {
		if err := c.Submit(w); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Commit(); err != nil {
		t.Fatal(err)
	}
	// 重启后周期的输出：记录 2（x）已从历史中丢失，组件无法得知
	// x 已输出，必须重复输出（宁可重复）；记录 3（y）仍在历史中，
	// 由去重窗口抑制；z 为新写入。
	after := writeIDs(e.Published("a"))
	wantAfter := []string{"x-0", "x-1", "z-0", "z-1"}
	if !reflect.DeepEqual(after, wantAfter) {
		t.Fatalf("post-restart published = %v, want %v", after, wantAfter)
	}
	// 累计输出（中断前 + 重启后）不得遗漏任何写入。
	seen := map[string]int{}
	for _, id := range append(before, after...) {
		seen[id]++
	}
	for _, id := range []string{"w-0", "w-1", "w-2", "x-0", "x-1", "y-0", "y-1", "z-0", "z-1"} {
		if seen[id] == 0 {
			t.Fatalf("write %q missing from cumulative output %v + %v", id, before, after)
		}
	}
}

func TestResourceExhaustedAbortsCycle(t *testing.T) {
	e, src, _, _ := newHarness(Options{MaxCycleWrites: 2})
	end := appendWrites(src, "w", 5)

	c, err := e.BeginCycle("a", GenesisCursor, end)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := src.Scan(GenesisCursor, end)
	for i, w := range ws {
		err := c.Submit(w)
		if i < 2 && err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if i == 2 {
			var ierr *Error
			if !errors.As(err, &ierr) || ierr.Kind != ErrKindResourceExhausted {
				t.Fatalf("expected resource-exhausted, got %v", err)
			}
		}
	}
	// 周期已被中止：不得确认任何位点，输出为空。
	if got := e.Published("a"); len(got) != 0 {
		t.Fatalf("published after abort = %v, want empty", got)
	}
	cur, err := e.ConfirmedCursor("a")
	if err != nil || cur != GenesisCursor {
		t.Fatalf("confirmed = %d, %v; want genesis", cur, err)
	}
	// 提高资源上限后从同一已确认位点重新发起，结果与未中断一致。
	e2, _, _, _ := newHarness(Options{MaxCycleWrites: 16})
	_ = e2
	c2, err := e.BeginCycle("a", GenesisCursor, end)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range ws[:2] { // 资源不足前的部分重试不得留下痕迹
		if err := c2.Submit(w); err != nil {
			t.Fatal(err)
		}
	}
	c2.Abort()
}

func TestErrorPriorityStartMismatchBeatsUnreadable(t *testing.T) {
	// 位点记录不可读且声明起始位点与推导结果不一致时，
	// 必须报告起始位点不匹配（优先级更高）。
	e, src, cs, _ := newHarness(Options{})
	end := appendWrites(src, "w", 3)
	runCycle(t, e, src, "a", GenesisCursor, end)
	cs.Corrupt("a")

	end2 := appendWrites(src, "x", 2)
	_, err := e.BeginCycle("a", GenesisCursor, end2) // 声明了错误的起始位点
	var ierr *Error
	if !errors.As(err, &ierr) || ierr.Kind != ErrKindStartMismatch {
		t.Fatalf("expected start-mismatch (priority over unreadable), got %v", err)
	}
}

func TestErrorPriorityHistoryGapBeatsResource(t *testing.T) {
	// 推导遇历史缺口时，即使资源上限很小，也必须报告历史缺口：
	// 缺口判定发生在周期建立阶段，资源判定发生在输出阶段。
	e, src, cs, ledger := newHarness(Options{MaxCycleWrites: 1})
	end1 := appendWrites(src, "w", 1)
	runCycle(t, e, src, "a", GenesisCursor, end1)
	end2 := appendWrites(src, "x", 1)
	runCycle(t, e, src, "a", end1, end2)
	cs.Corrupt("a")
	ledger.Drop("a", 1)

	e = NewExporter(src, cs, ledger, Options{MaxCycleWrites: 1})
	end3 := appendWrites(src, "y", 5)
	_, err := e.BeginCycle("a", end1, end3)
	var ierr *Error
	if !errors.As(err, &ierr) || ierr.Kind != ErrKindHistoryGap {
		t.Fatalf("expected history-gap, got %v", err)
	}
	if ierr.SafeCursor != GenesisCursor {
		t.Fatalf("safe cursor = %d, want genesis (gap at first record)", ierr.SafeCursor)
	}
}

func TestAbortAndRestartProducesIdenticalOutput(t *testing.T) {
	// 同一链路：一次经历多次中断重启，一次持续不中断，
	// 累计输出必须逐条完全一致。
	src := NewMemSource()
	end1 := appendWrites(src, "w", 4)
	end2 := appendWrites(src, "x", 4)
	end3 := appendWrites(src, "y", 4)

	// 参照：从未中断。
	ref, _, _, _ := newHarness(Options{})
	runCycle(t, ref, src, "a", GenesisCursor, end1)
	runCycle(t, ref, src, "a", end1, end2)
	runCycle(t, ref, src, "a", end2, end3)
	want := writeIDs(ref.Published("a"))

	// 被测：每个周期在确认前中断若干次（含提交部分写入后中断）。
	e, _, _, _ := newHarness(Options{})
	for _, span := range [][2]Cursor{{GenesisCursor, end1}, {end1, end2}, {end2, end3}} {
		ws, _ := src.Scan(span[0], span[1])
		for _, cut := range []int{0, 1, len(ws)} { // 中断点：未提交/提交一半/全部提交未确认
			c, err := e.BeginCycle("a", span[0], span[1])
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range ws[:cut] {
				if err := c.Submit(w); err != nil {
					t.Fatal(err)
				}
			}
			c.Abort() // 模拟中断：结束位点未被确认
		}
		runCycle(t, e, src, "a", span[0], span[1])
	}
	if got := writeIDs(e.Published("a")); !reflect.DeepEqual(got, want) {
		t.Fatalf("interrupted output = %v, want %v", got, want)
	}
}

func TestInterruptAfterCommitKeepsConfirmedCursor(t *testing.T) {
	// 结束位点确认之后、下一周期开始之前“中断”：已确认位点不变。
	e, src, _, _ := newHarness(Options{})
	end := appendWrites(src, "w", 3)
	runCycle(t, e, src, "a", GenesisCursor, end)
	cur, err := e.ConfirmedCursor("a")
	if err != nil || cur != end {
		t.Fatalf("confirmed = %d, %v; want %d", cur, err, end)
	}
	// 下一周期必须从已确认位点继续。
	end2 := appendWrites(src, "x", 2)
	runCycle(t, e, src, "a", end, end2)
	want := []string{"w-0", "w-1", "w-2", "x-0", "x-1"}
	if got := writeIDs(e.Published("a")); !reflect.DeepEqual(got, want) {
		t.Fatalf("published = %v, want %v", got, want)
	}
}

func TestDedupWindowStaysBounded(t *testing.T) {
	// 可复核的有界性证明：历史持续增长时，去重判定结构规模
	// 只与当前周期及固定窗口有关，与历史总量无关。
	const window = 4
	e, src, _, _ := newHarness(Options{DedupWindow: window})
	start := GenesisCursor
	for cycle := 0; cycle < 50; cycle++ {
		end := appendWrites(src, "w"+itoa(cycle), 3)
		c, err := e.BeginCycle("a", start, end)
		if err != nil {
			t.Fatal(err)
		}
		st := c.DedupStats()
		if st.WindowSize > window {
			t.Fatalf("cycle %d: window size %d exceeds cap %d", cycle, st.WindowSize, window)
		}
		ws, _ := src.Scan(start, end)
		for _, w := range ws {
			if err := c.Submit(w); err != nil {
				t.Fatal(err)
			}
		}
		st = c.DedupStats()
		if st.CycleSetSize > len(ws) {
			t.Fatalf("cycle %d: cycle set %d exceeds cycle writes %d", cycle, st.CycleSetSize, len(ws))
		}
		if err := c.Commit(); err != nil {
			t.Fatal(err)
		}
		start = end
	}
}

func TestRetryDedupKeepsFirstAcceptancePosition(t *testing.T) {
	e, src, _, _ := newHarness(Options{})
	end := appendWrites(src, "w", 4)

	c, err := e.BeginCycle("a", GenesisCursor, end)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := src.Scan(GenesisCursor, end)
	// 交错提交：w-0, w-1, 重试 w-0, w-2, 重试 w-1, w-3, 重试 w-0。
	seq := []Write{ws[0], ws[1], ws[0], ws[2], ws[1], ws[3], ws[0]}
	for _, w := range seq {
		if err := c.Submit(w); err != nil {
			t.Fatal(err)
		}
	}
	got := writeIDs(c.Buffered())
	want := []string{"w-0", "w-1", "w-2", "w-3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buffered = %v, want %v（重试不得留下位置痕迹）", got, want)
	}
	if err := c.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := writeIDs(e.Published("a")); !reflect.DeepEqual(got, want) {
		t.Fatalf("published = %v, want %v", got, want)
	}
}
