package pipeline

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/dag"
)

// exampleSpecs 是题目示例的作业图；deployBlocking 控制 deploy 是否为阻塞人工闸。
func exampleSpecs(deployBlocking bool) []dag.Job {
	return []dag.Job{
		j("build", "on_success"),
		jr("test", "on_success", 1, "build"),
		ja("lint", "on_success", true, "build"),
		{Name: "deploy", Needs: []string{"test", "lint"}, When: "manual", AllowFailure: !deployBlocking},
		j("notify", "on_failure", "test"),
		j("report", "on_success", "lint"),
		j("cleanup", "always", "deploy"),
	}
}

// 把示例推进到 test 重试耗尽失败：deploy 跳过、notify 与 cleanup 待定。
func exampleUntilTestFailed(t *testing.T, deployBlocking bool) *Pipeline {
	t.Helper()
	p := newPipeline(t, exampleSpecs(deployBlocking))
	runSteps(t, p, []step{
		{op: "status", wantStatus: StatusRunning},
		{op: "start", name: "build"},
		{op: "finish", name: "build", ok: true, maxEvals: bound(2),
			wantStates: map[string]State{"test": StatePending, "lint": StatePending}},
		{op: "start", name: "lint"},
		// lint 失败被允许：report 的 bad 为假，照常转 Pending。
		{op: "finish", name: "lint", ok: false, maxEvals: bound(1),
			wantStates: map[string]State{"lint": StateFailed, "report": StatePending, "deploy": StateCreated}},
		{op: "start", name: "test"},
		// 重试未耗尽：回到 Pending，不评估下游。
		{op: "finish", name: "test", ok: false, maxEvals: bound(0),
			wantStates:  map[string]State{"test": StatePending, "deploy": StateCreated, "notify": StateCreated},
			wantRetries: map[string]int{"test": 1}},
		{op: "start", name: "test"},
		// 重试耗尽：test 转 Failed；deploy 因 bad 跳过；notify 转 Pending；
		// cleanup 的上游 deploy 已定，always 转 Pending。
		{op: "finish", name: "test", ok: false, maxEvals: bound(3),
			wantStates: map[string]State{
				"test": StateFailed, "deploy": StateSkipped,
				"notify": StatePending, "cleanup": StatePending,
			}},
	})
	return p
}

// 题目示例主流程：最终 Status 为 Failed（test 的失败未被允许，
// on_failure 的 notify 自己成功并不挽回）。
func TestExampleMainFlow(t *testing.T) {
	p := exampleUntilTestFailed(t, true)
	runSteps(t, p, []step{
		{op: "start", name: "report"},
		{op: "finish", name: "report", ok: true},
		{op: "start", name: "notify"},
		{op: "finish", name: "notify", ok: true},
		{op: "start", name: "cleanup"},
		{op: "finish", name: "cleanup", ok: true},
		{op: "status", wantStatus: StatusFailed},
	})
}

// 题目续例（阻塞闸）：RetryJob(test) 重置整个下游闭包（含已成功的
// notify 与 cleanup），report 与 lint 不变；test 成功后 deploy 转阻塞
// 人工闸、notify 改判 Skipped、cleanup 仍为 Created；Play 并成功后
// 整体 Success 且 Warnings=1。
func TestExampleRetryContinuationBlockingGate(t *testing.T) {
	p := exampleUntilTestFailed(t, true)
	runSteps(t, p, []step{
		{op: "start", name: "report"},
		{op: "finish", name: "report", ok: true},
		{op: "start", name: "notify"},
		{op: "finish", name: "notify", ok: true},
		{op: "start", name: "cleanup"},
		{op: "finish", name: "cleanup", ok: true},
		{op: "status", wantStatus: StatusFailed},
		// RetryJob：test 回 Pending 且重试清零；deploy/notify/cleanup
		// 一律重置为 Created；report 与 lint 不在闭包内，保持不变。
		{op: "retry", name: "test",
			wantStates: map[string]State{
				"test": StatePending, "deploy": StateCreated,
				"notify": StateCreated, "cleanup": StateCreated,
				"report": StateSuccess, "lint": StateFailed,
			},
			wantRetries: map[string]int{"test": 0}},
		{op: "start", name: "test"},
		// test 成功：deploy 的 bad 与 skip 均为假（lint 的失败被允许），
		// 转阻塞人工闸；notify 的 bad 为假改判 Skipped；cleanup 的上游
		// deploy 未定，仍为 Created。
		{op: "finish", name: "test", ok: true, maxEvals: bound(2),
			wantStates: map[string]State{
				"test": StateSuccess, "deploy": StateManual,
				"notify": StateSkipped, "cleanup": StateCreated,
			}},
		{op: "status", wantStatus: StatusBlocked},
		{op: "play", name: "deploy", maxEvals: bound(0),
			wantStates: map[string]State{"deploy": StatePending}},
		{op: "start", name: "deploy"},
		{op: "finish", name: "deploy", ok: true, maxEvals: bound(1),
			wantStates: map[string]State{"cleanup": StatePending}},
		{op: "start", name: "cleanup"},
		{op: "finish", name: "cleanup", ok: true},
		{op: "status", wantStatus: StatusSuccess, wantWarnings: 1},
	})
}

// 题目续例（非阻塞闸）：deploy 允许失败时，test 成功那一刻 deploy
// 转 Manual 即已定，cleanup 立刻转 Pending；cleanup 成功后即使
// deploy 仍停在 Manual，Status 也是 Success。
func TestExampleNonBlockingGate(t *testing.T) {
	p := exampleUntilTestFailed(t, false)
	runSteps(t, p, []step{
		{op: "start", name: "report"},
		{op: "finish", name: "report", ok: true},
		{op: "start", name: "notify"},
		{op: "finish", name: "notify", ok: true},
		{op: "start", name: "cleanup"},
		{op: "finish", name: "cleanup", ok: true},
		{op: "status", wantStatus: StatusFailed},
		{op: "retry", name: "test"},
		{op: "start", name: "test"},
		// deploy 转 Manual 且非阻塞即已定，cleanup 级联转 Pending。
		{op: "finish", name: "test", ok: true, maxEvals: bound(3),
			wantStates: map[string]State{
				"deploy": StateManual, "notify": StateSkipped, "cleanup": StatePending,
			}},
		{op: "start", name: "cleanup"},
		{op: "finish", name: "cleanup", ok: true},
		// deploy 仍停在 Manual（非阻塞），整体 Success。
		{op: "status", wantStatus: StatusSuccess, wantWarnings: 1},
	})
}

// Skipped 沿 on_success 与 manual 向下游传播，always 照常运行。
func TestSkippedPropagation(t *testing.T) {
	p := newPipeline(t, []dag.Job{
		j("f", "on_success"),
		j("s1", "on_success", "f"),
		j("m1", "manual", "f"),
		j("al1", "always", "f"),
		j("s2", "on_success", "s1"),
		j("m2", "manual", "s1"),
		j("al2", "always", "s1"),
	})
	runSteps(t, p, []step{
		{op: "start", name: "f"},
		// f 失败：s1/m1 因 bad 跳过，al1 照常；s2/m2 因 s1 被跳过而
		// 跳过（skip 传播），al2 照常。
		{op: "finish", name: "f", ok: false, maxEvals: bound(6),
			wantStates: map[string]State{
				"s1": StateSkipped, "m1": StateSkipped, "al1": StatePending,
				"s2": StateSkipped, "m2": StateSkipped, "al2": StatePending,
			}},
		{op: "start", name: "al1"},
		{op: "finish", name: "al1", ok: true},
		{op: "start", name: "al2"},
		{op: "finish", name: "al2", ok: true},
		{op: "status", wantStatus: StatusFailed},
	})
}

// on_failure 在无 needs 与上游被跳过时均为 Skipped。
func TestOnFailureSkipped(t *testing.T) {
	p := newPipeline(t, []dag.Job{
		j("root", "on_success"),
		j("nf", "on_failure"), // 无 needs：New 时即 Skipped
		j("s", "on_success", "root"),
		j("nf2", "on_failure", "s"), // 上游被跳过：bad 为假，Skipped
	})
	if got := p.jobs["nf"].state; got != StateSkipped {
		t.Fatalf("nf = %s; want Skipped", got)
	}
	runSteps(t, p, []step{
		{op: "start", name: "root"},
		{op: "finish", name: "root", ok: false,
			wantStates: map[string]State{"s": StateSkipped, "nf2": StateSkipped}},
	})
}

// 阻塞与非阻塞人工闸的对比：阻塞闸未定、阻塞整体；非阻塞闸即已定。
func TestManualGates(t *testing.T) {
	p := newPipeline(t, []dag.Job{
		j("root", "on_success"),
		j("blocking", "manual", "root"),
		j("afterBlocking", "always", "blocking"),
		ja("open", "manual", true, "root"),
		j("afterOpen", "always", "open"),
	})
	runSteps(t, p, []step{
		{op: "start", name: "root"},
		// 非阻塞闸 open 转 Manual 即已定，afterOpen 级联转 Pending；
		// 阻塞闸 blocking 未定，afterBlocking 保持 Created。
		{op: "finish", name: "root", ok: true, maxEvals: bound(4),
			wantStates: map[string]State{
				"blocking": StateManual, "afterBlocking": StateCreated,
				"open": StateManual, "afterOpen": StatePending,
			}},
		{op: "status", wantStatus: StatusRunning}, // afterOpen 待定
		{op: "start", name: "afterOpen"},
		{op: "finish", name: "afterOpen", ok: true},
		{op: "status", wantStatus: StatusBlocked}, // 只剩阻塞闸
		{op: "play", name: "blocking"},
		{op: "start", name: "blocking"},
		{op: "finish", name: "blocking", ok: true, maxEvals: bound(1),
			wantStates: map[string]State{"afterBlocking": StatePending}},
		{op: "start", name: "afterBlocking"},
		{op: "finish", name: "afterBlocking", ok: true},
		{op: "status", wantStatus: StatusSuccess},
	})
}

// 非阻塞人工闸被 Play 之后无论结果如何，已评估过的下游都不回溯。
func TestNonBlockingGateLateFailureNoBacktrack(t *testing.T) {
	p := newPipeline(t, []dag.Job{
		ja("gate", "manual", true),
		j("child", "on_success", "gate"),
	})
	runSteps(t, p, []step{
		// New 时 gate 转 Manual 即已定，child 级联转 Pending。
		{op: "status", wantStatus: StatusRunning},
		{op: "start", name: "child"},
		{op: "finish", name: "child", ok: true},
		{op: "status", wantStatus: StatusSuccess},
		// 事后 Play 并失败：child 已成功，不回溯。
		{op: "play", name: "gate"},
		{op: "start", name: "gate"},
		{op: "finish", name: "gate", ok: false, maxEvals: bound(0),
			wantStates: map[string]State{"gate": StateFailed, "child": StateSuccess}},
		{op: "status", wantStatus: StatusSuccess, wantWarnings: 1},
	})
}

// Status 各分支的先后：Running > Blocked > Failed > Canceled > Success。
func TestStatusPrecedence(t *testing.T) {
	// Running 压过 Blocked 与 Failed。
	p := newPipeline(t, []dag.Job{
		j("f", "always"),
		j("m", "manual"),
		j("r", "always"),
	})
	runSteps(t, p, []step{
		{op: "start", name: "f"},
		{op: "finish", name: "f", ok: false},      // Failed 未被允许
		{op: "status", wantStatus: StatusRunning}, // r 仍 Pending
	})
	// Blocked 压过 Failed。
	p2 := newPipeline(t, []dag.Job{j("f", "always"), j("m", "manual")})
	runSteps(t, p2, []step{
		{op: "start", name: "f"},
		{op: "finish", name: "f", ok: false},
		{op: "status", wantStatus: StatusBlocked},
	})
	// Failed 压过 Canceled。
	p3 := newPipeline(t, []dag.Job{j("f", "always"), j("x", "always")})
	runSteps(t, p3, []step{
		{op: "start", name: "f"},
		{op: "finish", name: "f", ok: false},
		{op: "cancel"},
		{op: "status", wantStatus: StatusFailed},
	})
	// 只有 Canceled 时为 Canceled。
	p4 := newPipeline(t, []dag.Job{j("x", "always")})
	runSteps(t, p4, []step{
		{op: "cancel"},
		{op: "status", wantStatus: StatusCanceled},
	})
	// 被允许的 Failed 只计入 Warnings，不影响 Success。
	p5 := newPipeline(t, []dag.Job{ja("a", "always", true), ja("b", "always", true), j("c", "always")})
	runSteps(t, p5, []step{
		{op: "start", name: "a"},
		{op: "finish", name: "a", ok: false},
		{op: "start", name: "b"},
		{op: "finish", name: "b", ok: false},
		{op: "start", name: "c"},
		{op: "finish", name: "c", ok: true},
		{op: "status", wantStatus: StatusSuccess, wantWarnings: 2},
	})
}

// RetryJob 在传递下游存在 Running 时拒绝，且不改任何状态。
func TestRetryJobDownstreamRunningRejected(t *testing.T) {
	p := newPipeline(t, []dag.Job{
		j("a", "always"),
		j("b", "always", "a"),
	})
	runSteps(t, p, []step{
		{op: "start", name: "a"},
		{op: "finish", name: "a", ok: false},
		{op: "start", name: "b"},
		{op: "retry", name: "a", wantErr: ErrInvalidState,
			wantStates: map[string]State{"a": StateFailed, "b": StateRunning}},
	})
}

// RetryJob 对非 Failed/Canceled 目标拒绝；重置会清零重试数。
func TestRetryJobTargetState(t *testing.T) {
	p := newPipeline(t, []dag.Job{
		jr("a", "always", 2),
		j("b", "on_success", "a"),
	})
	runSteps(t, p, []step{
		{op: "retry", name: "a", wantErr: ErrInvalidState}, // Pending
		{op: "start", name: "a"},
		{op: "retry", name: "a", wantErr: ErrInvalidState}, // Running
		{op: "finish", name: "a", ok: false, wantRetries: map[string]int{"a": 1}},
		{op: "start", name: "a"},
		{op: "finish", name: "a", ok: false, wantRetries: map[string]int{"a": 2}},
		{op: "start", name: "a"},
		{op: "finish", name: "a", ok: false,
			wantStates: map[string]State{"a": StateFailed, "b": StateSkipped}},
		{op: "retry", name: "a",
			wantStates:  map[string]State{"a": StatePending, "b": StateCreated},
			wantRetries: map[string]int{"a": 0, "b": 0}},
	})
}

// 运行期拒绝次序：参数非法（空名）> 作业不存在 > 状态不符。
func TestRuntimeErrorOrdering(t *testing.T) {
	p := newPipeline(t, []dag.Job{j("a", "always"), j("b", "on_success", "a")})
	ops := []struct {
		name string
		call func(string) error
	}{
		{"start", p.Start},
		{"play", p.Play},
		{"retry", p.RetryJob},
		{"finish", func(n string) error { return p.Finish(n, true) }},
	}
	for _, op := range ops {
		if err := op.call(""); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("%s(\"\") = %v; want ErrInvalidArgument", op.name, err)
		}
		if err := op.call("ghost"); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s(ghost) = %v; want ErrNotFound", op.name, err)
		}
	}
	// b 处于 Created（a 未定）：状态不符。
	if err := p.Start("b"); !errors.Is(err, ErrInvalidState) {
		t.Errorf("Start(b) = %v; want ErrInvalidState", err)
	}
	if err := p.Finish("a", true); !errors.Is(err, ErrInvalidState) {
		t.Errorf("Finish(a) on Pending = %v; want ErrInvalidState", err)
	}
	if err := p.Play("a"); !errors.Is(err, ErrInvalidState) {
		t.Errorf("Play(a) on Pending = %v; want ErrInvalidState", err)
	}
	// 被拒绝的操作不改任何状态。
	want := map[string]State{"a": StatePending, "b": StateCreated}
	if got := p.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot() = %v; want %v", got, want)
	}
}

// Cancel 把所有非终态作业转 Canceled 且之后不再评估；
// RetryJob 可复活 Canceled 作业并恢复评估。
func TestCancel(t *testing.T) {
	p := newPipeline(t, []dag.Job{
		j("a", "always"),
		j("b", "on_success", "a"),
		j("m", "manual", "a"),
	})
	runSteps(t, p, []step{
		{op: "cancel"},
		{op: "status", wantStatus: StatusCanceled},
		{op: "start", name: "a", wantErr: ErrInvalidState},
		{op: "retry", name: "a",
			wantStates: map[string]State{
				"a": StatePending, "b": StateCreated, "m": StateCreated,
			}},
		{op: "start", name: "a"},
		{op: "finish", name: "a", ok: true, maxEvals: bound(2),
			wantStates: map[string]State{"b": StatePending, "m": StateManual}},
		{op: "status", wantStatus: StatusRunning},
		{op: "start", name: "b"},
		{op: "finish", name: "b", ok: true},
		{op: "status", wantStatus: StatusBlocked},
	})
}

// 一次 Finish 触发的评估次数不超过该作业的传递下游数，
// 且与图中无关作业数无关（100 与 10000 两档对照）。
func TestEvalsIndependentOfUnrelatedJobs(t *testing.T) {
	deltas := make(map[int]int)
	for _, noise := range []int{100, dag.MaxJobs - 3} {
		specs := []dag.Job{
			j("src", "always"),
			j("d1", "on_success", "src"),
			j("d2", "on_success", "d1"),
		}
		for i := 0; i < noise; i++ {
			specs = append(specs, j(fmt.Sprintf("noise%d", i), "always"))
		}
		p := newPipeline(t, specs)
		before := p.evals
		runSteps(t, p, []step{
			{op: "start", name: "src"},
			// src 的传递下游为 {d1, d2}；d1 转 Pending 未定，d2 不评估。
			{op: "finish", name: "src", ok: true, maxEvals: bound(2),
				wantStates: map[string]State{"d1": StatePending, "d2": StateCreated}},
		})
		deltas[noise] = p.evals - before
	}
	if deltas[100] != deltas[dag.MaxJobs-3] {
		t.Fatalf("evals delta differs: 100 noise -> %d, %d noise -> %d",
			deltas[100], dag.MaxJobs-3, deltas[dag.MaxJobs-3])
	}
	if deltas[100] != 1 {
		t.Fatalf("evals delta = %d; want exactly 1 (only d1)", deltas[100])
	}
}

// Play 不触发评估；阻塞闸 Play 后 Finish 的评估数同样被下游数限界。
func TestEvalsOnPlayAndGateFinish(t *testing.T) {
	specs := []dag.Job{
		j("gate", "manual"),
		j("c1", "always", "gate"),
		j("c2", "always", "c1"),
	}
	p := newPipeline(t, specs)
	runSteps(t, p, []step{
		{op: "play", name: "gate", maxEvals: bound(0)},
		{op: "start", name: "gate"},
		{op: "finish", name: "gate", ok: true, maxEvals: bound(2),
			wantStates: map[string]State{"c1": StatePending, "c2": StateCreated}},
	})
	if got := p.evals; got != 2 {
		t.Fatalf("total evals = %d; want 2 (gate at New, c1 at Finish)", got)
	}
}

// 并发调用等价于某个串行顺序：无数据竞争，最终状态合法。
func TestConcurrentOps(t *testing.T) {
	specs := []dag.Job{
		j("a", "always"),
		jr("b", "on_success", 2, "a"),
		j("c", "manual", "a"),
		j("d", "always", "b", "c"),
	}
	p := newPipeline(t, specs)
	var wg sync.WaitGroup
	ops := []func(){
		func() { _ = p.Start("a") },
		func() { _ = p.Finish("a", true) },
		func() { _ = p.Start("b") },
		func() { _ = p.Finish("b", false) },
		func() { _ = p.Play("c") },
		func() { _ = p.RetryJob("b") },
		func() { p.Cancel() },
		func() { _, _ = p.Status() },
		func() { _ = p.Snapshot() },
	}
	for i := 0; i < 64; i++ {
		for _, op := range ops {
			wg.Add(1)
			go func() { defer wg.Done(); op() }()
		}
	}
	wg.Wait()
	// 不变式：Created 的作业至少有一个 need 未定。
	snap := p.Snapshot()
	for name, st := range snap {
		if st != StateCreated {
			continue
		}
		hasUnsettled := false
		for _, need := range p.jobs[name].spec.Needs {
			nst := snap[need]
			settled := nst.Terminal() || (nst == StateManual && p.jobs[need].spec.AllowFailure)
			if !settled {
				hasUnsettled = true
			}
		}
		if !hasUnsettled {
			t.Errorf("job %s is Created but all needs settled, snapshot = %v", name, snap)
		}
	}
}
