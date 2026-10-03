package autoscaler

import (
	"reflect"
	"testing"
)

func exampleConfig() Config {
	return Config{
		Min:     2,
		Max:     20,
		Initial: 4,
		High:    70,
		Low:     30,
		Up:      []Tier{{0, 20}, {10, 50}, {20, 100}},
		Down:    []Tier{{0, 10}, {10, 30}},
		MinStep: 1,
		Warmup:  5,
		CoolOut: 10,
		CoolIn:  15,
	}
}

func mustNew(t *testing.T, cfg Config) *Controller {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return c
}

func mustEval(t *testing.T, c *Controller, now, metric int64) Result {
	t.Helper()
	r, err := c.Evaluate(now, metric)
	if err != nil {
		t.Fatalf("Evaluate(%d, %d) error = %v", now, metric, err)
	}
	return r
}

func checkResult(t *testing.T, got Result, action ActionKind, amount, capAfter, inflight int64) {
	t.Helper()
	want := Result{Action: action, Amount: amount, Cap: capAfter, Inflight: inflight}
	if got != want {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
}

// TestWorkedExample 逐步复现题目给出的完整示例。
func TestWorkedExample(t *testing.T) {
	c := mustNew(t, exampleConfig())

	// Evaluate(0,75)：v=5，20%，eff=4，delta=max(1,ceil(0.8))=1，追加 (5,1)。
	checkResult(t, mustEval(t, c, 0, 75), ActionScaleOut, 1, 4, 1)
	s := c.State()
	if s.B0 != 4 || s.LastOut != 0 {
		t.Fatalf("after step1 B0=%d lastOut=%d, want 4/0", s.B0, s.LastOut)
	}
	if !reflect.DeepEqual(s.Batches, []Batch{{5, 1}}) {
		t.Fatalf("after step1 batches=%v", s.Batches)
	}

	// Evaluate(3,85)：冷却内，base=B0=4，delta=ceil(2.0)=2，target=6>eff=5，追加 (8,1)。
	checkResult(t, mustEval(t, c, 3, 85), ActionScaleOut, 1, 4, 2)
	s = c.State()
	if s.B0 != 4 || s.LastOut != 0 {
		t.Fatalf("after step2 B0=%d lastOut=%d, want unchanged 4/0", s.B0, s.LastOut)
	}
	if !reflect.DeepEqual(s.Batches, []Batch{{5, 1}, {8, 1}}) {
		t.Fatalf("after step2 batches=%v", s.Batches)
	}

	// Evaluate(5,50)：批次 (5,1) 恰等 now 就绪并入，cap=5，死区无动作。
	checkResult(t, mustEval(t, c, 5, 50), ActionNone, 0, 5, 1)

	// Evaluate(6,20)：u=10，30%，但仍有在途 (8,1)，缩容被阻止。
	checkResult(t, mustEval(t, c, 6, 20), ActionNone, 0, 5, 1)

	// Evaluate(8,20)：(8,1) 就绪，cap=6，无在途，delta=max(1,floor(1.8))=1。
	checkResult(t, mustEval(t, c, 8, 20), ActionScaleIn, 1, 5, 0)
	if c.State().LastIn != 8 {
		t.Fatalf("lastIn=%d, want 8", c.State().LastIn)
	}

	// Evaluate(10,80)：now 恰等于 lastOut+Cout，冷却结束，base=eff=5，
	// delta=ceil(2.5)=3，追加 (15,3)。
	checkResult(t, mustEval(t, c, 10, 80), ActionScaleOut, 3, 5, 3)
	s = c.State()
	if s.B0 != 5 || s.LastOut != 10 {
		t.Fatalf("after step6 B0=%d lastOut=%d, want 5/10", s.B0, s.LastOut)
	}
	if !reflect.DeepEqual(s.Batches, []Batch{{15, 3}}) {
		t.Fatalf("after step6 batches=%v", s.Batches)
	}
}
