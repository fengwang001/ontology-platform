package tracing_test

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/tracing"
)

// permSpans 是用于顺序无关性验证的跨度集合，含跨服务偏移与同服务子树。
func permSpans() []tracing.Span {
	return []tracing.Span{
		sp("perm", "r", "", "svcA", 0, 100),
		sp("perm", "b", "r", "svcB", 150, 160),
		sp("perm", "c", "b", "svcC", 110, 120),
		sp("perm", "d", "r", "svcB", 10, 15),
		sp("perm", "e", "d", "svcA", 12, 14),
	}
}

func wantPermResult() tracing.TraceResult {
	mk := func(id, parent, svc string, start, end, off int64) tracing.AcceptedSpan {
		return tracing.AcceptedSpan{Span: sp("perm", id, parent, svc, start, end), Offset: off}
	}
	return tracing.TraceResult{
		TraceID: "perm",
		Spans: []tracing.AcceptedSpan{
			mk("b", "r", "svcB", 150, 160, -60),
			mk("c", "b", "svcC", 110, 120, -20),
			mk("d", "r", "svcB", 10, 15, 0),
			mk("e", "d", "svcA", 12, 14, 0),
			mk("r", "", "svcA", 0, 100, 0),
		},
		Orphans:      nil,
		CriticalPath: []string{"r", "b", "c"},
	}
}

func permutations(n int) [][]int {
	var out [][]int
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			out = append(out, append([]int(nil), perm...))
			return
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			rec(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	rec(0)
	return out
}

// 同一跨度集合按任意顺序到达，完结输出必须完全相同。
func TestAllPermutationsSameOutput(t *testing.T) {
	spans := permSpans()
	want := wantPermResult()
	perms := permutations(len(spans))
	t.Logf("comparing %d arrival orders", len(perms))
	for i, order := range perms {
		a := tracing.NewAssembler(10, 0)
		for _, idx := range order {
			if err := a.AddSpan(spans[idx]); err != nil {
				t.Fatalf("perm %d: add %+v: %v", i, spans[idx], err)
			}
		}
		results := a.AdvanceClock(1000)
		if len(results) != 1 {
			t.Fatalf("perm %d: finalized %d traces, want 1", i, len(results))
		}
		if !reflect.DeepEqual(results[0], want) {
			t.Fatalf("perm %d order %v:\n got %+v\nwant %+v", i, order, results[0], want)
		}
	}
	t.Logf("all %d permutations produced identical output: %+v", len(perms), want)
}

// 恒有：已接受跨度数 = 已完结输出跨度数 + 未完结暂存跨度数。
func TestAcceptedCountInvariant(t *testing.T) {
	a := tracing.NewAssembler(5, 0)
	accepted, finalized := 0, 0
	check := func(step string) {
		t.Helper()
		open := a.AcceptedCount()
		t.Logf("%s: accepted=%d finalized=%d open=%d", step, accepted, finalized, open)
		if open+finalized != accepted {
			t.Fatalf("invariant broken at %s: open(%d)+finalized(%d) != accepted(%d)", step, open, finalized, accepted)
		}
	}
	for i := 0; i < 5; i++ {
		traceID := fmt.Sprintf("tr%d", i)
		root := sp(traceID, "r", "", "svcA", 0, int64(10+i))
		child := sp(traceID, "c", "r", "svcB", 1, 2)
		for _, s := range []tracing.Span{root, child} {
			if err := a.AddSpan(s); err != nil {
				t.Fatal(err)
			}
			accepted++
			check(fmt.Sprintf("add %s/%s", traceID, s.SpanID))
		}
		// 幂等重复不改变计数。
		if err := a.AddSpan(child); err != nil {
			t.Fatal(err)
		}
		check("idempotent redelivery")
		for _, res := range a.AdvanceClock(int64(20 + i*10)) {
			finalized += len(res.Spans)
		}
		check("advance clock")
	}
	results := a.AdvanceClock(10000)
	for _, res := range results {
		finalized += len(res.Spans)
	}
	check("final drain")
	if a.AcceptedCount() != 0 || finalized != accepted {
		t.Fatalf("end state: open=%d finalized=%d accepted=%d", a.AcceptedCount(), finalized, accepted)
	}
}

// AddSpan 与 AdvanceClock 并发调用；最终输出与顺序执行完全一致。
func TestConcurrentDelivery(t *testing.T) {
	const traces = 20
	build := func() []tracing.Span {
		var out []tracing.Span
		for i := 0; i < traces; i++ {
			id := fmt.Sprintf("ct%02d", i)
			out = append(out,
				sp(id, "r", "", "svcA", 0, 100),
				sp(id, "b", "r", "svcB", 150, 160),
				sp(id, "c", "b", "svcC", 170, 180),
			)
		}
		return out
	}

	// 顺序参考输出。
	ref := tracing.NewAssembler(1000, 0)
	for _, s := range build() {
		if err := ref.AddSpan(s); err != nil {
			t.Fatal(err)
		}
	}
	want := ref.AdvanceClock(5000)
	if len(want) != traces {
		t.Fatalf("reference finalized %d, want %d", len(want), traces)
	}

	// 并发投递 + 并发时钟推进（推进幅度不足以触发完结）。
	a := tracing.NewAssembler(1000, 0)
	spans := build()
	var wg sync.WaitGroup
	for i, s := range spans {
		wg.Add(1)
		go func(i int, s tracing.Span) {
			defer wg.Done()
			// 交错起点，打散到达顺序。
			for j := 0; j <= i%7; j++ {
				a.AdvanceClock(int64(j))
			}
			if err := a.AddSpan(s); err != nil {
				t.Errorf("add %+v: %v", s, err)
			}
		}(i, s)
	}
	wg.Wait()
	got := a.AdvanceClock(5000)
	if a.LateCount() != 0 {
		t.Fatalf("unexpected late rejects: %d", a.LateCount())
	}
	if a.AcceptedCount() != 0 {
		t.Fatalf("open spans remain: %d", a.AcceptedCount())
	}
	if !reflect.DeepEqual(got, want) {
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("trace %s:\n got %+v\nwant %+v", want[i].TraceID, got[i], want[i])
			}
		}
		t.Fatal("concurrent output differs from sequential reference")
	}
	t.Logf("concurrent output matches sequential reference for %d traces", traces)
}
