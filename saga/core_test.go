package saga

import (
	"errors"
	"testing"

	"ontology/step"
)

var errBoom = errors.New("boom")

type fakeClock struct{ t int64 }

func (c *fakeClock) now() int64 { return c.t }

type counters struct {
	fwd map[string]int
	cmp map[string]int
}

func newCounters(keys ...string) *counters {
	c := &counters{fwd: map[string]int{}, cmp: map[string]int{}}
	for _, k := range keys {
		c.fwd[k], c.cmp[k] = 0, 0
	}
	return c
}

func defFail() error { return step.NewDefiniteFailure(errors.New("definite boom")) }

// mkStep：fwdErr 为正向结果；failN>0 时前 failN 次返回 fwdErr、之后成功。
func mkStep(key string, c *counters, fwdErr, cmpErr error, retry bool, failN ...int) step.Step {
	fails := 0
	recovers := len(failN) > 0
	if recovers {
		fails = failN[0]
	}
	return step.Step{Key: key, Retryable: retry,
		Forward: func() error {
			c.fwd[key]++
			if fails > 0 {
				fails--
				return fwdErr
			}
			if recovers {
				return nil
			}
			return fwdErr
		},
		Compensate: func() error { c.cmp[key]++; return cmpErr }}
}

func newO(t *testing.T, cfg Config) *Orchestrator {
	t.Helper()
	clk := &fakeClock{}
	o, err := New(clk.now, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return o
}

func eqInts(a, b []int) bool {
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

func TestHappyPath(t *testing.T) {
	o := newO(t, Config{})
	c := newCounters("a", "b", "c")
	st, err := o.Run("s", []step.Step{
		mkStep("a", c, nil, nil, false), mkStep("b", c, nil, nil, false),
		mkStep("c", c, nil, nil, false)})
	if err != nil || st.Status != Succeeded {
		t.Fatalf("status=%s err=%v", st.Status, err)
	}
	if c.fwd["a"]+c.fwd["b"]+c.fwd["c"] != 3 {
		t.Fatal("each forward called once")
	}
	if c.cmp["a"]+c.cmp["b"]+c.cmp["c"] != 0 {
		t.Fatal("no compensation on happy path")
	}
	if err := o.SelfCheck("s"); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestReverseCompensation(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		// 第 3 步（c）明确失败；a/b 已成功
	}{
		{"reverse order b then a, failed step skipped", []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := newO(t, Config{})
			c := newCounters("a", "b", "c")
			st, _ := o.Run("s", []step.Step{
				mkStep("a", c, nil, nil, false), mkStep("b", c, nil, nil, false),
				mkStep("c", c, defFail(), nil, false)})
			if st.Status != Compensated || !eqInts(st.Succeeded, []int{0, 1}) ||
				st.ForwardFailed != 2 {
				t.Fatalf("bad state %+v", st)
			}
			if c.cmp["c"] != 0 {
				t.Fatal("failed step must never be compensated")
			}
			if c.cmp["b"] != 1 || c.cmp["a"] != 1 {
				t.Fatalf("comp order/count a=%d b=%d", c.cmp["a"], c.cmp["b"])
			}
			if err := o.SelfCheck("s"); err != nil {
				t.Fatalf("SelfCheck: %v", err)
			}
		})
	}
}

func TestCompensationFailureContinues(t *testing.T) {
	o := newO(t, Config{})
	c := newCounters("a", "b", "c")
	st, _ := o.Run("s", []step.Step{
		mkStep("a", c, nil, nil, false),
		mkStep("b", c, nil, errBoom, false),
		mkStep("c", c, nil, nil, false),
		mkStep("d", c, defFail(), nil, false)})
	if st.Status != CompensateFailed || !eqInts(st.CompensateFailures, []int{1}) {
		t.Fatalf("state=%+v", st)
	}
	if c.cmp["a"] != 1 || c.cmp["b"] != 1 || c.cmp["c"] != 1 {
		t.Fatalf("remaining compensation must continue: a=%d b=%d c=%d",
			c.cmp["a"], c.cmp["b"], c.cmp["c"])
	}
	// Resume 只重试失败的 b；a/c 不重放，且 b 仍失败时状态不吞错。
	st2, err := o.Resume("s")
	if err == nil || st2.Status != CompensateFailed {
		t.Fatalf("resume state=%+v err=%v", st2, err)
	}
	if c.cmp["b"] != 2 || c.cmp["a"] != 1 || c.cmp["c"] != 1 {
		t.Fatalf("only failed step retried: a=%d b=%d c=%d", c.cmp["a"], c.cmp["b"], c.cmp["c"])
	}
}

func TestUnknownOutcome(t *testing.T) {
	cases := []struct {
		name    string
		unknown bool
	}{
		{"unknown presumed success and compensated", true},
		{"definite failure not compensated", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := newO(t, Config{})
			c := newCounters("a", "b")
			failErr := step.NewDefiniteFailure(errBoom)
			if tc.unknown {
				failErr = step.NewUnknownOutcome(errBoom)
			}
			st, _ := o.Run("s", []step.Step{
				mkStep("a", c, nil, nil, false), mkStep("b", c, failErr, nil, false)})
			if tc.unknown {
				if st.Status != Compensated || !eqInts(st.Succeeded, []int{0, 1}) ||
					!eqInts(st.UnknownSucceeded, []int{1}) || c.cmp["b"] != 1 {
					t.Fatalf("unknown must be presumed success: %+v cmp=%d", st, c.cmp["b"])
				}
			} else if st.Status != Compensated || c.cmp["b"] != 0 {
				t.Fatalf("definite failure must not compensate b: %+v cmp=%d", st, c.cmp["b"])
			}
		})
	}
}

func TestRetryRules(t *testing.T) {
	cases := []struct {
		name    string
		retry   bool
		max     int
		failN   int
		calls   int
		status  Status
	}{
		{"non retryable", false, 3, 1, 1, Compensated},
		{"exhaust retries", true, 3, 9, 3, Compensated},
		{"recover on 3rd", true, 3, 2, 3, Succeeded},
		{"recover on 2nd", true, 3, 1, 2, Succeeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := newO(t, Config{MaxRetries: tc.max})
			c := newCounters("a")
			st, _ := o.Run("s", []step.Step{
				mkStep("a", c, defFail(), nil, tc.retry, tc.failN)})
			if st.Status != tc.status || c.fwd["a"] != tc.calls {
				t.Fatalf("status=%s calls=%d want %s/%d", st.Status, c.fwd["a"], tc.status, tc.calls)
			}
		})
	}
}
