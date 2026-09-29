package tracing_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/tracing"
)

func sp(traceID, id, parent, svc string, start, end int64) tracing.Span {
	return tracing.Span{TraceID: traceID, SpanID: id, ParentID: parent, Service: svc, Start: start, End: end}
}

func rejectReason(t *testing.T, err error) tracing.RejectReason {
	t.Helper()
	var re *tracing.RejectError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RejectError, got %v", err)
	}
	return re.Reason
}

// 完结并断言只产出一个结果。
func finalizeOne(t *testing.T, a *tracing.Assembler, now int64) tracing.TraceResult {
	t.Helper()
	results := a.AdvanceClock(now)
	if len(results) != 1 {
		t.Fatalf("expected 1 finalized trace, got %d: %+v", len(results), results)
	}
	t.Logf("finalized output: %+v", results[0])
	return results[0]
}

func offsetsOf(res tracing.TraceResult) map[string]int64 {
	out := make(map[string]int64, len(res.Spans))
	for _, s := range res.Spans {
		out[s.SpanID] = s.Offset
	}
	return out
}

func TestRejectReasons(t *testing.T) {
	a := tracing.NewAssembler(10, 0)

	cases := []struct {
		name string
		span tracing.Span
		want tracing.RejectReason
	}{
		{"结束早于开始", sp("t", "bad1", "", "svcA", 10, 5), tracing.ReasonInvalidInterval},
		{"以自己为父", sp("t", "bad2", "bad2", "svcA", 0, 5), tracing.ReasonSelfParent},
	}
	for _, tc := range cases {
		before := a.AcceptedCount()
		err := a.AddSpan(tc.span)
		got := rejectReason(t, err)
		t.Logf("input=%+v reason=%s", tc.span, got)
		if got != tc.want {
			t.Fatalf("%s: got reason %s, want %s", tc.name, got, tc.want)
		}
		if a.AcceptedCount() != before {
			t.Fatalf("%s: rejected span changed state", tc.name)
		}
	}

	// 第二个根被拒绝。
	if err := a.AddSpan(sp("t", "root1", "", "svcA", 0, 100)); err != nil {
		t.Fatal(err)
	}
	if got := rejectReason(t, a.AddSpan(sp("t", "root2", "", "svcA", 0, 50))); got != tracing.ReasonDuplicateRoot {
		t.Fatalf("duplicate root: got %s", got)
	}
	// 内容不同的同号重复被拒绝，内容相同的重复幂等。
	if err := a.AddSpan(sp("t", "c1", "root1", "svcA", 1, 2)); err != nil {
		t.Fatal(err)
	}
	if got := rejectReason(t, a.AddSpan(sp("t", "c1", "root1", "svcB", 1, 2))); got != tracing.ReasonConflictingDuplicate {
		t.Fatalf("conflicting duplicate: got %s", got)
	}
	if err := a.AddSpan(sp("t", "c1", "root1", "svcA", 1, 2)); err != nil {
		t.Fatalf("idempotent duplicate should succeed: %v", err)
	}
	if a.AcceptedCount() != 2 {
		t.Fatalf("accepted count = %d, want 2 (rejections must not change state)", a.AcceptedCount())
	}
}

func TestChildBeforeParent(t *testing.T) {
	a := tracing.NewAssembler(10, 0)
	// 子先于父到达。
	for _, s := range []tracing.Span{
		sp("t1", "c", "r", "svcA", 10, 20),
		sp("t1", "r", "", "svcA", 0, 100),
	} {
		t.Logf("input: %+v", s)
		if err := a.AddSpan(s); err != nil {
			t.Fatal(err)
		}
	}
	res := finalizeOne(t, a, 1000)
	if len(res.Orphans) != 0 {
		t.Fatalf("unexpected orphans: %v", res.Orphans)
	}
	if !reflect.DeepEqual(res.CriticalPath, []string{"r", "c"}) {
		t.Fatalf("critical path = %v", res.CriticalPath)
	}
}

func TestChildEntirelyBeforeParent(t *testing.T) {
	a := tracing.NewAssembler(10, 0)
	// 子区间整体早于父：整体后移，平移量取最小绝对值。
	for _, s := range []tracing.Span{
		sp("t1", "r", "", "svcA", 100, 200),
		sp("t1", "c", "r", "svcB", 0, 10),
	} {
		if err := a.AddSpan(s); err != nil {
			t.Fatal(err)
		}
	}
	res := finalizeOne(t, a, 1000)
	got := offsetsOf(res)["c"]
	t.Logf("child offset=%d (判定依据: 子[0,10]整体早于父[100,200], 最小平移=100-0=+100)", got)
	if got != 100 {
		t.Fatalf("child offset = %d, want 100", got)
	}
	if !reflect.DeepEqual(res.CriticalPath, []string{"r", "c"}) {
		t.Fatalf("critical path = %v", res.CriticalPath)
	}
}

func TestChildLongerThanParent(t *testing.T) {
	a := tracing.NewAssembler(10, 0)
	// 子比父长：改为起点对齐。
	for _, s := range []tracing.Span{
		sp("t1", "r", "", "svcA", 0, 10),
		sp("t1", "c", "r", "svcB", 100, 200),
	} {
		if err := a.AddSpan(s); err != nil {
			t.Fatal(err)
		}
	}
	res := finalizeOne(t, a, 1000)
	got := offsetsOf(res)["c"]
	t.Logf("child offset=%d (判定依据: 子长100>父长10, 起点对齐 0-100=-100)", got)
	if got != -100 {
		t.Fatalf("child offset = %d, want -100", got)
	}
}

func TestThreeLayerCrossServiceChain(t *testing.T) {
	a := tracing.NewAssembler(10, 0)
	// 三层跨服务链，逐层相对已平移的父重新判定。
	spans := []tracing.Span{
		sp("t1", "a", "", "svcA", 0, 100),
		sp("t1", "b", "a", "svcB", 150, 160),
		sp("t1", "c", "b", "svcC", 170, 180),
		sp("t1", "d", "c", "svcC", 172, 182), // 与父同服务：随父整体平移，不重判
	}
	for _, s := range spans {
		t.Logf("input: %+v", s)
		if err := a.AddSpan(s); err != nil {
			t.Fatal(err)
		}
	}
	res := finalizeOne(t, a, 1000)
	offsets := offsetsOf(res)
	// b: [150,160] 超出父[0,100] -> 平移 100-160=-60 -> [90,100]
	// c: 继承-60后有效区间[110,120], 相对已平移的父[90,100] -> 再移 100-120=-20, 累计-80 -> [90,100]
	// d: 与c同服务, 继承c的累计平移-80 -> [92,102]
	want := map[string]int64{"a": 0, "b": -60, "c": -80, "d": -80}
	t.Logf("offsets=%v want=%v (逐层重判)", offsets, want)
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("offsets = %v, want %v", offsets, want)
	}
	if !reflect.DeepEqual(res.CriticalPath, []string{"a", "b", "c", "d"}) {
		t.Fatalf("critical path = %v", res.CriticalPath)
	}
}

func TestCycleBecomesOrphan(t *testing.T) {
	a := tracing.NewAssembler(10, 0)
	for _, s := range []tracing.Span{
		sp("t1", "r", "", "svcA", 0, 100),
		sp("t1", "x", "y", "svcA", 10, 20),
		sp("t1", "y", "x", "svcA", 15, 25),
		sp("t1", "z", "missing", "svcA", 30, 40),
	} {
		if err := a.AddSpan(s); err != nil {
			t.Fatal(err)
		}
	}
	res := finalizeOne(t, a, 1000)
	t.Logf("orphans=%v (x/y成环, z父缺失)", res.Orphans)
	if !reflect.DeepEqual(res.Orphans, []string{"x", "y", "z"}) {
		t.Fatalf("orphans = %v, want [x y z]", res.Orphans)
	}
	if !reflect.DeepEqual(res.CriticalPath, []string{"r"}) {
		t.Fatalf("critical path = %v", res.CriticalPath)
	}
	if len(res.Spans) != 4 {
		t.Fatalf("finalized span count = %d, want 4", len(res.Spans))
	}
}

func TestLateSpanAfterFinalize(t *testing.T) {
	a := tracing.NewAssembler(10, 0)
	if err := a.AddSpan(sp("t1", "r", "", "svcA", 0, 10)); err != nil {
		t.Fatal(err)
	}
	finalizeOne(t, a, 100)
	if a.AcceptedCount() != 0 {
		t.Fatalf("finalized trace should release spans, accepted=%d", a.AcceptedCount())
	}
	late := sp("t1", "late", "r", "svcA", 1, 2)
	if got := rejectReason(t, a.AddSpan(late)); got != tracing.ReasonTraceFinalized {
		t.Fatalf("late span: got %s", got)
	}
	if got := rejectReason(t, a.AddSpan(sp("t1", "late2", "r", "svcA", 2, 3))); got != tracing.ReasonTraceFinalized {
		t.Fatalf("late span 2: got %s", got)
	}
	t.Logf("late count=%d", a.LateCount())
	if a.LateCount() != 2 {
		t.Fatalf("late count = %d, want 2", a.LateCount())
	}
}

func TestCapacityLimit(t *testing.T) {
	a := tracing.NewAssembler(10, 3)
	adds := []tracing.Span{
		sp("t1", "r", "", "svcA", 0, 10),
		sp("t1", "c1", "r", "svcA", 1, 2),
		sp("t2", "r", "", "svcA", 0, 5),
	}
	for _, s := range adds {
		if err := a.AddSpan(s); err != nil {
			t.Fatal(err)
		}
	}
	over := sp("t2", "c1", "r", "svcA", 1, 2)
	if got := rejectReason(t, a.AddSpan(over)); got != tracing.ReasonCapacityExceeded {
		t.Fatalf("overflow: got %s", got)
	}
	t.Logf("input=%+v rejected: 暂存3/3已达上限", over)
	// 幂等重复不占额度。
	if err := a.AddSpan(adds[1]); err != nil {
		t.Fatalf("idempotent duplicate should not count against capacity: %v", err)
	}
	if a.AcceptedCount() != 3 {
		t.Fatalf("accepted = %d, want 3", a.AcceptedCount())
	}
	// 完结释放后可以继续接收。
	results := a.AdvanceClock(100)
	t.Logf("finalized %d traces, releasing capacity", len(results))
	if len(results) != 2 || a.AcceptedCount() != 0 {
		t.Fatalf("finalized=%d accepted=%d, want 2/0", len(results), a.AcceptedCount())
	}
	if err := a.AddSpan(sp("t3", "r", "", "svcA", 0, 1)); err != nil {
		t.Fatalf("after release should accept: %v", err)
	}
}
