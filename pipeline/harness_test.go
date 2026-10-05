package pipeline

import (
	"errors"
	"testing"

	"ontology/dag"
)

// step 是表驱动场景里的一步操作及其期望。
type step struct {
	op      string // start / finish / play / cancel / retry / status
	name    string
	ok      bool
	wantErr error // errors.Is 语义
	// maxEvals 非 nil 时断言本步触发的评估次数不超过它。
	maxEvals *int
	// wantStates 断言部分作业在本步之后的状态。
	wantStates map[string]State
	// wantRetries 断言部分作业在本步之后的已用重可以及。
	wantRetries map[string]int
	// 仅 op == "status" 时使用。
	wantStatus   Status
	wantWarnings int
}

func newPipeline(t *testing.T, specs []dag.Job) *Pipeline {
	t.Helper()
	p, err := New(specs)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return p
}

func runSteps(t *testing.T, p *Pipeline, steps []step) {
	t.Helper()
	for i, s := range steps {
		before := p.evals
		var err error
		switch s.op {
		case "start":
			err = p.Start(s.name)
		case "finish":
			err = p.Finish(s.name, s.ok)
		case "play":
			err = p.Play(s.name)
		case "cancel":
			p.Cancel()
		case "retry":
			err = p.RetryJob(s.name)
		case "status":
			status, warnings := p.Status()
			if status != s.wantStatus || warnings != s.wantWarnings {
				t.Errorf("step %d: Status() = %v, %d; want %v, %d", i, status, warnings, s.wantStatus, s.wantWarnings)
			}
		default:
			t.Fatalf("step %d: unknown op %q", i, s.op)
		}
		if !errors.Is(err, s.wantErr) {
			t.Errorf("step %d (%s %s): err = %v; want errors.Is %v", i, s.op, s.name, err, s.wantErr)
		}
		if err == nil && s.maxEvals != nil {
			if delta := p.evals - before; delta > *s.maxEvals {
				t.Errorf("step %d (%s %s): evals delta = %d; want <= %d", i, s.op, s.name, delta, *s.maxEvals)
			}
		}
		for name, want := range s.wantStates {
			if got := p.jobs[name].state; got != want {
				t.Errorf("step %d (%s %s): state[%s] = %s; want %s", i, s.op, s.name, name, got, want)
			}
		}
		for name, want := range s.wantRetries {
			if got := p.jobs[name].retries; got != want {
				t.Errorf("step %d (%s %s): retries[%s] = %d; want %d", i, s.op, s.name, name, got, want)
			}
		}
		if t.Failed() {
			t.Fatalf("step %d (%s %s) failed, snapshot = %v", i, s.op, s.name, p.Snapshot())
		}
	}
}

func j(name, when string, needs ...string) dag.Job {
	return dag.Job{Name: name, Needs: needs, When: when}
}

func bound(n int) *int { return &n }

func ja(name, when string, allowFailure bool, needs ...string) dag.Job {
	return dag.Job{Name: name, Needs: needs, When: when, AllowFailure: allowFailure}
}

func jr(name, when string, retry int, needs ...string) dag.Job {
	return dag.Job{Name: name, Needs: needs, When: when, Retry: retry}
}
