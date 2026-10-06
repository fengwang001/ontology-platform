package pumpstation

import (
	"errors"
	"testing"
)

func testConfig() Config {
	return Config{
		PumpCount:            3,
		StartLevels:          []int{100, 120, 140},
		StopLevels:           []int{70, 80, 90},
		DryRunLevel:          10,
		DryRunRecoveryLevel:  20,
		OverflowLevel:        200,
		MinimumRunDuration:   10,
		MinimumStopDuration:  15,
		MinimumStartInterval: 20,
	}
}

func assertNoAction(t *testing.T, eval Evaluation) {
	t.Helper()
	if len(eval.Actions) != 0 {
		t.Fatalf("input=%d target=%d expected no action, got %#v (%s)", eval.Level, eval.Target, eval.Actions, eval.Reason)
	}
}

func assertAction(t *testing.T, eval Evaluation, id int, kind ActionKind, reason ActionReason) {
	t.Helper()
	if len(eval.Actions) != 1 || eval.Actions[0].PumpID != id || eval.Actions[0].Kind != kind || eval.Actions[0].Reason != reason {
		t.Fatalf("expected one action %d %s %s, got %#v (%s)", id, kind, reason, eval.Actions, eval.Reason)
	}
	t.Logf("level=%d t=%d target=%d running=%v action=%+v reason=%q", eval.Level, eval.Time, eval.Target, eval.Running, eval.Actions[0], eval.Reason)
}

func TestConfigValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"pump count too low", func(c *Config) { c.PumpCount = 1 }},
		{"start not above stop", func(c *Config) { c.StartLevels[0] = c.StopLevels[0] }},
		{"start not increasing", func(c *Config) { c.StartLevels[1] = c.StartLevels[0] }},
		{"stop decreases", func(c *Config) { c.StopLevels[1] = c.StopLevels[0] - 1 }},
		{"dry recovery not above dry", func(c *Config) { c.DryRunRecoveryLevel = c.DryRunLevel }},
		{"overflow not highest", func(c *Config) { c.OverflowLevel = c.StartLevels[2] }},
		{"negative timing", func(c *Config) { c.MinimumRunDuration = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.mutate(&cfg)
			_, err := New(cfg)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("expected ErrInvalidArgument, got %v", err)
			}
		})
	}
}

func TestLevelThresholdsAndHysteresis(t *testing.T) {
	controller, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}

	eval := report(t, controller, 99, 99)
	assertNoAction(t, eval)
	eval = report(t, controller, 100, 100)
	assertAction(t, eval, 1, ActionStart, ReasonNormalStart)
	eval = report(t, controller, 101, 85)
	assertNoAction(t, eval)
	if eval.Target != 1 {
		t.Fatalf("hysteresis target changed inside deadband: %d", eval.Target)
	}
	eval = report(t, controller, 109, 70)
	assertNoAction(t, eval)
	eval = report(t, controller, 110, 70)
	assertAction(t, eval, 1, ActionStop, ReasonNormalStop)

	eval = report(t, controller, 130, 120)
	assertAction(t, eval, 2, ActionStart, ReasonNormalStart)
	eval = report(t, controller, 149, 140)
	assertNoAction(t, eval)
	eval = report(t, controller, 150, 140)
	assertAction(t, eval, 3, ActionStart, ReasonNormalStart)
}

func TestMinimumRunStopAndStartIntervalBoundaries(t *testing.T) {
	controller, _ := New(testConfig())
	eval := report(t, controller, 100, 140)
	assertAction(t, eval, 1, ActionStart, ReasonNormalStart)
	eval = report(t, controller, 109, 70)
	assertNoAction(t, eval)
	eval = report(t, controller, 110, 70)
	assertAction(t, eval, 1, ActionStop, ReasonNormalStop)
	eval = report(t, controller, 119, 140)
	assertNoAction(t, eval)
	eval = report(t, controller, 120, 140)
	assertAction(t, eval, 2, ActionStart, ReasonNormalStart)
}

func TestDryRunAndRecoveryBoundaries(t *testing.T) {
	controller, _ := New(testConfig())
	eval := report(t, controller, 100, 140)
	assertAction(t, eval, 1, ActionStart, ReasonNormalStart)

	eval = report(t, controller, 101, 10)
	if len(eval.Actions) != 1 || eval.Actions[0].Kind != ActionStop || eval.Actions[0].Reason != ReasonDryRun {
		t.Fatalf("expected forced dry-run stop, got %#v (%s)", eval.Actions, eval.Reason)
	}
	if !eval.DryLock || eval.Target != 0 {
		t.Fatalf("expected locked target zero, locked=%v target=%d", eval.DryLock, eval.Target)
	}
	eval = report(t, controller, 102, 20)
	if !eval.DryLock {
		t.Fatal("level equal to recovery line must keep lock")
	}
	eval = report(t, controller, 118, 85)
	if eval.DryLock || eval.Target != 0 || len(eval.Actions) != 0 {
		t.Fatalf("lock released but target should restart from hysteresis zero, got target=%d actions=%#v", eval.Target, eval.Actions)
	}
	eval = report(t, controller, 119, 85)
	assertNoAction(t, eval)
}

func TestOverflowBoundaryStartsAllAvailablePumps(t *testing.T) {
	controller, _ := New(testConfig())
	eval := report(t, controller, 0, 200)
	if len(eval.Actions) != 3 {
		t.Fatalf("expected all three pumps started at overflow line, got %#v", eval.Actions)
	}
	for i, action := range eval.Actions {
		if action.PumpID != i+1 || action.Kind != ActionStart || action.Reason != ReasonOverflow {
			t.Fatalf("overflow action %d mismatch: %#v", i, action)
		}
	}
	eval = report(t, controller, 1, 200)
	if len(eval.Actions) != 0 {
		t.Fatalf("overflow period must not stop pumps: %#v", eval.Actions)
	}
	eval = report(t, controller, 10, 90)
	assertAction(t, eval, 3, ActionStop, ReasonNormalStop)
	if eval.Target != 2 {
		t.Fatalf("below overflow and at third stop line target should be 2, got %d", eval.Target)
	}
}

func TestOverflowToDryRunAndBackToOverflow(t *testing.T) {
	controller, _ := New(testConfig())
	eval := report(t, controller, 0, 200)
	if len(eval.Actions) != 3 || len(eval.Running) != 3 {
		t.Fatalf("expected three overflow starts, got %#v", eval.Actions)
	}

	eval = report(t, controller, 1, 10)
	if !eval.DryLock || eval.Target != 0 || len(eval.Actions) != 3 {
		t.Fatalf("dry-run must override overflow and stop all pumps: %+v", eval)
	}
	for _, action := range eval.Actions {
		if action.Kind != ActionStop || action.Reason != ReasonDryRun {
			t.Fatalf("overflow-to-dry action must be forced dry stop, got %#v", action)
		}
	}

	eval = report(t, controller, 20, 21)
	if eval.DryLock || len(eval.Actions) != 0 {
		t.Fatalf("recovery should release lock but honor stop/start delays: %+v", eval)
	}
	eval = report(t, controller, 21, 200)
	if len(eval.Actions) != 3 {
		t.Fatalf("new overflow must bypass start delays and start all pumps, got %#v", eval.Actions)
	}
}

func report(t *testing.T, controller *Controller, now int64, level int) Evaluation {
	t.Helper()
	eval, err := controller.ReportLevel(now, level)
	if err != nil {
		t.Fatalf("ReportLevel(%d,%d): %v", now, level, err)
	}
	t.Logf("REPORT input={t:%d level:%d} output={target:%d running:%v actions:%#v} basis=%q", now, level, eval.Target, eval.Running, eval.Actions, eval.Reason)
	return eval
}

func (c *Controller) unsafeLastStartForTest() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastStartAt
}
