package compensation

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// 1) 无依赖的多分支并发补偿：终态必须与串行拓扑逐一补偿一致。
func TestIndependentBranchesConcurrentCompensation(t *testing.T) {
	g := NewGraph()
	logger := &memLogger{}
	exec := NewExecutor(g, NewLockManager(), logger)

	a, err := exec.Declare("act1", []BranchSpec{
		{Name: "A", Steps: []StepSpec{step("a1", "ka", 1, false, false), step("a2", "ka", 2, false, false)}},
		{Name: "B", Steps: []StepSpec{step("b1", "kb", 10, false, false), step("b2", "kb", 20, false, false)}},
		{Name: "C", Steps: []StepSpec{step("c1", "kc", 100, false, false)}},
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	if rep := a.Execute(); rep != nil {
		t.Fatalf("forward expected success, got %v", rep)
	}
	if got := g.Snapshot(); fmt.Sprint(got) != "map[ka:3 kb:30 kc:100]" {
		t.Fatalf("forward graph = %v", got)
	}

	var wg sync.WaitGroup
	for _, b := range []string{"A", "B", "C"} {
		wg.Add(1)
		go func(name string) { defer wg.Done(); a.DirectCompensate(name) }(b)
	}
	wg.Wait()

	if !g.IsClean() {
		t.Fatalf("after compensation graph = %v, want all-zero", g.Snapshot())
	}
	if logger.count("ok") != 5 {
		t.Fatalf("expected 5 successful undo attempts, got logs:\n%s", logger.dump())
	}
	t.Log("\n" + logger.dump())
}

// 6) 两个不同动作并发执行与补偿，等价于某种串行顺序。
func TestConcurrentActionsSerializability(t *testing.T) {
	for round := 0; round < 50; round++ {
		g := NewGraph()
		locks := NewLockManager()
		var wg sync.WaitGroup
		for act := 0; act < 2; act++ {
			wg.Add(1)
			go func(act int) {
				defer wg.Done()
				exec := NewExecutor(g, locks, nil)
				suffix := fmt.Sprintf("%d", act)
				a, err := exec.Declare("act"+suffix, []BranchSpec{
					{Name: "A" + suffix, Steps: []StepSpec{step("a", "k"+suffix, int64(act+1), false, false)}},
					{Name: "B" + suffix, DependsOn: []string{"A" + suffix},
						Steps: []StepSpec{step("b", "shared", int64(10*(act+1)), false, false)}},
				})
				if err != nil {
					t.Errorf("declare: %v", err)
					return
				}
				if r := a.Execute(); r != nil {
					t.Errorf("execute: %v", r)
					return
				}
				a.DirectCompensate("B" + suffix)
				a.DirectCompensate("A" + suffix)
			}(act)
		}
		wg.Wait()
		if !g.IsClean() {
			t.Fatalf("round %d: non-serializable final state %v", round, g.Snapshot())
		}
	}
}

// 7) 与独立朴素串行模型对拍：随机 DAG + 随机正向/逆向故障注入。
func TestDifferentialAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	var verboseLogger *memLogger
	var verboseIter = -1
	for iter := 0; iter < 400; iter++ {
		spec := genRandomScenario(rng, 6)

		g1 := NewGraph()
		logger := &memLogger{}
		exec := NewExecutor(g1, NewLockManager(), logger)
		branches := make([]BranchSpec, 0, len(spec))
		for _, nb := range spec {
			steps := make([]StepSpec, 0, len(nb.Steps))
			for _, ns := range nb.Steps {
				steps = append(steps, step(ns.Name, ns.Keys[0], ns.Delta, ns.ApplyFail, ns.UndoFail))
			}
			branches = append(branches, BranchSpec{
				Name:      nb.Name,
				DependsOn: append([]string(nil), nb.DependsOn...),
				Steps:     steps,
			})
		}
		a, err := exec.Declare("rand", branches)
		if err != nil {
			t.Fatalf("iter %d: generated spec must be acyclic: %v", iter, err)
		}
		rep1 := a.Execute()
		final1 := g1.Snapshot()

		g2 := NewGraph()
		res2 := NaiveRun(g2, spec)
		final2 := g2.Snapshot()

		if fmt.Sprint(final1) != fmt.Sprint(final2) {
			t.Fatalf("iter %d graph mismatch:\nconcurrent=%v\nnaive=%v\nspec=%s",
				iter, final1, final2, dumpSpec(spec))
		}

		var opFails, passiveFails, undoFails int
		if rep1 != nil {
			opFails = len(rep1.ByKind(KindOperationFailure))
			passiveFails = len(rep1.ByKind(KindUpstreamFailure))
			undoFails = len(rep1.ByKind(KindUndoFailure))
		}
		if opFails != len(res2.OpFail) {
			t.Fatalf("iter %d op-fail count %d != naive %d\nspec=%s",
				iter, opFails, len(res2.OpFail), dumpSpec(spec))
		}
		if passiveFails != len(res2.PassiveFail) {
			t.Fatalf("iter %d passive-fail count %d != naive %d\nspec=%s",
				iter, passiveFails, len(res2.PassiveFail), dumpSpec(spec))
		}
		if undoFails != len(res2.UndoFailures) {
			t.Fatalf("iter %d undo-fail count %d != naive %d\nspec=%s",
				iter, undoFails, len(res2.UndoFailures), dumpSpec(spec))
		}
		if verboseIter < 0 && logger.count("ok")+logger.count("undo-error") > 0 {
			verboseLogger = logger
			verboseIter = iter
		}
	}
	// 打印第一轮每次补偿尝试的输入、结果与判定依据。
	if verboseLogger == nil {
		t.Fatal("no differential iteration triggered compensation")
	}
	t.Logf("differential iteration %d compensation attempts:\n%s",
		verboseIter, verboseLogger.dump())
}

// genRandomScenario 生成一个保证无环的随机分支依赖图及故障注入。
// 无环保证方式：只允许 i 依赖 j < i（按生成顺序），天然不可能成环。
// 所有正向原语都是 Add 交换律操作，因此并发模型的每个合法拓扑
// 交织都与朴素固定拓扑序终态相同，对拍只比较终态与错误集合。
func genRandomScenario(rng *rand.Rand, n int) []NaiveBranch {
	out := make([]NaiveBranch, 0, n)
	for i := 0; i < n; i++ {
		name := branchName(i)
		var deps []string
		if i > 0 {
			for j := 0; j < i; j++ {
				if rng.Intn(2) == 0 {
					deps = append(deps, branchName(j))
				}
			}
		}
		nsteps := 1 + rng.Intn(3)
		steps := make([]NaiveStep, 0, nsteps)
		for s := 0; s < nsteps; s++ {
			applyFail := rng.Intn(5) == 0
			undoFail := !applyFail && rng.Intn(6) == 0
			steps = append(steps, NaiveStep{
				Name:      fmt.Sprintf("%s-s%d", name, s),
				Keys:      []string{fmt.Sprintf("key-%d-%d", i, s)},
				Delta:     int64(1 + rng.Intn(5)),
				ApplyFail: applyFail,
				UndoFail:  undoFail,
			})
		}
		out = append(out, NaiveBranch{Name: name, DependsOn: deps, Steps: steps})
	}
	return out
}

func branchName(i int) string {
	return string(rune('A' + i))
}

func dumpSpec(spec []NaiveBranch) string {
	var b strings.Builder
	for _, nb := range spec {
		fmt.Fprintf(&b, "\n  %s deps=%v", nb.Name, nb.DependsOn)
		for _, st := range nb.Steps {
			fmt.Fprintf(&b, " {%s d=%d applyFail=%t undoFail=%t}",
				st.Name, st.Delta, st.ApplyFail, st.UndoFail)
		}
	}
	return b.String()
}

// 3) 依赖环必须在声明阶段被拒绝，且对象图无任何改动。
func TestDependencyCycleRejected(t *testing.T) {
	g := NewGraph()
	exec := NewExecutor(g, NewLockManager(), nil)
	_, err := exec.Declare("cyc", []BranchSpec{
		{Name: "A", DependsOn: []string{"C"}, Steps: []StepSpec{step("a1", "ka", 1, false, false)}},
		{Name: "B", DependsOn: []string{"A"}, Steps: []StepSpec{step("b1", "kb", 1, false, false)}},
		{Name: "C", DependsOn: []string{"B"}, Steps: []StepSpec{step("c1", "kc", 1, false, false)}},
	})
	ce, ok := err.(*cycleError)
	if !ok {
		t.Fatalf("want *cycleError, got %T %v", err, err)
	}
	if len(ce.cycle) < 2 || ce.cycle[0] != ce.cycle[len(ce.cycle)-1] {
		t.Fatalf("cycle path must return to start, got %v", ce.cycle)
	}
	if got := g.Snapshot(); len(got) != 0 {
		t.Fatalf("rejected declaration changed graph: %v", got)
	}

	_, err = exec.Declare("self", []BranchSpec{{Name: "A", DependsOn: []string{"A"}}})
	if _, ok := err.(*cycleError); !ok {
		t.Fatalf("self dependency must be a cycle, got %v", err)
	}
}

// 4) 下游未补偿前直接请求补偿上游：拒绝且状态不变。
func TestDirectCompensationRejectedByOrder(t *testing.T) {
	g := NewGraph()
	logger := &memLogger{}
	exec := NewExecutor(g, NewLockManager(), logger)
	a, err := exec.Declare("order", []BranchSpec{
		{Name: "A", Steps: []StepSpec{step("a1", "ka", 5, false, false)}},
		{Name: "B", DependsOn: []string{"A"}, Steps: []StepSpec{step("b1", "kb", 7, false, false)}},
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	if rep := a.Execute(); rep != nil {
		t.Fatalf("forward: %v", rep)
	}

	before := g.Snapshot()
	rep := a.DirectCompensate("A")
	if rep == nil || rep.Kind() != KindCompensationOrderViolation {
		t.Fatalf("expected order violation, got %v", rep)
	}
	if !strings.Contains(rep.Errors[0].Cause.Error(), "B") {
		t.Fatalf("violation reason must name blocker B: %v", rep.Errors[0])
	}
	if fmt.Sprint(before) != fmt.Sprint(g.Snapshot()) {
		t.Fatalf("rejected direct compensation changed state: before=%v after=%v",
			before, g.Snapshot())
	}
	if a.CanCompensate("A") {
		t.Fatal("A must not be compensatable while B is uncompensated")
	}
	if logger.count("rejected") != 1 {
		t.Fatalf("expected one rejected log entry:\n%s", logger.dump())
	}

	if r := a.DirectCompensate("B"); r != nil {
		t.Fatalf("compensate B: %v", r)
	}
	if !a.CanCompensate("A") {
		t.Fatal("A must be compensatable after B done")
	}
	if r := a.DirectCompensate("A"); r != nil {
		t.Fatalf("compensate A: %v", r)
	}
	if !g.IsClean() {
		t.Fatalf("graph not all-zero: %v", g.Snapshot())
	}
}

// 5) 多分支逆操作失败：信息独立、精确定位、互不遮蔽。
func TestMultipleUndoFailuresNotMasked(t *testing.T) {
	g := NewGraph()
	exec := NewExecutor(g, NewLockManager(), nil)
	a, err := exec.Declare("mask", []BranchSpec{
		{Name: "A", Steps: []StepSpec{
			step("a1", "ka", 1, false, true),
			step("a2", "ka", 1, false, false),
		}},
		{Name: "B", Steps: []StepSpec{
			step("b1", "kb", 1, false, false),
			step("b2", "kb", 1, false, true),
		}},
		{Name: "C", Steps: []StepSpec{step("c1", "kc", 1, true, false)}},
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	rep := a.Execute()
	if rep == nil {
		t.Fatal("expected report")
	}
	undoFailures := rep.ByKind(KindUndoFailure)
	if len(undoFailures) != 2 {
		t.Fatalf("expected 2 independent undo failures, got %d: %v",
			len(undoFailures), rep.Errors)
	}
	sort.Slice(undoFailures, func(i, j int) bool {
		return undoFailures[i].BranchName < undoFailures[j].BranchName
	})
	if undoFailures[0].BranchName != "A" || undoFailures[0].StepIndex != 0 {
		t.Fatalf("A failure must pinpoint A step 0, got %+v", undoFailures[0])
	}
	if undoFailures[1].BranchName != "B" || undoFailures[1].StepIndex != 1 {
		t.Fatalf("B failure must pinpoint B step 1, got %+v", undoFailures[1])
	}
	if g.Get("ka") != 1 {
		t.Fatalf("ka = %d, want 1 (a2 undone, a1 undo failed)", g.Get("ka"))
	}
	if g.Get("kb") != 1 {
		t.Fatalf("kb = %d, want 1 (b1 undone, b2 undo failed)", g.Get("kb"))
	}
}

// 5b) 首要错误优先级固定：同时存在环以外的多类错误时，
// 上游被动失败 > 自身失败 > 顺序违例 > 逆操作失败。
func TestErrorPriorityOrdering(t *testing.T) {
	g := NewGraph()
	exec := NewExecutor(g, NewLockManager(), nil)
	a, err := exec.Declare("prio", []BranchSpec{
		{Name: "A", Steps: []StepSpec{step("a1", "ka", 1, false, true)}},
		{Name: "B", DependsOn: []string{"A"},
			Steps: []StepSpec{step("b1", "kb", 1, false, true), step("b2", "kb", 1, true, false)}},
		{Name: "C", DependsOn: []string{"B"},
			Steps: []StepSpec{step("c1", "kc", 1, false, false)}},
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	rep := a.Execute()
	if rep == nil {
		t.Fatal("expected report")
	}
	if rep.Kind() != KindUpstreamFailure {
		t.Fatalf("top priority must be upstream-failure, got %s (all=%v)",
			rep.Kind(), rep.Errors)
	}
	if len(rep.ByKind(KindUndoFailure)) != 2 {
		t.Fatalf("undo failures of A and B's applied step must remain recorded: %v", rep.Errors)
	}
}

// 2a) 有依赖链 A <- B <- C 且 C 自身失败：补偿必须逆拓扑，终态为空。
func TestDependentChainCompensationOrder(t *testing.T) {
	g := NewGraph()
	exec := NewExecutor(g, NewLockManager(), nil)
	a, err := exec.Declare("chain", []BranchSpec{
		{Name: "A", Steps: []StepSpec{step("a1", "ka", 1, false, false)}},
		{Name: "B", DependsOn: []string{"A"}, Steps: []StepSpec{step("b1", "kb", 1, false, false)}},
		{Name: "C", DependsOn: []string{"B"}, Steps: []StepSpec{step("c1", "kc", 1, true, false)}},
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	rep := a.Execute()
	if rep == nil || rep.Kind() != KindOperationFailure {
		t.Fatalf("want operation-failure report, got %v", rep)
	}
	if !g.IsClean() {
		t.Fatalf("graph after auto compensation = %v, want all-zero", g.Snapshot())
	}
	if errs := rep.ByKind(KindOperationFailure); len(errs) != 1 || errs[0].BranchName != "C" {
		t.Fatalf("expected exactly C op failure, got %v", rep.Errors)
	}
}

// 2b) 上游失败向下游传播：下游一个子操作都不启动。
func TestUpstreamFailurePropagates(t *testing.T) {
	g := NewGraph()
	exec := NewExecutor(g, NewLockManager(), nil)
	a, err := exec.Declare("prop", []BranchSpec{
		{Name: "A", Steps: []StepSpec{step("a1", "ka", 1, false, false), step("a2", "ka", 1, true, false)}},
		{Name: "B", DependsOn: []string{"A"}, Steps: []StepSpec{step("b1", "kb", 1, false, false)}},
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	rep := a.Execute()
	if rep == nil {
		t.Fatal("expected failure report")
	}
	passive := rep.ByKind(KindUpstreamFailure)
	if len(passive) != 1 || passive[0].BranchName != "B" || passive[0].Detail != "upstream=A" {
		t.Fatalf("expected B passive-failed by A, got %v", rep.Errors)
	}
	if g.Get("kb") != 0 {
		t.Fatalf("B must never start, but kb=%d", g.Get("kb"))
	}
	if g.Get("ka") != 0 {
		t.Fatalf("A's applied step must be compensated, ka=%d", g.Get("ka"))
	}
	// 固定优先级：上游被动失败(2) 高于 子操作自身失败(3)。
	if rep.Kind() != KindUpstreamFailure {
		t.Fatalf("report priority = %s, want upstream-failure", rep.Kind())
	}
}
