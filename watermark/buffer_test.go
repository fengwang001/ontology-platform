package watermark

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// mustNew 创建测试用缓冲，容量非法时立即失败。
func mustNew(t *testing.T, capacity int) *Buffer {
	t.Helper()
	b, err := NewBuffer(capacity)
	if err != nil {
		t.Fatalf("NewBuffer(%d) unexpected error: %v", capacity, err)
	}
	return b
}

// stateSnapshot 是用于校验“拒绝不改变状态”的只读快照。
type stateSnapshot struct {
	wm       int64
	main     []buffered
	side     []buffered
	pendingN int
}

func snapshot(b *Buffer) stateSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return stateSnapshot{
		wm:       b.wm,
		main:     append([]buffered(nil), b.main...),
		side:     append([]buffered(nil), b.side...),
		pendingN: len(b.pending),
	}
}

// TestStableOrderSameTimestamp 验证相同事件时间的事件按到达序号稳定排序。
func TestStableOrderSameTimestamp(t *testing.T) {
	b := mustNew(t, 10)

	inputs := []Event{
		{ID: "a", Time: 5},
		{ID: "b", Time: 5},
		{ID: "c", Time: 5},
	}
	for _, e := range inputs {
		r := b.Offer(e)
		t.Logf("input=%v decision=%s seq=%d wm=%d released=%v basis=%s",
			e, r.Outcome, r.Seq, r.Watermark, r.Released, decisionBasis(e, r))
		if r.Outcome != OutcomeAccepted || len(r.Released) != 0 {
			t.Fatalf("event %v expected accepted with no release, got %+v", e, r)
		}
	}

	// 更高时间到达：水位线越过 5，三个同时间事件一次性按序号释放。
	r := b.Offer(Event{ID: "d", Time: 8})
	t.Logf("input=%v decision=%s released=%v", Event{ID: "d", Time: 8}, r.Outcome, r.Released)
	wantReleased := []Event{{ID: "a", Time: 5}, {ID: "b", Time: 5}, {ID: "c", Time: 5}}
	if !eventsEqual(r.Released, wantReleased) {
		t.Fatalf("released order = %v, want %v (stable by arrival seq)", r.Released, wantReleased)
	}

	tail := b.Close()
	if !eventsEqual(tail, []Event{{ID: "d", Time: 8}}) {
		t.Fatalf("close tail = %v, want [d@8]", tail)
	}

	entries := b.mainEntries()
	assertOrdered(t, entries)
	if len(entries) != 4 || entries[0].seq != 1 || entries[3].event.ID != "d" {
		t.Fatalf("main entries unexpected: %+v", entries)
	}
	t.Logf("main=%v side=%v", b.MainOutput(), b.SideOutput())
}

// TestLateRoutingSideOutput 验证迟到事件立即进入旁路，且等于水位线不迟到。
func TestLateRoutingSideOutput(t *testing.T) {
	b := mustNew(t, 10)

	r := b.Offer(Event{ID: "a", Time: 10}) // wm -> 10，a 驻留（10 不 < 10）
	if r.Outcome != OutcomeAccepted {
		t.Fatalf("a: %+v", r)
	}
	r = b.Offer(Event{ID: "b", Time: 5}) // 5 < 10：迟到
	t.Logf("input=b@5 decision=%s basis=%s", r.Outcome, decisionBasis(Event{ID: "b", Time: 5}, r))
	if r.Outcome != OutcomeLate || r.Seq != 2 {
		t.Fatalf("b expected late seq=2, got %+v", r)
	}
	r = b.Offer(Event{ID: "c", Time: 10}) // 10 == wm：准时，不迟到
	if r.Outcome != OutcomeAccepted {
		t.Fatalf("c@10 equal to watermark must be accepted, got %+v", r)
	}
	r = b.Offer(Event{ID: "d", Time: 15}) // wm -> 15，释放 a@10、c@10
	if r.Outcome != OutcomeAccepted || !eventsEqual(r.Released,
		[]Event{{ID: "a", Time: 10}, {ID: "c", Time: 10}}) {
		t.Fatalf("d release unexpected: %+v", r)
	}
	tail := b.Close()
	if !eventsEqual(tail, []Event{{ID: "d", Time: 15}}) {
		t.Fatalf("tail = %v", tail)
	}

	side := b.SideOutput()
	if !eventsEqual(side, []Event{{ID: "b", Time: 5}}) {
		t.Fatalf("side output = %v, want [b@5]", side)
	}
	assertOrdered(t, b.mainEntries())
	t.Logf("main=%v side=%v", b.MainOutput(), b.SideOutput())
}

// TestInvalidRejectionsLeaveStateUntouched 覆盖各类拒绝，并验证拒绝不改变
// 水位线、序号、缓冲与两路输出。
func TestInvalidRejectionsLeaveStateUntouched(t *testing.T) {
	if _, err := NewBuffer(0); err != ErrInvalidCapacity {
		t.Fatalf("capacity 0: err=%v, want ErrInvalidCapacity", err)
	}
	if _, err := NewBuffer(-3); err != ErrInvalidCapacity {
		t.Fatalf("capacity -3: err=%v, want ErrInvalidCapacity", err)
	}

	b := mustNew(t, 3)

	cases := []struct {
		name string
		e    Event
		want Outcome
	}{
		{"negative time", Event{ID: "x", Time: -1}, OutcomeInvalidParameter},
		{"empty id", Event{ID: "", Time: 1}, OutcomeEmptyID},
		{"blank id", Event{ID: "   \t", Time: 1}, OutcomeEmptyID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := snapshot(b)
			r := b.Offer(c.e)
			t.Logf("input=%v decision=%s reason=%q", c.e, r.Outcome, r.Reason)
			if r.Outcome != c.want || r.Seq != 0 {
				t.Fatalf("got %+v, want outcome %s seq 0", r, c.want)
			}
			assertSameState(t, before, snapshot(b))
		})
	}

	// 接收一个正常事件作为后续重复判定的基线。
	if r := b.Offer(Event{ID: "a", Time: 1}); r.Outcome != OutcomeAccepted || r.Seq != 1 {
		t.Fatalf("baseline a: %+v", r)
	}

	// 重复标识拒绝，且序号不跳号：下一个被接受事件序号必须为 2。
	before := snapshot(b)
	r := b.Offer(Event{ID: "a", Time: 99})
	if r.Outcome != OutcomeDuplicateID {
		t.Fatalf("duplicate a: %+v", r)
	}
	assertSameState(t, before, snapshot(b))

	r = b.Offer(Event{ID: "b", Time: 1})
	if r.Outcome != OutcomeAccepted || r.Seq != 2 {
		t.Fatalf("after rejection seq must stay contiguous: %+v", r)
	}

	// 容量硬上限：容量 2 时第三个驻留事件被拒，即使它本可推进水位线。
	b2 := mustNew(t, 2)
	if r := b2.Offer(Event{ID: "p", Time: 1}); r.Outcome != OutcomeAccepted {
		t.Fatal(r)
	}
	if r := b2.Offer(Event{ID: "q", Time: 1}); r.Outcome != OutcomeAccepted {
		t.Fatal(r)
	}
	before2 := snapshot(b2)
	r = b2.Offer(Event{ID: "r", Time: 2})
	t.Logf("input=r@2 decision=%s reason=%q", r.Outcome, r.Reason)
	if r.Outcome != OutcomeBufferFull || r.Seq != 0 {
		t.Fatalf("expected BUFFER_FULL without seq, got %+v", r)
	}
	assertSameState(t, before2, snapshot(b2))
	tail := b2.Close()
	if !eventsEqual(tail, []Event{{ID: "p", Time: 1}, {ID: "q", Time: 1}}) {
		t.Fatalf("tail after full = %v", tail)
	}

	// 关闭后一律拒绝，重复 Close 不再产生输出。
	r = b.Offer(Event{ID: "z", Time: 1})
	_ = b.Close()
	after := snapshot(b)
	r = b.Offer(Event{ID: "z", Time: 1})
	if r.Outcome != OutcomeClosed {
		t.Fatalf("offer after close: %+v", r)
	}
	if tail := b.Close(); tail != nil {
		t.Fatalf("second close must return nil, got %v", tail)
	}
	assertSameState(t, after, snapshot(b))
}

// TestDuplicateIDAcrossOutputs 验证进入主路与旁路的标识都登记去重。
func TestDuplicateIDAcrossOutputs(t *testing.T) {
	b := mustNew(t, 10)
	b.Offer(Event{ID: "a", Time: 5})
	b.Offer(Event{ID: "late1", Time: 1}) // wm=5 → 迟到旁路

	if r := b.Offer(Event{ID: "late1", Time: 1}); r.Outcome != OutcomeDuplicateID {
		t.Fatalf("side-event duplicate: %+v", r)
	}
	b.Offer(Event{ID: "b", Time: 9}) // wm=9，释放 a@5 到主输出
	if r := b.Offer(Event{ID: "a", Time: 0}); r.Outcome != OutcomeDuplicateID {
		t.Fatalf("main-event duplicate must still be rejected: %+v", r)
	}
	if r := b.Offer(Event{ID: "c", Time: 8}); r.Outcome != OutcomeLate {
		t.Fatalf("c@8 after wm=9 must be late, got %+v", r)
	}
}

// TestNoLossNoDuplicateAndOrder 用乱序序列验证：主∪旁路=输入（不丢不重）、
// 主输出严格按 (时间, 序号) 有序、序号 1..K 连续。
func TestNoLossNoDuplicateAndOrder(t *testing.T) {
	b := mustNew(t, 4)
	inputs := []Event{
		{ID: "e1", Time: 3},
		{ID: "e2", Time: 7},
		{ID: "e3", Time: 1},
		{ID: "e4", Time: 7},
		{ID: "e5", Time: 2},
		{ID: "e6", Time: 9},
		{ID: "e7", Time: 6},
		{ID: "e8", Time: 7},
	}
	for _, e := range inputs {
		r := b.Offer(e)
		t.Logf("input=%v decision=%s seq=%d wm=%d released=%v basis=%s",
			e, r.Outcome, r.Seq, r.Watermark, r.Released, decisionBasis(e, r))
		if r.Outcome != OutcomeAccepted && r.Outcome != OutcomeLate {
			t.Fatalf("unexpected rejection for %v: %+v", e, r)
		}
	}
	tail := b.Close()
	t.Logf("tail=%v", tail)

	main := b.mainEntries()
	side := b.sideEntries()
	assertOrdered(t, main)

	// 并集 = 输入集合；无重复。
	seen := map[string]int{}
	for _, be := range main {
		seen[be.event.ID]++
	}
	for _, be := range side {
		seen[be.event.ID]++
	}
	if len(seen) != len(inputs) {
		t.Fatalf("union size %d != input size %d (loss or duplicate)", len(seen), len(inputs))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("event %s appeared %d times", id, n)
		}
	}

	// 序号 1..N 连续且唯一。
	allSeq := map[int64]bool{}
	for _, be := range append(append([]buffered{}, main...), side...) {
		if allSeq[be.seq] {
			t.Fatalf("duplicate seq %d", be.seq)
		}
		allSeq[be.seq] = true
	}
	for i := int64(1); i <= int64(len(inputs)); i++ {
		if !allSeq[i] {
			t.Fatalf("missing seq %d", i)
		}
	}
	t.Logf("main=%v side=%v", b.MainOutput(), b.SideOutput())
}

// TestDeterminism 验证同一输入序列反复计算得到完全相同的输出。
func TestDeterminism(t *testing.T) {
	inputs := []Event{
		{ID: "a", Time: 4}, {ID: "b", Time: 2}, {ID: "c", Time: 8},
		{ID: "d", Time: 2}, {ID: "e", Time: 1}, {ID: "f", Time: 6},
		{ID: "g", Time: 8}, {ID: "h", Time: 3},
	}

	run := func() (int64, []Event, []Event) {
		b, _ := NewBuffer(5)
		for _, e := range inputs {
			b.Offer(e)
		}
		tail := b.Close()
		main := b.MainOutput()
		if !eventsEqual(tail, main[len(main)-len(tail):]) {
			t.Fatalf("tail must be the suffix of main output")
		}
		return b.Watermark(), main, b.SideOutput()
	}

	wm0, main0, side0 := run()
	for i := 0; i < 5; i++ {
		wm, main, side := run()
		if wm != wm0 || !eventsEqual(main, main0) || !eventsEqual(side, side0) {
			t.Fatalf("run %d differs:\nwm=%d/%d\nmain=%v/%v\nside=%v/%v",
				i, wm, wm0, main, main0, side, side0)
		}
	}
}

// TestWatermarkMonotonic 验证水位线随最大事件时间单调不减。
func TestWatermarkMonotonic(t *testing.T) {
	b := mustNew(t, 10)
	prev := int64(0)
	for _, e := range []Event{
		{ID: "a", Time: 5}, {ID: "b", Time: 3}, {ID: "c", Time: 10},
		{ID: "d", Time: 2}, {ID: "e", Time: 10}, {ID: "f", Time: 7},
	} {
		r := b.Offer(e)
		if r.Watermark < prev {
			t.Fatalf("watermark went backwards: %d -> %d", prev, r.Watermark)
		}
		prev = r.Watermark
	}
	if got := b.Watermark(); got != 10 {
		t.Fatalf("watermark = %d, want 10", got)
	}
}

// TestConcurrentSameTimestamp 并发提交互不重复的同时间事件：全部准时，
// 验证恰好一次、序号连续、主输出严格有序。
func TestConcurrentSameTimestamp(t *testing.T) {
	const goroutines, perG = 8, 50
	b := mustNew(t, goroutines*perG)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				r := b.Offer(Event{ID: idFor(g, i), Time: 1})
				if r.Outcome != OutcomeAccepted {
					t.Errorf("unexpected outcome %s for g%di%d", r.Outcome, g, i)
				}
			}
		}(g)
	}
	wg.Wait()
	b.Close()

	main := b.mainEntries()
	if len(main) != goroutines*perG {
		t.Fatalf("main size = %d, want %d", len(main), goroutines*perG)
	}
	assertOrdered(t, main)
	seqs := map[int64]string{}
	for _, be := range main {
		if _, dup := seqs[be.seq]; dup {
			t.Fatalf("duplicate seq %d", be.seq)
		}
		seqs[be.seq] = be.event.ID
	}
	for i := int64(1); i <= int64(goroutines*perG); i++ {
		if _, ok := seqs[i]; !ok {
			t.Fatalf("missing seq %d", i)
		}
	}
	if len(b.SideOutput()) != 0 {
		t.Fatalf("same-timestamp events must never be late")
	}
}

// TestConcurrentMixedInvariants 并发提交乱序时间事件，不断言归属，
// 只验证全局不变量：恰好一次、主输出有序、序号连续、水位线单调记录。
func TestConcurrentMixedInvariants(t *testing.T) {
	const goroutines, perG = 8, 40
	b := mustNew(t, goroutines*perG)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				// 时间在 [1,12) 内乱序，制造交错下的迟到/准时分歧。
				e := Event{ID: idFor(g, i), Time: int64(1 + ((g*7 + i*3) % 12))}
				r := b.Offer(e)
				if r.Outcome != OutcomeAccepted && r.Outcome != OutcomeLate {
					t.Errorf("unexpected rejection: %+v", r)
				}
			}
		}(g)
	}
	wg.Wait()
	b.Close()

	main := b.mainEntries()
	side := b.sideEntries()
	assertOrdered(t, main)

	ids := map[string]int{}
	for _, be := range main {
		ids[be.event.ID]++
	}
	for _, be := range side {
		ids[be.event.ID]++
	}
	if len(ids) != goroutines*perG {
		t.Fatalf("unique accepted ids = %d, want %d", len(ids), goroutines*perG)
	}
	for id, n := range ids {
		if n != 1 {
			t.Fatalf("%s appeared %d times", id, n)
		}
	}
	all := append(append([]buffered{}, main...), side...)
	seqs := map[int64]bool{}
	for _, be := range all {
		if seqs[be.seq] {
			t.Fatalf("dup seq %d", be.seq)
		}
		seqs[be.seq] = true
	}
	if len(seqs) != goroutines*perG {
		t.Fatalf("distinct seq = %d, want %d", len(seqs), goroutines*perG)
	}
}

// TestLoggerOutput 验证日志包含输入、判定依据与两路输出。
func TestLoggerOutput(t *testing.T) {
	var buf bytes.Buffer
	b, _ := NewBuffer(10)
	l := NewLogger(b, &buf)

	l.Offer(Event{ID: "a", Time: 5})
	l.Offer(Event{ID: "b", Time: 9})
	l.Offer(Event{ID: "c", Time: 2}) // 迟到
	l.Close()

	log := buf.String()
	t.Logf("captured log:\n%s", log)
	for _, want := range []string{
		`input  id="a" time=5`,
		`input  id="c" time=2`,
		"decide ACCEPTED",
		"decide LATE",
		"basis=",
		"output main=",
		"output side=",
		"a@5",
		"c@2",
		"close",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q", want)
		}
	}
}

// --- helpers ---

func idFor(g, i int) string {
	return "g" + itoa(g) + "-" + itoa(i)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(b[pos:])
}

func eventsEqual(a, b []Event) bool {
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

// sideEntries 返回旁路条目副本（包内校验用）。
func (b *Buffer) sideEntries() []buffered {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]buffered(nil), b.side...)
}

// assertOrdered 断言主输出严格按 (Time, Seq) 升序。
func assertOrdered(t *testing.T, entries []buffered) {
	t.Helper()
	for i := 1; i < len(entries); i++ {
		p, c := entries[i-1], entries[i]
		if p.event.Time > c.event.Time ||
			(p.event.Time == c.event.Time && p.seq >= c.seq) {
			t.Fatalf("main output not strictly ordered at %d: %+v then %+v", i, p, c)
		}
	}
}

func assertSameState(t *testing.T, want, got stateSnapshot) {
	t.Helper()
	if want.wm != got.wm || want.pendingN != got.pendingN ||
		!bufferedEqual(want.main, got.main) || !bufferedEqual(want.side, got.side) {
		t.Fatalf("state changed by rejected operation:\nbefore=%+v\nafter =%+v", want, got)
	}
}

func bufferedEqual(a, b []buffered) bool {
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

// decisionBasis 复刻 Logger 的判定依据说明，供测试日志直接展示。
func decisionBasis(e Event, r OfferResult) string {
	switch r.Outcome {
	case OutcomeAccepted:
		return "accepted; buffered or flushed per watermark"
	case OutcomeLate:
		return "event time below watermark => side output"
	default:
		return r.Reason
	}
}

// TestMiscBranches 覆盖空缓冲关闭、结果类别命名与 Logger 访问器等边界。
func TestMiscBranches(t *testing.T) {
	// 空缓冲直接 Close 不产生输出，且可重复。
	b := mustNew(t, 2)
	if tail := b.Close(); tail != nil {
		t.Fatalf("close empty buffer = %v, want nil", tail)
	}
	if tail := b.Close(); tail != nil {
		t.Fatalf("second close = %v, want nil", tail)
	}

	// 所有判定类别均有稳定名称；未知类别落到 UNKNOWN。
	names := map[Outcome]string{
		OutcomeInvalidParameter: "INVALID_PARAMETER",
		OutcomeEmptyID:          "EMPTY_ID",
		OutcomeDuplicateID:      "DUPLICATE_ID",
		OutcomeBufferFull:       "BUFFER_FULL",
		OutcomeAccepted:         "ACCEPTED",
		OutcomeLate:             "LATE",
		OutcomeClosed:           "CLOSED",
		Outcome(999):            "UNKNOWN",
	}
	for o, want := range names {
		if got := o.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", o, got, want)
		}
	}

	// nil writer 回退到标准错误；Buffer() 返回被包装实例。
	b2 := mustNew(t, 2)
	l := NewLogger(b2, nil)
	if l.Buffer() != b2 {
		t.Fatalf("Buffer() accessor mismatch")
	}
	r := l.Offer(Event{ID: "x", Time: 1})
	if r.Outcome != OutcomeAccepted {
		t.Fatalf("offer via nil-writer logger: %+v", r)
	}
}
