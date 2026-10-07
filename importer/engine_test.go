package importer

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// invalidPayloadValidator 将 Payload 含 "INVALID" 的条目判定为校验失败。
func invalidPayloadValidator(ent Entry) error {
	if strings.Contains(ent.Payload, "INVALID") {
		return fmt.Errorf("entry %q is invalid", ent.ID)
	}
	return nil
}

func newTestEngine(t *testing.T, opts ...Option) *Engine {
	t.Helper()
	base := []Option{WithValidator(invalidPayloadValidator)}
	return NewEngine(append(base, opts...)...)
}

func mustCreateJob(t *testing.T, e *Engine, id string, maxChunks int) {
	t.Helper()
	if err := e.CreateJob(id, maxChunks); err != nil {
		t.Fatalf("CreateJob(%q): %v", id, err)
	}
}

func mustSubmit(t *testing.T, e *Engine, c Chunk) {
	t.Helper()
	if res := e.SubmitChunk(c); res.Outcome != OutcomeAccepted {
		t.Fatalf("SubmitChunk(seq=%d) = %+v, want accepted", c.Seq, res)
	}
}

// 跨块悬挂引用：先到的块引用未到的块，被引用块到达后自动落地，无需重新提交。
func TestCrossChunkDanglingAutoLand(t *testing.T) {
	e := newTestEngine(t)
	mustCreateJob(t, e, "job", 2)

	mustSubmit(t, e, Chunk{JobID: "job", Seq: 0, Entries: []Entry{
		{ID: "A", References: []string{"B"}},
	}})
	if info := e.QueryEntry("job", "A"); info.Status != StatusPending {
		t.Fatalf("A after chunk0 = %v, want pending", info.Status)
	}

	mustSubmit(t, e, Chunk{JobID: "job", Seq: 1, Entries: []Entry{
		{ID: "B"},
	}})
	for _, id := range []string{"A", "B"} {
		if info := e.QueryEntry("job", id); info.Status != StatusLanded {
			t.Fatalf("%s after chunk1 = %v, want landed", id, info.Status)
		}
	}
}

// 引用条目在其块处理后被判定失败时，悬挂条目必须立即传播失败，不得继续等待。
func TestPropagationOnReferencedFailure(t *testing.T) {
	e := newTestEngine(t)
	mustCreateJob(t, e, "job", 3)

	mustSubmit(t, e, Chunk{JobID: "job", Seq: 0, Entries: []Entry{
		{ID: "A", References: []string{"B"}},
		{ID: "C", References: []string{"A"}},
	}})
	mustSubmit(t, e, Chunk{JobID: "job", Seq: 1, Entries: []Entry{
		{ID: "B", Payload: "INVALID"},
	}})

	if info := e.QueryEntry("job", "B"); info.Status != StatusFailed || info.Category != CategoryEntryInvalid {
		t.Fatalf("B = %+v, want failed/entry-invalid", info)
	}
	if info := e.QueryEntry("job", "A"); info.Status != StatusFailed || info.Category != CategoryReferenceFailed {
		t.Fatalf("A = %+v, want failed/reference-failed", info)
	}
	// 失败沿引用链传播：C 引用 A，A 已失败。
	if info := e.QueryEntry("job", "C"); info.Status != StatusFailed || info.Category != CategoryReferenceFailed {
		t.Fatalf("C = %+v, want failed/reference-failed", info)
	}
}

// 达到块数上限后，仍悬挂的条目判定为超时失败；已落地条目不受影响。
func TestMaxChunksTimeout(t *testing.T) {
	e := newTestEngine(t)
	mustCreateJob(t, e, "job", 2)

	mustSubmit(t, e, Chunk{JobID: "job", Seq: 0, Entries: []Entry{
		{ID: "ok"},
		{ID: "A", References: []string{"B"}},
		{ID: "B", References: []string{"ghost"}},
	}})
	if info := e.QueryEntry("job", "A"); info.Status != StatusPending {
		t.Fatalf("A before limit = %v, want pending", info.Status)
	}

	mustSubmit(t, e, Chunk{JobID: "job", Seq: 1, Entries: []Entry{
		{ID: "done"},
	}})

	// B 引用永不到达的 ghost：超时失败。
	if info := e.QueryEntry("job", "B"); info.Status != StatusTimeout || info.Category != CategoryDanglingTimeout {
		t.Fatalf("B = %+v, want timeout/dangling-timeout", info)
	}
	// A 引用已确定失败的 B：传播失败（优先级高于超时）。
	if info := e.QueryEntry("job", "A"); info.Status != StatusFailed || info.Category != CategoryReferenceFailed {
		t.Fatalf("A = %+v, want failed/reference-failed", info)
	}
	// 已落地条目不受超时判定影响。
	for _, id := range []string{"ok", "done"} {
		if info := e.QueryEntry("job", id); info.Status != StatusLanded {
			t.Fatalf("%s = %v, want landed", id, info.Status)
		}
	}
}

// 超时（优先级 3）必须高于条目自身校验失败（优先级 4）：
// 既有未知引用又自身无效的条目，在上限到达时报告超时。
func TestTimeoutBeatsValidationFailure(t *testing.T) {
	e := newTestEngine(t)
	mustCreateJob(t, e, "job", 1)
	mustSubmit(t, e, Chunk{JobID: "job", Seq: 0, Entries: []Entry{
		{ID: "A", References: []string{"ghost"}, Payload: "INVALID"},
	}})
	if info := e.QueryEntry("job", "A"); info.Status != StatusTimeout || info.Category != CategoryDanglingTimeout {
		t.Fatalf("A = %+v, want timeout/dangling-timeout", info)
	}
}

// 内容一致的重复到达：幂等忽略，不重新处理条目。
func TestDuplicateSameContent(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[string]int)
	counting := func(ent Entry) error {
		mu.Lock()
		defer mu.Unlock()
		calls[ent.ID]++
		return invalidPayloadValidator(ent)
	}
	e := newTestEngine(t, WithValidator(counting))
	mustCreateJob(t, e, "job", 1)

	chunk := Chunk{JobID: "job", Seq: 0, Entries: []Entry{{ID: "A"}, {ID: "B"}}}
	mustSubmit(t, e, chunk)

	res := e.SubmitChunk(chunk)
	if res.Outcome != OutcomeDuplicate {
		t.Fatalf("duplicate submit = %+v, want duplicate", res)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"A", "B"} {
		if calls[id] != 1 {
			t.Fatalf("validator called %d times for %s, want exactly 1 (no reprocessing)", calls[id], id)
		}
		if info := e.QueryEntry("job", id); info.Status != StatusLanded {
			t.Fatalf("%s = %v, want landed", id, info.Status)
		}
	}
}

// 内容不一致的重复到达：判定为独立的冲突错误类别，已确定结果不被改变。
func TestDuplicateConflictingContent(t *testing.T) {
	e := newTestEngine(t)
	mustCreateJob(t, e, "job", 1)

	mustSubmit(t, e, Chunk{JobID: "job", Seq: 0, Entries: []Entry{
		{ID: "A", Payload: "v1"},
	}})

	conflict := Chunk{JobID: "job", Seq: 0, Entries: []Entry{
		{ID: "A", Payload: "v2"},
		{ID: "X"},
	}}
	res := e.SubmitChunk(conflict)
	if res.Outcome != OutcomeRejected || res.Category != CategoryChunkConflict {
		t.Fatalf("conflicting submit = %+v, want rejected/chunk-conflict", res)
	}
	if !errors.Is(res.Err, ErrChunkConflict) {
		t.Fatalf("err = %v, want ErrChunkConflict", res.Err)
	}
	// 已确定的结果不被覆盖。
	if info := e.QueryEntry("job", "A"); info.Status != StatusLanded {
		t.Fatalf("A = %+v, want landed (unchanged)", info)
	}
	// 首次出现于冲突块中的条目可查询到冲突状态。
	if info := e.QueryEntry("job", "X"); info.Status != StatusConflict || info.Category != CategoryChunkConflict {
		t.Fatalf("X = %+v, want conflict/chunk-conflict", info)
	}
	// 原始内容再次到达仍是幂等重复。
	if res := e.SubmitChunk(Chunk{JobID: "job", Seq: 0, Entries: []Entry{{ID: "A", Payload: "v1"}}}); res.Outcome != OutcomeDuplicate {
		t.Fatalf("original resubmit = %+v, want duplicate", res)
	}
}

// 双向悬挂：两个块中的条目互相引用，必须给出确定结果而非永久悬挂。
func TestBidirectionalDangling(t *testing.T) {
	run := func(t *testing.T, concurrent bool) {
		e := newTestEngine(t)
		mustCreateJob(t, e, "job", 2)
		c0 := Chunk{JobID: "job", Seq: 0, Entries: []Entry{{ID: "X", References: []string{"Y"}}}}
		c1 := Chunk{JobID: "job", Seq: 1, Entries: []Entry{{ID: "Y", References: []string{"X"}}}}
		if concurrent {
			var wg sync.WaitGroup
			for _, c := range []Chunk{c0, c1} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if res := e.SubmitChunk(c); res.Outcome != OutcomeAccepted {
						t.Errorf("SubmitChunk(seq=%d) = %+v", c.Seq, res)
					}
				}()
			}
			wg.Wait()
		} else {
			mustSubmit(t, e, c0)
			mustSubmit(t, e, c1)
		}
		for _, id := range []string{"X", "Y"} {
			info := e.QueryEntry("job", id)
			if info.Status != StatusFailed || info.Category != CategoryEntryInvalid {
				t.Fatalf("%s = %+v, want failed/entry-invalid (circular reference)", id, info)
			}
			if !strings.Contains(info.Detail, "circular") {
				t.Fatalf("%s detail = %q, want mention of circular reference", id, info.Detail)
			}
		}
	}
	t.Run("sequential", func(t *testing.T) { run(t, false) })
	t.Run("concurrent", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			run(t, true)
		}
	})
}

// 自引用是长度为 1 的循环，同样必须判定失败。
func TestSelfReference(t *testing.T) {
	e := newTestEngine(t)
	mustCreateJob(t, e, "job", 1)
	mustSubmit(t, e, Chunk{JobID: "job", Seq: 0, Entries: []Entry{
		{ID: "S", References: []string{"S"}},
	}})
	if info := e.QueryEntry("job", "S"); info.Status != StatusFailed || info.Category != CategoryEntryInvalid {
		t.Fatalf("S = %+v, want failed/entry-invalid", info)
	}
}

// 提前结束：新块被拒绝，已落地条目不受影响；内容一致的重复块仍幂等；
// 内容不一致的重复块按优先级报告冲突（优先级 1 高于提前结束 5）。
func TestEarlyTermination(t *testing.T) {
	e := newTestEngine(t)
	mustCreateJob(t, e, "job", 3)

	mustSubmit(t, e, Chunk{JobID: "job", Seq: 0, Entries: []Entry{{ID: "A", Payload: "v1"}}})
	if err := e.TerminateJob("job"); err != nil {
		t.Fatalf("TerminateJob: %v", err)
	}

	res := e.SubmitChunk(Chunk{JobID: "job", Seq: 1, Entries: []Entry{{ID: "B"}}})
	if res.Outcome != OutcomeRejected || res.Category != CategoryJobTerminated {
		t.Fatalf("submit after terminate = %+v, want rejected/job-terminated", res)
	}
	if !errors.Is(res.Err, ErrJobTerminated) {
		t.Fatalf("err = %v, want ErrJobTerminated", res.Err)
	}
	if info := e.QueryEntry("job", "A"); info.Status != StatusLanded {
		t.Fatalf("A = %+v, want landed (unaffected by termination)", info)
	}
	if info := e.QueryEntry("job", "B"); info.Status != StatusUnknown {
		t.Fatalf("B = %+v, want unknown (rejected chunk not processed)", info)
	}
	// 内容一致的重复块是幂等空操作，即使任务已结束。
	if res := e.SubmitChunk(Chunk{JobID: "job", Seq: 0, Entries: []Entry{{ID: "A", Payload: "v1"}}}); res.Outcome != OutcomeDuplicate {
		t.Fatalf("duplicate after terminate = %+v, want duplicate", res)
	}
	// 内容不一致的重复块：冲突优先级高于提前结束。
	res = e.SubmitChunk(Chunk{JobID: "job", Seq: 0, Entries: []Entry{{ID: "A", Payload: "v2"}}})
	if res.Outcome != OutcomeRejected || res.Category != CategoryChunkConflict {
		t.Fatalf("conflict after terminate = %+v, want rejected/chunk-conflict (priority over terminated)", res)
	}
}

// 提前结束与块到达并发：每个块要么完整受理并处理完毕，要么被拒绝且
// 不留痕迹；结束后的再次提交能验证受理集合是线性化的。
func TestTerminationRaceDeterminism(t *testing.T) {
	for iter := 0; iter < 100; iter++ {
		e := newTestEngine(t)
		mustCreateJob(t, e, "job", 4)
		chunks := make([]Chunk, 4)
		for i := range chunks {
			chunks[i] = Chunk{JobID: "job", Seq: i, Entries: []Entry{
				{ID: fmt.Sprintf("c%d-e0", i)},
				{ID: fmt.Sprintf("c%d-e1", i)},
			}}
		}
		results := make([]SubmitResult, len(chunks))
		var wg sync.WaitGroup
		for i, c := range chunks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = e.SubmitChunk(c)
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.TerminateJob("job")
		}()
		wg.Wait()

		for i, res := range results {
			switch res.Outcome {
			case OutcomeAccepted:
				for _, ent := range chunks[i].Entries {
					if info := e.QueryEntry("job", ent.ID); info.Status != StatusLanded {
						t.Fatalf("iter %d: accepted chunk %d entry %s = %+v, want landed", iter, i, ent.ID, info)
					}
				}
			case OutcomeRejected:
				if res.Category != CategoryJobTerminated {
					t.Fatalf("iter %d: rejected chunk %d category = %v, want job-terminated", iter, i, res.Category)
				}
				for _, ent := range chunks[i].Entries {
					if info := e.QueryEntry("job", ent.ID); info.Status != StatusUnknown {
						t.Fatalf("iter %d: rejected chunk %d entry %s = %+v, want unknown", iter, i, ent.ID, info)
					}
				}
			default:
				t.Fatalf("iter %d: chunk %d unexpected outcome %+v", iter, i, res)
			}
		}
		// 线性化探针：任务已结束，曾被受理的序号必须是幂等重复，
		// 曾被拒绝的序号必须仍被拒绝——两种结果都对应一个确定的先后顺序。
		for i, c := range chunks {
			res := e.SubmitChunk(c)
			if results[i].Outcome == OutcomeAccepted && res.Outcome != OutcomeDuplicate {
				t.Fatalf("iter %d: resubmit accepted chunk %d = %+v, want duplicate", iter, i, res)
			}
			if results[i].Outcome == OutcomeRejected &&
				(res.Outcome != OutcomeRejected || res.Category != CategoryJobTerminated) {
				t.Fatalf("iter %d: resubmit rejected chunk %d = %+v, want rejected/job-terminated", iter, i, res)
			}
		}
	}
}

// 悬挂引用的记录与查找开销不随已处理条目总数增长：
// 解析完全由 waiters 索引与 pending 集合驱动，EvalOps/ScanOps 的增量
// 只取决于当前悬挂数量。两个任务落地数量相差两个数量级的无引用条目后，
// 执行相同的悬挂-解析序列，两者的操作数增量必须完全一致。
func TestDanglingOverheadIndependentOfProcessed(t *testing.T) {
	e := newTestEngine(t)

	danglingSequence := func(job string, noiseChunks, noisePerChunk int) (evalDelta, scanDelta int64) {
		mustCreateJob(t, e, job, noiseChunks+2)
		for s := 0; s < noiseChunks; s++ {
			entries := make([]Entry, 0, noisePerChunk)
			for k := 0; k < noisePerChunk; k++ {
				entries = append(entries, Entry{ID: fmt.Sprintf("%s-noise-%d-%d", job, s, k)})
			}
			mustSubmit(t, e, Chunk{JobID: job, Seq: s, Entries: entries})
		}
		before, ok := e.Stats(job)
		if !ok {
			t.Fatalf("Stats(%q) missing", job)
		}
		mustSubmit(t, e, Chunk{JobID: job, Seq: noiseChunks, Entries: []Entry{
			{ID: job + "-dangling", References: []string{job + "-target"}},
		}})
		mid, _ := e.Stats(job)
		if mid.DanglingRefs != 1 || mid.Pending != 1 {
			t.Fatalf("%s: dangling refs = %d, pending = %d, want 1/1", job, mid.DanglingRefs, mid.Pending)
		}
		mustSubmit(t, e, Chunk{JobID: job, Seq: noiseChunks + 1, Entries: []Entry{
			{ID: job + "-target"},
		}})
		after, _ := e.Stats(job)
		if after.DanglingRefs != 0 || after.Pending != 0 {
			t.Fatalf("%s: dangling refs = %d, pending = %d, want 0/0", job, after.DanglingRefs, after.Pending)
		}
		return after.EvalOps - before.EvalOps, after.ScanOps - before.ScanOps
	}

	smallEval, smallScan := danglingSequence("small", 3, 2)
	largeEval, largeScan := danglingSequence("large", 60, 20)

	if smallEval != largeEval || smallScan != largeScan {
		t.Fatalf("resolution cost grew with processed total: small=(%d,%d) large=(%d,%d)",
			smallEval, smallScan, largeEval, largeScan)
	}
	t.Logf("resolution ops independent of processed total: eval=%d scan=%d (small landed=%d, large landed=%d)",
		smallEval, smallScan, 3*2, 60*20)
}

// 五类条目状态两两可区分，且查询是只读操作。
func TestQueryStatusDistinguishableAndReadOnly(t *testing.T) {
	e := newTestEngine(t)
	mustCreateJob(t, e, "job", 3)

	mustSubmit(t, e, Chunk{JobID: "job", Seq: 0, Entries: []Entry{
		{ID: "landed"},
		{ID: "self-invalid", Payload: "INVALID"},
		{ID: "propagated", References: []string{"self-invalid"}},
		{ID: "will-timeout", References: []string{"ghost"}},
	}})
	mustSubmit(t, e, Chunk{JobID: "job", Seq: 1, Entries: []Entry{{ID: "filler"}}})
	mustSubmit(t, e, Chunk{JobID: "job", Seq: 2, Entries: []Entry{{ID: "filler2"}}})
	// 内容不一致的重复块制造冲突状态。
	e.SubmitChunk(Chunk{JobID: "job", Seq: 0, Entries: []Entry{{ID: "conflicted"}}})

	cases := map[string]EntryStatus{
		"landed":       StatusLanded,
		"self-invalid": StatusFailed,
		"propagated":   StatusFailed,
		"will-timeout": StatusTimeout,
		"conflicted":   StatusConflict,
		"never-seen":   StatusUnknown,
	}
	for id, want := range cases {
		if info := e.QueryEntry("job", id); info.Status != want {
			t.Fatalf("%s = %v, want %v", id, info.Status, want)
		}
	}
	// 类别可区分：自身校验失败 vs 传播失败。
	if info := e.QueryEntry("job", "self-invalid"); info.Category != CategoryEntryInvalid {
		t.Fatalf("self-invalid category = %v, want entry-invalid", info.Category)
	}
	if info := e.QueryEntry("job", "propagated"); info.Category != CategoryReferenceFailed {
		t.Fatalf("propagated category = %v, want reference-failed", info.Category)
	}

	// 查询不得改变任何状态：反复查询后统计快照不变。
	before, _ := e.Stats("job")
	for id := range cases {
		e.QueryEntry("job", id)
	}
	after, _ := e.Stats("job")
	if before != after {
		t.Fatalf("query mutated state: before=%+v after=%+v", before, after)
	}
}

// 传播失败（优先级 2）必须高于条目自身校验失败（优先级 4）。
func TestPropagatedFailureBeatsValidationFailure(t *testing.T) {
	e := newTestEngine(t)
	mustCreateJob(t, e, "job", 2)
	mustSubmit(t, e, Chunk{JobID: "job", Seq: 0, Entries: []Entry{
		{ID: "A", References: []string{"B"}, Payload: "INVALID"},
	}})
	mustSubmit(t, e, Chunk{JobID: "job", Seq: 1, Entries: []Entry{
		{ID: "B", Payload: "INVALID"},
	}})
	if info := e.QueryEntry("job", "A"); info.Status != StatusFailed || info.Category != CategoryReferenceFailed {
		t.Fatalf("A = %+v, want failed/reference-failed (not entry-invalid)", info)
	}
}

// 无依赖关系的并发块可以并行处理且结果互不影响。
func TestConcurrentIndependentChunks(t *testing.T) {
	e := newTestEngine(t)
	const chunks = 16
	const perChunk = 5
	mustCreateJob(t, e, "job", chunks)
	var wg sync.WaitGroup
	for s := 0; s < chunks; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entries := make([]Entry, 0, perChunk)
			for k := 0; k < perChunk; k++ {
				entries = append(entries, Entry{ID: fmt.Sprintf("c%d-e%d", s, k)})
			}
			if res := e.SubmitChunk(Chunk{JobID: "job", Seq: s, Entries: entries}); res.Outcome != OutcomeAccepted {
				t.Errorf("chunk %d = %+v", s, res)
			}
		}()
	}
	wg.Wait()
	stats, _ := e.Stats("job")
	if stats.Landed != chunks*perChunk || stats.Pending != 0 || stats.Failed != 0 {
		t.Fatalf("stats = %+v, want landed=%d only", stats, chunks*perChunk)
	}
}

// 块序号超出声明上限时被拒绝。
func TestSeqOutOfRange(t *testing.T) {
	e := newTestEngine(t)
	mustCreateJob(t, e, "job", 1)
	res := e.SubmitChunk(Chunk{JobID: "job", Seq: 5, Entries: []Entry{{ID: "A"}}})
	if res.Outcome != OutcomeRejected || !errors.Is(res.Err, ErrSeqOutOfRange) {
		t.Fatalf("out-of-range submit = %+v, want rejected/ErrSeqOutOfRange", res)
	}
}

// 日志必须打印每个块与条目的输入、处理结果与判定依据。
func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e := newTestEngine(t, WithLogger(logger))
	mustCreateJob(t, e, "job", 2)

	mustSubmit(t, e, Chunk{JobID: "job", Seq: 0, Entries: []Entry{
		{ID: "A", References: []string{"B"}},
	}})
	mustSubmit(t, e, Chunk{JobID: "job", Seq: 1, Entries: []Entry{{ID: "B"}}})

	out := buf.String()
	for _, want := range []string{
		"chunk accepted", "seq=0", "seq=1",
		"entry pending", "entry=A", "waiting_on",
		"entry landed", "entry=B", "reason",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}
