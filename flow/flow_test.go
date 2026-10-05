package flow_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/flow"
	"ontology/quarantine"
	"ontology/rule"
)

func mustGate(t *testing.T, c, k, dmax int) *flow.Gate {
	t.Helper()
	g, err := flow.NewGate(c, k, dmax)
	if err != nil {
		t.Fatalf("NewGate(%d,%d,%d): %v", c, k, dmax, err)
	}
	return g
}

func mustRule(t *testing.T, g *flow.Gate, id, field string, lo, hi int64, sev rule.Severity) {
	t.Helper()
	if err := g.PutRule(id, field, lo, hi, sev); err != nil {
		t.Fatalf("PutRule(%s): %v", id, err)
	}
}

func mustIngest(t *testing.T, g *flow.Gate, key string, fields map[string]int64) {
	t.Helper()
	if err := g.Ingest(key, fields); err != nil {
		t.Fatalf("Ingest(%q,%v): %v", key, fields, err)
	}
}

func outSeqs(out []flow.OutEntry) []uint64 {
	seqs := make([]uint64, len(out))
	for i, e := range out {
		seqs[i] = e.Seq
	}
	return seqs
}

func TestSpecExample(t *testing.T) {
	g := mustGate(t, 3, 2, 5)
	mustRule(t, g, "r1", "amt", 0, 100, rule.Block)
	mustRule(t, g, "r2", "qty", 1, 10, rule.Warn)
	if rv := g.RV(); rv != 2 {
		t.Fatalf("RV = %d, want 2", rv)
	}

	mustIngest(t, g, "a", map[string]int64{"amt": 50, "qty": 0})  // seq1, Warn [r2]
	mustIngest(t, g, "a", map[string]int64{"amt": 150, "qty": 1}) // seq2, Quarantined [r1]
	mustIngest(t, g, "a", map[string]int64{"amt": 10, "qty": 1})  // seq3, Held
	if err := g.Ingest("a", map[string]int64{"amt": 1, "qty": 1}); !errors.Is(err, flow.ErrKeyFull) {
		t.Fatalf("4th ingest a: got %v, want ErrKeyFull", err)
	}
	mustIngest(t, g, "b", map[string]int64{"amt": -1}) // seq4, Quarantined [r1,r2]
	if err := g.Ingest("c", map[string]int64{"amt": 200, "qty": 1}); !errors.Is(err, flow.ErrFull) {
		t.Fatalf("ingest c blocking: got %v, want ErrFull", err)
	}
	mustIngest(t, g, "c", map[string]int64{"amt": 20, "qty": 5}) // seq5, 直接放行

	out := g.Out()
	if got := outSeqs(out); !reflect.DeepEqual(got, []uint64{1, 5}) {
		t.Fatalf("Out seqs = %v, want [1 5]", got)
	}
	if !reflect.DeepEqual(out[0].Violations, []string{"r2"}) || out[0].RV != 2 || out[0].Forced {
		t.Fatalf("Out[0] = %+v, want Violations=[r2] RV=2 Forced=false", out[0])
	}

	mustRule(t, g, "r1", "amt", 0, 200, rule.Block) // rv=3
	evalsBefore := g.Evals()
	if n, err := g.Reeval("a"); err != nil || n != 2 {
		t.Fatalf("Reeval(a) = %d,%v, want 2,nil", n, err)
	}
	if d := g.Evals() - evalsBefore; d != 2 {
		t.Fatalf("Reeval(a) evals = %d, want 2", d)
	}
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []uint64{1, 5, 2, 3}) {
		t.Fatalf("Out seqs = %v, want [1 5 2 3]", got)
	}

	evalsBefore = g.Evals()
	if n, err := g.Reeval("b"); err != nil || n != 0 {
		t.Fatalf("Reeval(b) = %d,%v, want 0,nil", n, err)
	}
	if d := g.Evals() - evalsBefore; d != 1 {
		t.Fatalf("Reeval(b) evals = %d, want 1", d)
	}
	queued := g.Queued()
	if len(queued) != 1 || queued[0].Seq != 4 || queued[0].RV != 3 ||
		!reflect.DeepEqual(queued[0].Violations, []string{"r1", "r2"}) ||
		queued[0].Status != quarantine.Quarantined {
		t.Fatalf("queued = %+v, want seq4 RV=3 Violations=[r1 r2] Quarantined", queued)
	}

	if err := g.Fix("b", map[string]int64{"amt": 5}); err != nil {
		t.Fatalf("Fix(b): %v", err)
	}
	out = g.Out()
	if got := outSeqs(out); !reflect.DeepEqual(got, []uint64{1, 5, 2, 3, 4}) {
		t.Fatalf("Out seqs = %v, want [1 5 2 3 4]", got)
	}
	if !reflect.DeepEqual(out[4].Violations, []string{"r2"}) || out[4].RV != 3 || out[4].Forced {
		t.Fatalf("Out[4] = %+v, want Violations=[r2] RV=3 Forced=false", out[4])
	}
	if len(g.Queued()) != 0 {
		t.Fatalf("zone should be empty, got %+v", g.Queued())
	}
}

func TestErrorPrecedence(t *testing.T) {
	g := mustGate(t, 2, 1, 1)
	mustRule(t, g, "r1", "amt", 0, 0, rule.Block)

	// 参数非法 > ErrBanned：封禁键 + 非法记录报参数非法。
	mustIngest(t, g, "a", map[string]int64{"amt": 1}) // Quarantined
	if err := g.Discard("a"); err != nil {            // 丢弃账 1 = Dmax，封禁
		t.Fatalf("Discard(a): %v", err)
	}
	if err := g.Ingest("a", nil); !errors.Is(err, flow.ErrInvalid) {
		t.Fatalf("banned+invalid: got %v, want ErrInvalid", err)
	}
	if err := g.Ingest("a", map[string]int64{"amt": 0}); !errors.Is(err, flow.ErrBanned) {
		t.Fatalf("banned: got %v, want ErrBanned", err)
	}

	// ErrKeyFull > ErrFull：K=1 的键满且总量满时报 ErrKeyFull。
	mustIngest(t, g, "b", map[string]int64{"amt": 1}) // Quarantined，总量 1
	mustIngest(t, g, "c", map[string]int64{"amt": 1}) // Quarantined，总量 2 = C
	if err := g.Ingest("b", map[string]int64{"amt": 1}); !errors.Is(err, flow.ErrKeyFull) {
		t.Fatalf("keyfull+full: got %v, want ErrKeyFull", err)
	}
	if err := g.Ingest("d", map[string]int64{"amt": 1}); !errors.Is(err, flow.ErrFull) {
		t.Fatalf("full: got %v, want ErrFull", err)
	}

	// 参数非法 > ErrNoQueue。
	if err := g.Fix("zzz", nil); !errors.Is(err, flow.ErrInvalid) {
		t.Fatalf("Fix empty patch: got %v, want ErrInvalid", err)
	}
	for name, fn := range map[string]func() error{
		"Fix":     func() error { return g.Fix("zzz", map[string]int64{"amt": 1}) },
		"Release": func() error { return g.Release("zzz") },
		"Discard": func() error { return g.Discard("zzz") },
	} {
		if err := fn(); !errors.Is(err, flow.ErrNoQueue) {
			t.Fatalf("%s(no queue): got %v, want ErrNoQueue", name, err)
		}
	}
	if _, err := g.Reeval("zzz"); !errors.Is(err, flow.ErrNoQueue) {
		t.Fatalf("Reeval(no queue): got %v, want ErrNoQueue", err)
	}

	// 参数非法 > 规则不存在。
	if err := g.DropRule(""); !errors.Is(err, flow.ErrInvalid) {
		t.Fatalf("DropRule(empty): got %v, want ErrInvalid", err)
	}
	if err := g.DropRule("nope"); !errors.Is(err, flow.ErrNoRule) {
		t.Fatalf("DropRule(nope): got %v, want ErrNoRule", err)
	}
}

func TestRecordValidation(t *testing.T) {
	g := mustGate(t, 4, 4, 2)
	if err := g.Ingest("", map[string]int64{"a": 1}); !errors.Is(err, flow.ErrInvalid) {
		t.Fatalf("empty key: got %v, want ErrInvalid", err)
	}
	if err := g.Ingest("k", nil); !errors.Is(err, flow.ErrInvalid) {
		t.Fatalf("no fields: got %v, want ErrInvalid", err)
	}
	tooMany := make(map[string]int64, 17)
	for i := 0; i < 17; i++ {
		tooMany[fmt.Sprintf("f%d", i)] = int64(i)
	}
	if err := g.Ingest("k", tooMany); !errors.Is(err, flow.ErrInvalid) {
		t.Fatalf("17 fields: got %v, want ErrInvalid", err)
	}
	if len(g.Out()) != 0 || g.Evals() != 0 {
		t.Fatal("rejected records must not change state")
	}
}

func TestHeldBlocksAndNotEvaluated(t *testing.T) {
	g := mustGate(t, 10, 5, 3)
	mustRule(t, g, "r1", "amt", 0, 100, rule.Block)

	mustIngest(t, g, "a", map[string]int64{"amt": 150}) // seq1 Quarantined
	evals := g.Evals()
	// 本可通过的记录被同键隔离者挡住，且入队时不判定。
	mustIngest(t, g, "a", map[string]int64{"amt": 10}) // seq2 Held
	mustIngest(t, g, "a", map[string]int64{"amt": 20}) // seq3 Held
	if d := g.Evals() - evals; d != 0 {
		t.Fatalf("Held ingest evals = %d, want 0", d)
	}
	if len(g.Out()) != 0 {
		t.Fatalf("Out = %+v, want empty (后继不得越过被隔离者)", g.Out())
	}
	queued := g.Queued()
	if len(queued) != 3 || queued[0].Status != quarantine.Quarantined ||
		queued[1].Status != quarantine.Held || queued[2].Status != quarantine.Held {
		t.Fatalf("queued = %+v, want [Quarantined Held Held]", queued)
	}
	if len(queued[1].Violations) != 0 || queued[1].RV != 0 {
		t.Fatalf("Held record must be unevaluated, got %+v", queued[1])
	}
}

func TestReevalStopsAtFirstFailure(t *testing.T) {
	g := mustGate(t, 10, 5, 3)
	mustRule(t, g, "r1", "amt", 0, 100, rule.Block)
	mustIngest(t, g, "a", map[string]int64{"amt": 150}) // seq1 Quarantined
	mustIngest(t, g, "a", map[string]int64{"amt": 50})  // seq2 Held
	mustIngest(t, g, "a", map[string]int64{"amt": 999}) // seq3 Held
	mustIngest(t, g, "a", map[string]int64{"amt": 60})  // seq4 Held

	mustRule(t, g, "r1", "amt", 0, 200, rule.Block) // 放宽区间，rv=2
	evals := g.Evals()
	n, err := g.Reeval("a")
	if err != nil || n != 2 {
		t.Fatalf("Reeval(a) = %d,%v, want 2,nil", n, err)
	}
	// 放行 seq1、seq2，遇 seq3 不通过停下：evals = 放行数+1。
	if d := g.Evals() - evals; d != 3 {
		t.Fatalf("evals = %d, want 3 (放行 2 + 拦停 1)", d)
	}
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []uint64{1, 2}) {
		t.Fatalf("Out = %v, want [1 2]", got)
	}
	queued := g.Queued()
	if len(queued) != 2 || queued[0].Seq != 3 || queued[0].Status != quarantine.Quarantined ||
		queued[0].RV != 2 || !reflect.DeepEqual(queued[0].Violations, []string{"r1"}) ||
		queued[1].Seq != 4 || queued[1].Status != quarantine.Held {
		t.Fatalf("queued = %+v, want head seq3 Quarantined rv2 [r1], then seq4 Held", queued)
	}
}

func TestEvalsIndependentOfHeldCount(t *testing.T) {
	for _, k := range []int{10, 1000} {
		g := mustGate(t, k+1, k, 3)
		mustRule(t, g, "r1", "amt", 0, 0, rule.Block)
		mustIngest(t, g, "a", map[string]int64{"amt": 1}) // Quarantined
		for i := 0; i < k-1; i++ {
			mustIngest(t, g, "a", map[string]int64{"amt": 1}) // Held
		}
		evals := g.Evals()
		n, err := g.Reeval("a")
		if err != nil || n != 0 {
			t.Fatalf("K=%d: Reeval = %d,%v, want 0,nil", k, n, err)
		}
		if d := g.Evals() - evals; d != 1 {
			t.Fatalf("K=%d: evals = %d, want 1 (与 Held 条数无关)", k, d)
		}
	}
}

func TestDirectReleaseWhenZoneFull(t *testing.T) {
	g := mustGate(t, 1, 1, 2)
	mustRule(t, g, "r1", "amt", 0, 0, rule.Block)
	mustIngest(t, g, "a", map[string]int64{"amt": 1}) // Quarantined，总量 1 = C
	// 容量恰满时直接放行不受影响。
	mustIngest(t, g, "b", map[string]int64{"amt": 0})
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []uint64{2}) {
		t.Fatalf("Out = %v, want [2]", got)
	}
}

func TestDiscardThenNewHeadEvaluated(t *testing.T) {
	g := mustGate(t, 10, 5, 3)
	mustRule(t, g, "r1", "amt", 0, 100, rule.Block)
	mustRule(t, g, "r2", "qty", 1, 10, rule.Warn)
	mustIngest(t, g, "a", map[string]int64{"amt": 150, "qty": 1}) // seq1 Quarantined [r1]
	mustIngest(t, g, "a", map[string]int64{"amt": 10, "qty": 0})  // seq2 Held

	evals := g.Evals()
	if err := g.Discard("a"); err != nil {
		t.Fatalf("Discard(a): %v", err)
	}
	// Discard 后隐式 Reeval：新队首首次判定。
	if d := g.Evals() - evals; d != 1 {
		t.Fatalf("evals = %d, want 1 (新队首首次判定)", d)
	}
	dropped := g.Dropped()
	if len(dropped) != 1 || dropped[0].Seq != 1 ||
		!reflect.DeepEqual(dropped[0].Violations, []string{"r1"}) || dropped[0].RV != 2 {
		t.Fatalf("Dropped = %+v, want seq1 [r1] rv2", dropped)
	}
	out := g.Out()
	if len(out) != 1 || out[0].Seq != 2 || !reflect.DeepEqual(out[0].Violations, []string{"r2"}) {
		t.Fatalf("Out = %+v, want seq2 Warn [r2]", out)
	}
}

func TestReleaseForcedViolations(t *testing.T) {
	g := mustGate(t, 10, 5, 3)
	mustRule(t, g, "r1", "amt", 0, 100, rule.Block)
	mustRule(t, g, "r2", "qty", 1, 10, rule.Warn)
	mustIngest(t, g, "a", map[string]int64{"amt": 150})          // seq1 Quarantined [r1 r2]（qty 缺失）
	mustIngest(t, g, "a", map[string]int64{"amt": 10, "qty": 1}) // seq2 Held

	if err := g.Release("a"); err != nil {
		t.Fatalf("Release(a): %v", err)
	}
	out := g.Out()
	if len(out) != 2 {
		t.Fatalf("Out len = %d, want 2", len(out))
	}
	// 强制放行：Forced=true，违规取最近一次判定的全部违规。
	if !out[0].Forced || !reflect.DeepEqual(out[0].Violations, []string{"r1", "r2"}) || out[0].RV != 2 {
		t.Fatalf("Out[0] = %+v, want Forced=true Violations=[r1 r2] RV=2", out[0])
	}
	// Release 后隐式 Reeval：seq2 通过放行。
	if out[1].Seq != 2 || out[1].Forced || len(out[1].Violations) != 0 {
		t.Fatalf("Out[1] = %+v, want seq2 非强制无违规", out[1])
	}
}

func TestBanExactlyAtDmax(t *testing.T) {
	g := mustGate(t, 10, 5, 2)
	mustRule(t, g, "r1", "amt", 0, 0, rule.Block)
	for i := 0; i < 4; i++ {
		mustIngest(t, g, "a", map[string]int64{"amt": 1}) // 1 Quarantined + 3 Held
	}
	if err := g.Discard("a"); err != nil {
		t.Fatalf("Discard 1: %v", err)
	}
	// 未达 Dmax，仍可接收。
	mustIngest(t, g, "a", map[string]int64{"amt": 1})
	if err := g.Discard("a"); err != nil {
		t.Fatalf("Discard 2: %v", err)
	}
	// 恰达 Dmax=2 即封禁。
	if err := g.Ingest("a", map[string]int64{"amt": 1}); !errors.Is(err, flow.ErrBanned) {
		t.Fatalf("banned ingest: got %v, want ErrBanned", err)
	}
	// 队列中已有的记录仍可处置。
	if err := g.Release("a"); err != nil {
		t.Fatalf("Release on banned key: %v", err)
	}
	if n, err := g.Reeval("a"); err != nil || n != 0 {
		t.Fatalf("Reeval on banned key = %d,%v, want 0,nil", n, err)
	}
	if len(g.Dropped()) != 2 {
		t.Fatalf("Dropped len = %d, want 2", len(g.Dropped()))
	}
}

func TestReevalAllByteOrder(t *testing.T) {
	g := mustGate(t, 10, 5, 3)
	mustRule(t, g, "r1", "amt", 0, 100, rule.Block)
	mustIngest(t, g, "b", map[string]int64{"amt": 200}) // seq1 Quarantined
	mustIngest(t, g, "a", map[string]int64{"amt": 300}) // seq2 Quarantined
	mustRule(t, g, "r1", "amt", 0, 400, rule.Block)
	if n := g.ReevalAll(); n != 2 {
		t.Fatalf("ReevalAll = %d, want 2", n)
	}
	// 按键字节序处理：a(seq2) 先于 b(seq1) 放行。
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []uint64{2, 1}) {
		t.Fatalf("Out = %v, want [2 1]", got)
	}
}

func TestBatchSameKeyInteraction(t *testing.T) {
	g := mustGate(t, 10, 2, 3)
	mustRule(t, g, "r1", "amt", 0, 100, rule.Block)

	// 批内同键：首条不通过入队使次条成为 Held（不判定），第三条超 K=2 整批拒绝。
	evals := g.Evals()
	err := g.IngestBatch([]flow.Item{
		{Key: "a", Fields: map[string]int64{"amt": 150}},
		{Key: "a", Fields: map[string]int64{"amt": 10}},
		{Key: "a", Fields: map[string]int64{"amt": 20}},
	})
	var be *flow.BatchError
	if !errors.As(err, &be) || be.Index != 2 || !errors.Is(err, flow.ErrKeyFull) {
		t.Fatalf("batch err = %v, want index 2 ErrKeyFull", err)
	}
	// 整批回滚：不占 seq、不改状态、不计判定。
	if len(g.Queued()) != 0 || len(g.Out()) != 0 || len(g.Dropped()) != 0 {
		t.Fatalf("rejected batch must not change state: queued=%v out=%v dropped=%v",
			g.Queued(), g.Out(), g.Dropped())
	}
	if g.Evals() != evals {
		t.Fatalf("rejected batch evals = %d, want %d", g.Evals(), evals)
	}
	mustIngest(t, g, "ok", map[string]int64{"amt": 1}) // seq1：回滚未占号
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("Out = %v, want [1]", got)
	}

	// 全可接受才按次序生效。
	evals = g.Evals()
	err = g.IngestBatch([]flow.Item{
		{Key: "x", Fields: map[string]int64{"amt": 150}}, // seq2 Quarantined
		{Key: "x", Fields: map[string]int64{"amt": 10}},  // seq3 Held（不判定）
	})
	if err != nil {
		t.Fatalf("valid batch: %v", err)
	}
	if d := g.Evals() - evals; d != 1 {
		t.Fatalf("batch evals = %d, want 1 (Held 不判定)", d)
	}
	queued := g.Queued()
	if len(queued) != 2 || queued[0].Seq != 2 || queued[0].Status != quarantine.Quarantined ||
		queued[0].RV != 1 || !reflect.DeepEqual(queued[0].Violations, []string{"r1"}) ||
		queued[1].Seq != 3 || queued[1].Status != quarantine.Held {
		t.Fatalf("queued = %+v, want [seq2 Quarantined rv2 [r1], seq3 Held]", queued)
	}
}

func TestBatchRollbackOnCapacity(t *testing.T) {
	g := mustGate(t, 2, 2, 3)
	mustRule(t, g, "r1", "amt", 0, 0, rule.Block)
	mustIngest(t, g, "z", map[string]int64{"amt": 1}) // seq1 Quarantined，总量 1

	evals := g.Evals()
	err := g.IngestBatch([]flow.Item{
		{Key: "b", Fields: map[string]int64{"amt": 1}}, // 入队后总量达 C=2
		{Key: "c", Fields: map[string]int64{"amt": 1}}, // 触发 ErrFull
	})
	var be *flow.BatchError
	if !errors.As(err, &be) || be.Index != 1 || !errors.Is(err, flow.ErrFull) {
		t.Fatalf("batch err = %v, want index 1 ErrFull", err)
	}
	if got := len(g.Queued()); got != 1 {
		t.Fatalf("queued len = %d, want 1 (整批回滚)", got)
	}
	if g.Evals() != evals {
		t.Fatalf("rejected batch evals changed: %d -> %d", evals, g.Evals())
	}
	mustIngest(t, g, "ok", map[string]int64{"amt": 0}) // seq2：回滚未占号
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []uint64{2}) {
		t.Fatalf("Out = %v, want [2]", got)
	}
}

func TestBatchValidation(t *testing.T) {
	g := mustGate(t, 4, 4, 2)
	if err := g.IngestBatch(nil); !errors.Is(err, flow.ErrInvalid) {
		t.Fatalf("empty batch: got %v, want ErrInvalid", err)
	}
	items := make([]flow.Item, 101)
	for i := range items {
		items[i] = flow.Item{Key: "k", Fields: map[string]int64{"a": 1}}
	}
	if err := g.IngestBatch(items); !errors.Is(err, flow.ErrInvalid) {
		t.Fatalf("101 items: got %v, want ErrInvalid", err)
	}
	err := g.IngestBatch([]flow.Item{
		{Key: "k", Fields: map[string]int64{"a": 1}},
		{Key: "", Fields: map[string]int64{"a": 1}},
	})
	var be *flow.BatchError
	if !errors.As(err, &be) || be.Index != 1 || !errors.Is(err, flow.ErrInvalid) {
		t.Fatalf("batch err = %v, want index 1 ErrInvalid", err)
	}
}

func TestConcurrent(t *testing.T) {
	g := mustGate(t, 50, 10, 3)
	mustRule(t, g, "r1", "amt", 0, 100, rule.Block)
	mustRule(t, g, "r2", "qty", 1, 10, rule.Warn)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			keys := []string{"a", "b", "c", "d"}
			for i := 0; i < 300; i++ {
				key := keys[(w+i)%len(keys)]
				fields := map[string]int64{"amt": int64((w*7 + i*13) % 300), "qty": int64(i % 12)}
				switch (w + i) % 6 {
				case 0:
					_ = g.Ingest(key, fields)
				case 1:
					_, _ = g.Reeval(key)
				case 2:
					_ = g.Fix(key, map[string]int64{"amt": 5})
				case 3:
					_ = g.Release(key)
				case 4:
					_ = g.Discard(key)
				case 5:
					_ = g.IngestBatch([]flow.Item{{Key: key, Fields: fields}})
				}
			}
		}(w)
	}
	wg.Wait()

	// 不变式：每条被接受的记录恰处于 Out、Dropped、隔离区三者之一。
	seen := make(map[uint64]int)
	for _, e := range g.Out() {
		seen[e.Seq]++
	}
	for _, e := range g.Dropped() {
		seen[e.Seq]++
	}
	queued := g.Queued()
	for _, r := range queued {
		seen[r.Seq]++
	}
	for seq, n := range seen {
		if n != 1 {
			t.Fatalf("seq %d appears %d times across Out/Dropped/zone", seq, n)
		}
	}
	if len(queued) > 50 {
		t.Fatalf("zone size %d exceeds C=50", len(queued))
	}
	// 队首不变式。
	heads := make(map[string]quarantine.Status)
	for _, r := range queued {
		if _, ok := heads[r.Key]; !ok {
			heads[r.Key] = r.Status
			if r.Status != quarantine.Quarantined {
				t.Fatalf("head of key %q is %v, want Quarantined", r.Key, r.Status)
			}
		} else if r.Status != quarantine.Held {
			t.Fatalf("non-head of key %q is %v, want Held", r.Key, r.Status)
		}
	}
}
