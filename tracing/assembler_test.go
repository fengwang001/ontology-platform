package tracing

import (
	"reflect"
	"sync"
	"testing"
)

// fakeClock 为并发安全的注入时钟。
type fakeClock struct {
	mu  sync.Mutex
	now int64
}

func (c *fakeClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
}

func newAssembler(t *testing.T, clk *fakeClock, silence int64, maxBuffered int) *Assembler {
	t.Helper()
	return NewAssembler(Config{
		SilenceTimeout:   silence,
		MaxBufferedSpans: maxBuffered,
		Clock:            clk,
		Logf:             func(format string, args ...any) { t.Logf(format, args...) },
	})
}

// runTrace 按给定顺序投递全部跨度，推进时钟使其完结，返回唯一完结结果。
func runTrace(t *testing.T, spans []Span, silence int64) TraceResult {
	t.Helper()
	clk := &fakeClock{}
	asm := newAssembler(t, clk, silence, 0)
	for _, s := range spans {
		t.Logf("input %+v", s)
		if r := asm.Submit(s); !r.Accepted {
			t.Fatalf("span %+v rejected: %s", s, r.Reason)
		}
		st := asm.Stats()
		if st.AcceptedTotal != st.CompletedSpans+st.BufferedSpans {
			t.Fatalf("invariant broken: %+v", st)
		}
	}
	clk.Advance(silence)
	res := asm.Poll()
	if len(res) != 1 {
		t.Fatalf("expected 1 completed trace, got %d", len(res))
	}
	t.Logf("output %+v", res[0])
	st := asm.Stats()
	if st.AcceptedTotal != st.CompletedSpans+st.BufferedSpans {
		t.Fatalf("invariant broken after finalize: %+v", st)
	}
	return res[0]
}

func shiftMap(res TraceResult) map[string]int64 {
	m := make(map[string]int64, len(res.Spans))
	for _, ps := range res.Spans {
		m[ps.Span.SpanID] = ps.Shift
	}
	return m
}

func orphanIDs(res TraceResult) []string {
	ids := make([]string, 0, len(res.Orphans))
	for _, s := range res.Orphans {
		ids = append(ids, s.SpanID)
	}
	return ids
}

// TestChildBeforeParent 子先于父到达，完结时仍组装为同一棵树。
func TestChildBeforeParent(t *testing.T) {
	spans := []Span{
		{TraceID: "t1", SpanID: "c", ParentID: "r", Service: "B", Start: 110, End: 150},
		{TraceID: "t1", SpanID: "r", ParentID: "", Service: "A", Start: 100, End: 200},
	}
	res := runTrace(t, spans, 1000)
	if len(res.Spans) != 2 || len(res.Orphans) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := shiftMap(res); got["c"] != 0 || got["r"] != 0 {
		t.Fatalf("unexpected shifts: %v", got)
	}
	want := []string{"r", "c"}
	if !reflect.DeepEqual(res.CriticalPath, want) {
		t.Fatalf("critical path = %v, want %v", res.CriticalPath, want)
	}
}

// TestChildEntirelyBeforeParent 子区间整体早于父：向右平移最小量使子落入父区间，
// 同服务后代随之整体平移。
func TestChildEntirelyBeforeParent(t *testing.T) {
	spans := []Span{
		{TraceID: "t1", SpanID: "r", ParentID: "", Service: "A", Start: 100, End: 200},
		{TraceID: "t1", SpanID: "c", ParentID: "r", Service: "B", Start: 0, End: 50},
		{TraceID: "t1", SpanID: "g", ParentID: "c", Service: "B", Start: 10, End: 40},
	}
	res := runTrace(t, spans, 1000)
	got := shiftMap(res)
	want := map[string]int64{"r": 0, "c": 100, "g": 100}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shifts = %v, want %v", got, want)
	}
	// 平移后 c=[100,150]、g=[110,140] 均落入父区间。
	if path := res.CriticalPath; !reflect.DeepEqual(path, []string{"r", "c", "g"}) {
		t.Fatalf("critical path = %v", path)
	}
}

// TestChildLongerThanParent 子比父长：改为起点对齐。
func TestChildLongerThanParent(t *testing.T) {
	spans := []Span{
		{TraceID: "t1", SpanID: "r", ParentID: "", Service: "A", Start: 0, End: 50},
		{TraceID: "t1", SpanID: "c", ParentID: "r", Service: "B", Start: 100, End: 300},
	}
	res := runTrace(t, spans, 1000)
	got := shiftMap(res)
	// 子时长 200 > 父时长 50，起点对齐：delta = 0-100 = -100，子变为 [0,200]。
	want := map[string]int64{"r": 0, "c": -100}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shifts = %v, want %v", got, want)
	}
}

// TestThreeLevelCrossServiceChain 三层跨服务链：自根向下逐层重判，
// 异服务后代相对已平移的父重新计算平移量。
func TestThreeLevelCrossServiceChain(t *testing.T) {
	spans := []Span{
		{TraceID: "t1", SpanID: "r", ParentID: "", Service: "S1", Start: 0, End: 100},
		{TraceID: "t1", SpanID: "a", ParentID: "r", Service: "S2", Start: 200, End: 250},
		{TraceID: "t1", SpanID: "b", ParentID: "a", Service: "S2", Start: 210, End: 240},
		{TraceID: "t1", SpanID: "c", ParentID: "b", Service: "S3", Start: 500, End: 560},
	}
	res := runTrace(t, spans, 1000)
	got := shiftMap(res)
	// a 整体晚于 r：delta = 100-250 = -150，a=[50,100]，同服务的 b 随之平移为 [60,90]。
	// c 相对已平移的 b=[60,90] 重判：c 时长 60 > b 时长 30，起点对齐 delta = 60-500 = -440。
	want := map[string]int64{"r": 0, "a": -150, "b": -150, "c": -440}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shifts = %v, want %v", got, want)
	}
	// 平移后结束时刻：r=100, a=100, b=90, c=120，每层唯一子跨度。
	if path := res.CriticalPath; !reflect.DeepEqual(path, []string{"r", "a", "b", "c"}) {
		t.Fatalf("critical path = %v", path)
	}
}

// TestCycleBecomesOrphan 父子成环及挂到环上的跨度均列为孤儿。
func TestCycleBecomesOrphan(t *testing.T) {
	spans := []Span{
		{TraceID: "t1", SpanID: "r", ParentID: "", Service: "A", Start: 0, End: 100},
		{TraceID: "t1", SpanID: "ok", ParentID: "r", Service: "A", Start: 10, End: 20},
		{TraceID: "t1", SpanID: "x", ParentID: "y", Service: "B", Start: 10, End: 20},
		{TraceID: "t1", SpanID: "y", ParentID: "x", Service: "B", Start: 10, End: 20},
		{TraceID: "t1", SpanID: "z", ParentID: "x", Service: "C", Start: 10, End: 20},
		{TraceID: "t1", SpanID: "m", ParentID: "missing", Service: "D", Start: 10, End: 20},
	}
	res := runTrace(t, spans, 1000)
	if len(res.Spans) != 2 {
		t.Fatalf("tree spans = %+v, want r and ok", res.Spans)
	}
	wantOrphans := []string{"m", "x", "y", "z"}
	if got := orphanIDs(res); !reflect.DeepEqual(got, wantOrphans) {
		t.Fatalf("orphans = %v, want %v", got, wantOrphans)
	}
	if path := res.CriticalPath; !reflect.DeepEqual(path, []string{"r", "ok"}) {
		t.Fatalf("critical path = %v", path)
	}
}

// TestRejectReasons 各类非法跨度被以可区分的原因拒绝，且不改变任何状态。
func TestRejectReasons(t *testing.T) {
	clk := &fakeClock{}
	asm := newAssembler(t, clk, 1000, 0)
	root := Span{TraceID: "t1", SpanID: "r", ParentID: "", Service: "A", Start: 0, End: 100}
	if r := asm.Submit(root); !r.Accepted || r.Duplicate {
		t.Fatalf("root not accepted: %+v", r)
	}

	cases := []struct {
		name string
		span Span
		want RejectReason
	}{
		{"end-before-start", Span{TraceID: "t1", SpanID: "s1", ParentID: "r", Service: "A", Start: 10, End: 5}, RejectEndBeforeStart},
		{"self-parent", Span{TraceID: "t1", SpanID: "s2", ParentID: "s2", Service: "A", Start: 0, End: 5}, RejectSelfParent},
		{"second-root", Span{TraceID: "t1", SpanID: "r2", ParentID: "", Service: "A", Start: 0, End: 5}, RejectDuplicateRoot},
		{"conflicting-dup", Span{TraceID: "t1", SpanID: "r", ParentID: "", Service: "A", Start: 0, End: 99}, RejectConflictingDuplicate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := asm.Stats()
			r := asm.Submit(tc.span)
			t.Logf("input %+v -> rejected reason=%s", tc.span, r.Reason)
			if r.Accepted || r.Reason != tc.want {
				t.Fatalf("got %+v, want reason %s", r, tc.want)
			}
			after := asm.Stats()
			if after.AcceptedTotal != before.AcceptedTotal || after.BufferedSpans != before.BufferedSpans {
				t.Fatalf("rejected span changed state: before=%+v after=%+v", before, after)
			}
		})
	}

	// 内容相同的重复为幂等：接受但不改变状态。
	before := asm.Stats()
	if r := asm.Submit(root); !r.Accepted || !r.Duplicate {
		t.Fatalf("idempotent duplicate = %+v", r)
	}
	if after := asm.Stats(); after != before {
		t.Fatalf("idempotent duplicate changed stats: before=%+v after=%+v", before, after)
	}
}

// TestLateAfterCompletion 完结后到达的跨度单独拒绝并计数。
func TestLateAfterCompletion(t *testing.T) {
	clk := &fakeClock{}
	asm := newAssembler(t, clk, 100, 0)
	root := Span{TraceID: "t1", SpanID: "r", ParentID: "", Service: "A", Start: 0, End: 100}
	asm.Submit(root)
	clk.Advance(100)
	if res := asm.Poll(); len(res) != 1 {
		t.Fatalf("expected completion, got %d", len(res))
	}

	late := Span{TraceID: "t1", SpanID: "late", ParentID: "r", Service: "B", Start: 10, End: 20}
	r := asm.Submit(late)
	t.Logf("late input %+v -> rejected reason=%s", late, r.Reason)
	if r.Accepted || r.Reason != RejectTraceCompleted {
		t.Fatalf("late span = %+v, want %s", r, RejectTraceCompleted)
	}
	// 已完结追踪的幂等重投同样按迟到拒绝。
	if r := asm.Submit(root); r.Reason != RejectTraceCompleted {
		t.Fatalf("resubmitted root = %+v, want %s", r, RejectTraceCompleted)
	}
	st := asm.Stats()
	if st.LateRejected != 2 || st.RejectedTotal != 2 {
		t.Fatalf("stats = %+v, want LateRejected=2 RejectedTotal=2", st)
	}
	if st.AcceptedTotal != 1 || st.CompletedSpans != 1 || st.BufferedSpans != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

// TestBufferLimit 会使未完结追踪跨度总数超过上限的新跨度被拒绝，且不改变状态。
func TestBufferLimit(t *testing.T) {
	clk := &fakeClock{}
	asm := newAssembler(t, clk, 1000, 2)
	s1 := Span{TraceID: "t1", SpanID: "r", ParentID: "", Service: "A", Start: 0, End: 100}
	s2 := Span{TraceID: "t2", SpanID: "r", ParentID: "", Service: "A", Start: 0, End: 100}
	s3 := Span{TraceID: "t1", SpanID: "c", ParentID: "r", Service: "B", Start: 10, End: 20}
	if r := asm.Submit(s1); !r.Accepted {
		t.Fatalf("s1 = %+v", r)
	}
	if r := asm.Submit(s2); !r.Accepted {
		t.Fatalf("s2 = %+v", r)
	}
	before := asm.Stats()
	r := asm.Submit(s3)
	t.Logf("over-limit input %+v -> rejected reason=%s", s3, r.Reason)
	if r.Accepted || r.Reason != RejectBufferFull {
		t.Fatalf("s3 = %+v, want %s", r, RejectBufferFull)
	}
	if after := asm.Stats(); after.BufferedSpans != before.BufferedSpans || after.AcceptedTotal != before.AcceptedTotal {
		t.Fatalf("rejected span changed state: before=%+v after=%+v", before, after)
	}
	// 完结释放后可以继续接受。
	clk.Advance(1000)
	if res := asm.Poll(); len(res) != 2 {
		t.Fatalf("expected 2 completions, got %d", len(res))
	}
	s4 := Span{TraceID: "t3", SpanID: "r", ParentID: "", Service: "A", Start: 0, End: 100}
	if r := asm.Submit(s4); !r.Accepted {
		t.Fatalf("s4 after release = %+v", r)
	}
	st := asm.Stats()
	if st.AcceptedTotal != st.CompletedSpans+st.BufferedSpans {
		t.Fatalf("invariant broken: %+v", st)
	}
}

// permutations 返回 items 的全部排列。
func permutations(items []Span) [][]Span {
	var out [][]Span
	var rec func(k int)
	rec = func(k int) {
		if k == len(items) {
			cp := make([]Span, len(items))
			copy(cp, items)
			out = append(out, cp)
			return
		}
		for i := k; i < len(items); i++ {
			items[k], items[i] = items[i], items[k]
			rec(k + 1)
			items[k], items[i] = items[i], items[k]
		}
	}
	rec(0)
	return out
}

// permutationTrace 为全排列测试用的跨度集合：含跨服务平移、同服务后代、
// 子比父长与孤儿，覆盖各种判定路径。
func permutationTrace() []Span {
	return []Span{
		{TraceID: "t1", SpanID: "1", ParentID: "", Service: "A", Start: 100, End: 200},
		{TraceID: "t1", SpanID: "2", ParentID: "1", Service: "B", Start: 50, End: 120},
		{TraceID: "t1", SpanID: "3", ParentID: "2", Service: "B", Start: 60, End: 110},
		{TraceID: "t1", SpanID: "4", ParentID: "2", Service: "C", Start: 300, End: 400},
		{TraceID: "t1", SpanID: "5", ParentID: "1", Service: "A", Start: 150, End: 180},
		{TraceID: "t1", SpanID: "9", ParentID: "missing", Service: "D", Start: 10, End: 20},
	}
}

// TestAllPermutations 同一跨度集合按任意顺序到达，完结输出完全相同。
func TestAllPermutations(t *testing.T) {
	base := permutationTrace()
	perms := permutations(base)
	t.Logf("permutations: %d", len(perms))

	var want TraceResult
	for i, order := range perms {
		clk := &fakeClock{}
		asm := newAssembler(t, clk, 1000, 0)
		for _, s := range order {
			if r := asm.Submit(s); !r.Accepted {
				t.Fatalf("perm %d: span %+v rejected: %s", i, s, r.Reason)
			}
		}
		clk.Advance(1000)
		res := asm.Poll()
		if len(res) != 1 {
			t.Fatalf("perm %d: got %d completions", i, len(res))
		}
		if i == 0 {
			want = res[0]
			t.Logf("baseline output: %+v", want)
			continue
		}
		if !reflect.DeepEqual(res[0], want) {
			t.Fatalf("perm %d output differs:\n got %+v\nwant %+v", i, res[0], want)
		}
	}

	// 校验基线输出的关键内容：平移量、孤儿与关键路径。
	wantShifts := map[string]int64{"1": 0, "2": 50, "3": 50, "4": -200, "5": 0}
	if got := shiftMap(want); !reflect.DeepEqual(got, wantShifts) {
		t.Fatalf("baseline shifts = %v, want %v", got, wantShifts)
	}
	if got := orphanIDs(want); !reflect.DeepEqual(got, []string{"9"}) {
		t.Fatalf("baseline orphans = %v", got)
	}
	// 根 "1" 的子跨度：2 平移后结束于 170，5 结束于 180，选 5。
	if !reflect.DeepEqual(want.CriticalPath, []string{"1", "5"}) {
		t.Fatalf("baseline critical path = %v", want.CriticalPath)
	}
}

// TestIdempotentDuplicateOutput 混入幂等重复不改变完结输出。
func TestIdempotentDuplicateOutput(t *testing.T) {
	spans := permutationTrace()
	want := runTrace(t, spans, 1000)

	clk := &fakeClock{}
	asm := newAssembler(t, clk, 1000, 0)
	for _, s := range spans {
		asm.Submit(s)
		r := asm.Submit(s) // 立即重复投递
		if !r.Accepted || !r.Duplicate {
			t.Fatalf("duplicate of %+v = %+v", s, r)
		}
	}
	clk.Advance(1000)
	res := asm.Poll()
	if len(res) != 1 || !reflect.DeepEqual(res[0], want) {
		t.Fatalf("output with duplicates differs:\n got %+v\nwant %+v", res, want)
	}
	if st := asm.Stats(); st.AcceptedTotal != len(spans) {
		t.Fatalf("AcceptedTotal = %d, want %d", st.AcceptedTotal, len(spans))
	}
}

// TestConcurrentSubmitAndClock 跨度投递与时钟推进并发执行，不变量始终成立。
func TestConcurrentSubmitAndClock(t *testing.T) {
	clk := &fakeClock{}
	asm := newAssembler(t, clk, 50, 0)

	const traces = 8
	const spansPerTrace = 6
	var wg sync.WaitGroup
	for ti := 0; ti < traces; ti++ {
		wg.Add(1)
		go func(ti int) {
			defer wg.Done()
			id := string(rune('a' + ti))
			asm.Submit(Span{TraceID: id, SpanID: "r", ParentID: "", Service: "A", Start: 0, End: 100})
			for si := 0; si < spansPerTrace-1; si++ {
				asm.Submit(Span{
					TraceID: id,
					SpanID:  "s" + string(rune('0'+si)),
					ParentID: func() string {
						if si == 0 {
							return "r"
						}
						return "s" + string(rune('0'+si-1))
					}(),
					Service: "B",
					Start:   int64(10 * (si + 1)),
					End:     int64(10*(si+1) + 5),
				})
			}
		}(ti)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			clk.Advance(1)
			asm.Poll()
			st := asm.Stats()
			if st.AcceptedTotal != st.CompletedSpans+st.BufferedSpans {
				t.Errorf("invariant broken: %+v", st)
				return
			}
		}
	}()
	wg.Wait()
	<-done

	st := asm.Stats()
	t.Logf("final stats: %+v", st)
	if st.CompletedTraces != traces {
		t.Fatalf("CompletedTraces = %d, want %d", st.CompletedTraces, traces)
	}
	if st.AcceptedTotal != traces*spansPerTrace || st.BufferedSpans != 0 {
		t.Fatalf("stats = %+v", st)
	}
	if st.AcceptedTotal != st.CompletedSpans+st.BufferedSpans {
		t.Fatalf("invariant broken: %+v", st)
	}
}
