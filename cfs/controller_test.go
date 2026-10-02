package cfs

import "testing"

func TestSpecExampleBurstZero(t *testing.T) {
	c, err := New(20, 100, 5, 0, 2)
	if err != nil {
		t.Fatal(err)
	}

	mustWake(t, c, 0, 0)
	mustWake(t, c, 0, 1)
	mustRun(t, c, 1, 0, 3)
	mustRun(t, c, 2, 1, 10)
	mustRun(t, c, 3, 0, 6)
	mustRun(t, c, 4, 1, 5)
	mustRun(t, c, 150, 0, 1)
	mustIdle(t, c, 170, 0)

	if got := c.Pool(); got != 13 {
		t.Fatalf("Pool() = %d, want 13", got)
	}
	assertState(t, c, 0, Idle, 1, 3)
	assertState(t, c, 1, Running, 1, 4)
	assertStats(t, c, 1, 2, 193)
	assertQueue(t, c)
}

func TestSpecExampleBurstCarry(t *testing.T) {
	c, err := New(10, 100, 5, 15, 1)
	if err != nil {
		t.Fatal(err)
	}

	mustWake(t, c, 0, 0)
	mustRun(t, c, 1, 0, 2)
	mustRun(t, c, 100, 0, 1)
	mustRun(t, c, 200, 0, 1)
	mustRun(t, c, 300, 0, 1)
	mustRun(t, c, 1000, 0, 1)

	if got := c.Pool(); got != 25 {
		t.Fatalf("Pool() = %d, want 25", got)
	}
	assertState(t, c, 0, Running, 1, 0)
	assertStats(t, c, 10, 0, 0)
}

func TestBoundaryAtNowUnthrottlesBeforeRun(t *testing.T) {
	c, err := New(3, 1, 2, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustWake(t, c, 0, 0)
	if err := c.Run(1, 0, 3); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.State(0); got.State != Throttled {
		t.Fatalf("State = %v, want throttled", got)
	}
	if err := c.Run(1, 0, 1); !errorsIs(err, ErrThrottled) {
		t.Fatalf("second Run error = %v, want ErrThrottled", err)
	}
	if err := c.Run(2, 0, 1); err != nil {
		t.Fatal(err)
	}
	assertState(t, c, 0, Running, 2, 1)
}

func TestLargeSpanBoundaryIters(t *testing.T) {
	c, err := New(1, 1, 1, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustWake(t, c, 0, 0)
	mustRun(t, c, 0, 0, 1000)
	c.boundaryIters = 0
	if err := c.Run(1_000_000_000_000_000, 0, 1); err != nil {
		t.Fatal(err)
	}
	if c.boundaryIters > 1010 {
		t.Fatalf("boundaryIters = %d, want <= 1010", c.boundaryIters)
	}
}

func mustWake(t *testing.T, c *Controller, now int64, cpu int) {
	t.Helper()
	if err := c.Wake(now, cpu); err != nil {
		t.Fatalf("Wake(%d,%d): %v", now, cpu, err)
	}
}

func mustRun(t *testing.T, c *Controller, now int64, cpu int, duration int64) {
	t.Helper()
	if err := c.Run(now, cpu, duration); err != nil {
		t.Fatalf("Run(%d,%d,%d): %v", now, cpu, duration, err)
	}
}

func mustIdle(t *testing.T, c *Controller, now int64, cpu int) {
	t.Helper()
	if err := c.Idle(now, cpu); err != nil {
		t.Fatalf("Idle(%d,%d): %v", now, cpu, err)
	}
}

func assertState(t *testing.T, c *Controller, cpu int, state CpuState, local, since int64) {
	t.Helper()
	got, err := c.State(cpu)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state || got.Local != local || got.Since != since {
		t.Fatalf("State(%d) = %+v, want state=%d local=%d since=%d", cpu, got, state, local, since)
	}
}

func assertStats(t *testing.T, c *Controller, periods, nThrottled, throttledTime int64) {
	t.Helper()
	got := c.Stats()
	want := StatsSnapshot{periods, nThrottled, throttledTime}
	if got != want {
		t.Fatalf("Stats() = %+v, want %+v", got, want)
	}
}

func assertQueue(t *testing.T, c *Controller, want ...int) {
	t.Helper()
	got := c.Queue()
	if len(got) != len(want) {
		t.Fatalf("Queue() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Queue() = %v, want %v", got, want)
		}
	}
}

func errorsIs(got, target error) bool {
	return got == target
}
