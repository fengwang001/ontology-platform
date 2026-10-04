package pipeline

import (
	"errors"
	"fmt"
	"testing"

	"ontology/dag"
)

func mustNew(t *testing.T, specs []JobSpec) *Pipeline {
	t.Helper()
	p, err := New(specs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func mustState(t *testing.T, p *Pipeline, name string, want State) {
	t.Helper()
	got, err := p.StateOf(name)
	if err != nil {
		t.Fatalf("StateOf(%q): %v", name, err)
	}
	if got != want {
		t.Fatalf("StateOf(%q)=%s, want %s", name, got, want)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err=%v, want class %v", err, target)
	}
}

func mustStatus(t *testing.T, p *Pipeline, wantState State, wantWarnings int) {
	t.Helper()
	got := p.Status()
	if got.State != wantState || got.Warnings != wantWarnings {
		t.Fatalf("Status()=%+v, want {%s %d}", got, wantState, wantWarnings)
	}
}

func run(t *testing.T, p *Pipeline, name string, ok bool) {
	t.Helper()
	mustOK(t, p.Start(name))
	mustOK(t, p.Finish(name, ok))
}

// exampleSpecs is the graph from the design brief.
func exampleSpecs(deployAllowFailure bool) []JobSpec {
	return []JobSpec{
		{Name: "build", When: "on_success"},
		{Name: "test", Needs: []string{"build"}, When: "on_success", Retry: 1},
		{Name: "lint", Needs: []string{"build"}, When: "on_success", AllowFailure: true},
		{Name: "deploy", Needs: []string{"test", "lint"}, When: "manual", AllowFailure: deployAllowFailure},
		{Name: "notify", Needs: []string{"test"}, When: "on_failure"},
		{Name: "report", Needs: []string{"lint"}, When: "on_success"},
		{Name: "cleanup", Needs: []string{"deploy"}, When: "always"},
	}
}

// driveToTestFailed runs the example up to: build ok, lint failed
// (allowed), test failed after exhausting its single retry.
func driveToTestFailed(t *testing.T, p *Pipeline) {
	t.Helper()
	mustState(t, p, "build", Pending)
	for _, n := range []string{"test", "lint", "deploy", "notify", "report", "cleanup"} {
		mustState(t, p, n, Created)
	}
	run(t, p, "build", true)
	mustState(t, p, "test", Pending)
	mustState(t, p, "lint", Pending)
	run(t, p, "lint", false)
	mustState(t, p, "lint", Failed)
	mustState(t, p, "report", Pending) // allowed failure is not bad
	mustState(t, p, "deploy", Created)
	// First test failure: back to Pending, no downstream evaluation.
	run(t, p, "test", false)
	mustState(t, p, "test", Pending)
	mustState(t, p, "deploy", Created)
	mustState(t, p, "notify", Created)
	// Second failure exhausts the retry.
	run(t, p, "test", false)
	mustState(t, p, "test", Failed)
	mustState(t, p, "deploy", Skipped) // bad upstream
	mustState(t, p, "notify", Pending) // on_failure triggered
	mustState(t, p, "cleanup", Pending)
}

func TestExampleScenario(t *testing.T) {
	p := mustNew(t, exampleSpecs(false))
	driveToTestFailed(t, p)
	run(t, p, "report", true)
	run(t, p, "notify", true)
	run(t, p, "cleanup", true)
	mustStatus(t, p, Failed, 0) // test Failed, allowFailure=false
}

func TestExampleRetryJobContinuation(t *testing.T) {
	p := mustNew(t, exampleSpecs(false))
	driveToTestFailed(t, p)
	run(t, p, "report", true)
	run(t, p, "notify", true)
	run(t, p, "cleanup", true)

	mustOK(t, p.RetryJob("test"))
	mustState(t, p, "test", Pending)
	for _, n := range []string{"deploy", "notify", "cleanup"} {
		mustState(t, p, n, Created)
	}
	mustState(t, p, "report", Success) // outside closure, untouched
	mustState(t, p, "lint", Failed)    // outside closure, untouched

	run(t, p, "test", true)
	mustState(t, p, "deploy", Manual)  // blocking gate: lint failure allowed
	mustState(t, p, "notify", Skipped) // re-decided: no more bad upstream
	mustState(t, p, "cleanup", Created)
	mustStatus(t, p, Blocked, 0)

	mustOK(t, p.Play("deploy"))
	run(t, p, "deploy", true)
	mustState(t, p, "cleanup", Pending)
	run(t, p, "cleanup", true)
	mustStatus(t, p, Success, 1) // lint is the one warning
}

func TestExampleNonBlockingGate(t *testing.T) {
	p := mustNew(t, exampleSpecs(true))
	driveToTestFailed(t, p)
	run(t, p, "report", true)
	run(t, p, "notify", true)
	run(t, p, "cleanup", true)
	mustOK(t, p.RetryJob("test"))
	run(t, p, "test", true)
	mustState(t, p, "deploy", Manual)
	mustState(t, p, "cleanup", Pending) // non-blocking gate counts as decided
	run(t, p, "cleanup", true)
	mustState(t, p, "deploy", Manual) // still waiting, never played
	mustStatus(t, p, Success, 1)
}

func TestRetryNotPropagatingBeforeExhaustion(t *testing.T) {
	p := mustNew(t, []JobSpec{
		{Name: "a", When: "on_success", Retry: 2},
		{Name: "b", Needs: []string{"a"}, When: "on_success"},
	})
	for i := 0; i < 2; i++ {
		run(t, p, "a", false)
		mustState(t, p, "a", Pending)
		mustState(t, p, "b", Created) // downstream not evaluated yet
	}
	run(t, p, "a", false) // retries exhausted
	mustState(t, p, "a", Failed)
	mustState(t, p, "b", Skipped)
}

func TestAllowedFailureIsNotBad(t *testing.T) {
	p := mustNew(t, []JobSpec{
		{Name: "a", When: "on_success", AllowFailure: true},
		{Name: "b", Needs: []string{"a"}, When: "on_success"},
		{Name: "c", Needs: []string{"a"}, When: "on_failure"},
	})
	run(t, p, "a", false)
	mustState(t, p, "a", Failed)
	mustState(t, p, "b", Pending) // allowed failure: bad=false
	mustState(t, p, "c", Skipped) // on_failure sees no bad
	mustStatus(t, p, Running, 0)
	run(t, p, "b", true)
	mustStatus(t, p, Success, 1)
}

func TestSkipPropagation(t *testing.T) {
	// r has no needs and when=on_failure: Skipped at New.
	p := mustNew(t, []JobSpec{
		{Name: "r", When: "on_failure"},
		{Name: "b", Needs: []string{"r"}, When: "on_success"},
		{Name: "c", Needs: []string{"r"}, When: "manual"},
		{Name: "d", Needs: []string{"r"}, When: "always"},
	})
	mustState(t, p, "r", Skipped)
	mustState(t, p, "b", Skipped) // skip propagates over on_success
	mustState(t, p, "c", Skipped) // skip propagates over manual
	mustState(t, p, "d", Pending) // always runs regardless
	run(t, p, "d", true)
	mustStatus(t, p, Success, 0)
}

func TestCanceledNeedCountsAsSkip(t *testing.T) {
	p := mustNew(t, []JobSpec{
		{Name: "x", When: "on_success"},
		{Name: "t", When: "on_success"},
		{Name: "d", Needs: []string{"x", "t"}, When: "on_success"},
	})
	p.Cancel()
	mustState(t, p, "x", Canceled)
	mustOK(t, p.RetryJob("t")) // resets t and its downstream d
	mustState(t, p, "d", Created)
	run(t, p, "t", true)
	mustState(t, p, "d", Skipped) // x stayed Canceled: skip=true
}

func TestNonBlockingGateLateFailureNoRollback(t *testing.T) {
	p := mustNew(t, []JobSpec{
		{Name: "r", When: "on_success"},
		{Name: "m", Needs: []string{"r"}, When: "manual", AllowFailure: true},
		{Name: "d", Needs: []string{"m"}, When: "on_success"},
	})
	run(t, p, "r", true)
	mustState(t, p, "m", Manual)
	mustState(t, p, "d", Pending) // gate decided as Success for downstream
	mustOK(t, p.Play("m"))
	run(t, p, "m", false) // gate fails afterwards (allowed)
	mustState(t, p, "m", Failed)
	mustState(t, p, "d", Pending) // no rollback of evaluated downstream
	run(t, p, "d", true)
	mustStatus(t, p, Success, 1)
}

func TestBlockingGateBlocksDownstream(t *testing.T) {
	p := mustNew(t, []JobSpec{
		{Name: "r", When: "on_success"},
		{Name: "m", Needs: []string{"r"}, When: "manual"},
		{Name: "d", Needs: []string{"m"}, When: "always"},
	})
	run(t, p, "r", true)
	mustState(t, p, "m", Manual)
	mustState(t, p, "d", Created) // blocking gate is undecided
	mustStatus(t, p, Blocked, 0)
	mustOK(t, p.Play("m"))
	run(t, p, "m", true)
	mustState(t, p, "d", Pending)
}

func TestStatusPrecedence(t *testing.T) {
	// One pipeline driven through Running > Blocked > Failed > Canceled.
	p := mustNew(t, []JobSpec{
		{Name: "a", When: "on_success"},
		{Name: "m", When: "manual"},
		{Name: "f", When: "on_success"},
		{Name: "x", When: "manual"},
		{Name: "c", Needs: []string{"x"}, When: "on_success"},
	})
	run(t, p, "f", false)        // f Failed (not allowed)
	mustStatus(t, p, Running, 0) // a Pending wins over Blocked and Failed
	run(t, p, "a", true)
	mustStatus(t, p, Blocked, 0) // m (and x) blocking gates win over Failed
	mustOK(t, p.Play("m"))
	run(t, p, "m", false)       // m Failed too
	p.Cancel()                  // x and c become Canceled
	mustStatus(t, p, Failed, 0) // Failed wins over Canceled

	q := mustNew(t, []JobSpec{
		{Name: "a", When: "on_success"},
		{Name: "b", Needs: []string{"a"}, When: "on_success"},
	})
	q.Cancel()
	mustStatus(t, q, Canceled, 0)

	r := mustNew(t, []JobSpec{
		{Name: "w1", When: "on_success", AllowFailure: true},
		{Name: "w2", When: "on_success", AllowFailure: true},
		{Name: "ok", When: "on_success"},
	})
	run(t, r, "w1", false)
	run(t, r, "w2", false)
	run(t, r, "ok", true)
	mustStatus(t, r, Success, 2)
}

func TestNewValidationOrder(t *testing.T) {
	cases := []struct {
		name  string
		specs []JobSpec
		want  error
	}{
		{"no-jobs", nil, ErrInvalidArgument},
		{"empty-name", []JobSpec{{Name: "", When: "always"}}, ErrInvalidArgument},
		{"bad-when", []JobSpec{{Name: "a", When: "sometimes"}}, ErrInvalidArgument},
		{"retry-too-high", []JobSpec{{Name: "a", When: "always", Retry: 3}}, ErrInvalidArgument},
		{"retry-negative", []JobSpec{{Name: "a", When: "always", Retry: -1}}, ErrInvalidArgument},
		// invalid argument beats duplicate name
		{"invalid>dup", []JobSpec{
			{Name: "a", When: "always", Retry: 9},
			{Name: "a", When: "always"},
		}, ErrInvalidArgument},
		{"duplicate", []JobSpec{
			{Name: "a", When: "always"},
			{Name: "a", When: "always"},
		}, dag.ErrDuplicateName},
		// duplicate beats unknown dependency
		{"dup>unknown", []JobSpec{
			{Name: "a", When: "always"},
			{Name: "a", When: "always", Needs: []string{"ghost"}},
		}, dag.ErrDuplicateName},
		{"unknown-need", []JobSpec{
			{Name: "a", When: "always", Needs: []string{"ghost"}},
		}, dag.ErrUnknownNeed},
		// unknown dependency beats cycle
		{"unknown>cycle", []JobSpec{
			{Name: "a", When: "always", Needs: []string{"b", "ghost"}},
			{Name: "b", When: "always", Needs: []string{"a"}},
		}, dag.ErrUnknownNeed},
		{"cycle", []JobSpec{
			{Name: "a", When: "always", Needs: []string{"b"}},
			{Name: "b", When: "always", Needs: []string{"a"}},
		}, dag.ErrCycle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.specs)
			mustErrIs(t, err, tc.want)
			t.Logf("New(%+v) -> %v", tc.specs, err)
		})
	}
}

func TestRuntimeRejectionOrder(t *testing.T) {
	p := mustNew(t, []JobSpec{{Name: "x", When: "on_success"}})
	// Empty name wins over everything.
	mustErrIs(t, p.Start(""), ErrInvalidArgument)
	mustErrIs(t, p.Finish("", true), ErrInvalidArgument)
	mustErrIs(t, p.Play(""), ErrInvalidArgument)
	mustErrIs(t, p.RetryJob(""), ErrInvalidArgument)
	// Unknown name beats state mismatch (x is Pending, not Running).
	mustErrIs(t, p.Finish("nope", true), ErrJobNotFound)
	_, serr := p.StateOf("nope")
	mustErrIs(t, serr, ErrJobNotFound)
	// State mismatch last.
	mustErrIs(t, p.Finish("x", true), ErrInvalidState)
	mustErrIs(t, p.Play("x"), ErrInvalidState)
	mustErrIs(t, p.RetryJob("x"), ErrInvalidState)
	// Rejected operations changed nothing.
	mustState(t, p, "x", Pending)
	mustOK(t, p.Start("x"))
	mustErrIs(t, p.Start("x"), ErrInvalidState)
	mustState(t, p, "x", Running)
}

func TestRetryJobRejectedWhileDownstreamRunning(t *testing.T) {
	p := mustNew(t, []JobSpec{
		{Name: "a", When: "on_success"},
		{Name: "b", Needs: []string{"a"}, When: "always"},
	})
	run(t, p, "a", false)
	mustState(t, p, "b", Pending)
	mustOK(t, p.Start("b"))
	mustErrIs(t, p.RetryJob("a"), ErrInvalidState)
	// Rejection changed nothing.
	mustState(t, p, "a", Failed)
	mustState(t, p, "b", Running)
}

func TestCancel(t *testing.T) {
	p := mustNew(t, []JobSpec{
		{Name: "a", When: "on_success"},
		{Name: "m", When: "manual"},
		{Name: "b", Needs: []string{"a"}, When: "always"},
		{Name: "done", When: "on_success"},
	})
	run(t, p, "done", true)
	p.Cancel()
	mustState(t, p, "a", Canceled)
	mustState(t, p, "m", Canceled)
	mustState(t, p, "b", Canceled) // Created is canceled too
	mustState(t, p, "done", Success)
	mustStatus(t, p, Canceled, 0)
	// No evaluation happens after Cancel.
	mustErrIs(t, p.Start("a"), ErrInvalidState)
	mustState(t, p, "b", Canceled)
}

// TestEvalsBoundedByDownstream proves that one Finish triggers at most
// as many evaluations as the size of the job's transitive downstream,
// independent of the number of unrelated jobs (100 vs 10000).
func TestEvalsBoundedByDownstream(t *testing.T) {
	const chain = 5
	deltas := make([]int, 0, 2)
	for _, unrelated := range []int{100, 9994} { // 9994 + 6 chain jobs = 10^4
		specs := []JobSpec{{Name: "root", When: "always"}}
		prev := "root"
		for k := 0; k < chain; k++ {
			name := fmt.Sprintf("d%d", k)
			// manual + allowFailure: each evaluates to a decided
			// (non-blocking) gate, so one Finish cascades the chain.
			specs = append(specs, JobSpec{Name: name, Needs: []string{prev}, When: "manual", AllowFailure: true})
			prev = name
		}
		for k := 0; k < unrelated; k++ {
			specs = append(specs, JobSpec{Name: fmt.Sprintf("u%d", k), When: "on_success"})
		}
		p := mustNew(t, specs)
		before := p.evals
		run(t, p, "root", true)
		delta := p.evals - before
		if delta != chain {
			t.Fatalf("unrelated=%d: Finish(root) triggered %d evals, want %d", unrelated, delta, chain)
		}
		deltas = append(deltas, delta)
		t.Logf("unrelated=%d: evals for Finish(root)=%d (transitive downstream=%d)",
			unrelated, delta, chain)
	}
	if deltas[0] != deltas[1] {
		t.Fatalf("evals differ between 100 and 10000 unrelated jobs: %v", deltas)
	}
}
