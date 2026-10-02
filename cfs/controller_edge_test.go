package cfs

import (
	"errors"
	"testing"
)

func TestBorrowNegativeLocalExactAndShortByOne(t *testing.T) {
	c := newTestController(t, 8, 10, 3, 0, 1)
	mustWake(t, c, 0, 0)
	mustRun(t, c, 1, 0, 5)
	assertState(t, c, 0, Running, 3, 0)
	if got := c.Pool(); got != 0 {
		t.Fatalf("Pool() = %d, want 0", got)
	}

	c2 := newTestController(t, 4, 10, 1, 0, 1)
	mustWake(t, c2, 0, 0)
	mustRun(t, c2, 1, 0, 3)
	assertState(t, c2, 0, Running, 1, 0)

	c3 := newTestController(t, 3, 10, 1, 0, 1)
	mustWake(t, c3, 0, 0)
	mustRun(t, c3, 1, 0, 3)
	assertState(t, c3, 0, Throttled, 0, 1)
}

func TestBoundaryRefillNeedAndFIFO(t *testing.T) {
	c := newTestController(t, 2, 10, 2, 0, 3)
	mustWake(t, c, 0, 0)
	mustWake(t, c, 0, 1)
	mustRun(t, c, 1, 1, 3)
	mustRun(t, c, 2, 0, 1)
	assertQueue(t, c, 1, 0)
	mustWake(t, c, 10, 2)

	if err := c.Idle(10, 0); !errors.Is(err, ErrThrottled) {
		t.Fatalf("Idle throttled cpu: %v, want ErrThrottled", err)
	}
	assertStats(t, c, 1, 2, 9)
	assertState(t, c, 1, Running, 1, 1)
	assertState(t, c, 0, Throttled, -1, 2)
	assertQueue(t, c, 0)
}

func TestBoundaryPartialHeadBlocksFollowingCPU(t *testing.T) {
	c := newTestController(t, 2, 10, 4, 0, 3)
	mustWake(t, c, 0, 0)
	mustWake(t, c, 0, 1)
	mustRun(t, c, 1, 0, 5)
	mustRun(t, c, 2, 1, 3)
	assertQueue(t, c, 0, 1)

	mustWake(t, c, 10, 2)
	assertState(t, c, 0, Throttled, -1, 1)
	assertState(t, c, 1, Throttled, -3, 2)
	assertQueue(t, c, 0, 1)
	if got := c.Pool(); got != 0 {
		t.Fatalf("Pool() = %d, want 0", got)
	}
}

func TestIdleReturnLocalOneAndTwo(t *testing.T) {
	c := newTestController(t, 2, 10, 2, 0, 1)
	mustWake(t, c, 0, 0)
	mustRun(t, c, 1, 0, 1)
	mustIdle(t, c, 2, 0)
	assertState(t, c, 0, Idle, 1, 0)
	if got := c.Pool(); got != 0 {
		t.Fatalf("Pool() after local=1: %d, want 0", got)
	}

	mustWake(t, c, 3, 0)
	mustRun(t, c, 11, 0, 1)
	mustIdle(t, c, 12, 0)
	assertState(t, c, 0, Idle, 1, 0)
	if got := c.Pool(); got != 1 {
		t.Fatalf("Pool() after local=2: %d, want 1", got)
	}
}

func TestBurstCapsReturnAndBoundary(t *testing.T) {
	c := newTestController(t, 10, 100, 10, 5, 1)
	mustWake(t, c, 0, 0)
	mustRun(t, c, 1, 0, 1)
	mustIdle(t, c, 2, 0)
	if got := c.Pool(); got != 8 {
		t.Fatalf("Pool() after return: %d, want 8", got)
	}

	if err := c.Idle(101, 0); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Idle idle cpu: %v, want ErrNotRunning", err)
	}
	if got := c.Pool(); got != 8 {
		t.Fatalf("Pool() after rejected boundary: %d, want 8", got)
	}
	assertStats(t, c, 0, 0, 0)

	mustWake(t, c, 100, 0)
	if got := c.Pool(); got != 15 {
		t.Fatalf("Pool() after boundary: %d, want 15", got)
	}
	assertStats(t, c, 1, 0, 0)
}

func TestRejectedOperationDoesNotCommitBoundaries(t *testing.T) {
	c := newTestController(t, 10, 10, 5, 0, 2)
	mustWake(t, c, 0, 0)
	mustWake(t, c, 0, 1)

	if err := c.Wake(20, 0); !errors.Is(err, ErrNotIdle) {
		t.Fatalf("Wake running cpu: %v, want ErrNotIdle", err)
	}
	assertStats(t, c, 0, 0, 0)
	if got := c.Pool(); got != 10 {
		t.Fatalf("Pool() = %d, want 10", got)
	}

	if err := c.Run(-1, 1, 1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("negative now: %v, want ErrInvalidArg", err)
	}
	if err := c.Run(5, 1, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("zero duration: %v, want ErrInvalidArg", err)
	}
	if err := c.Run(5, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Run(4, 1, 1); !errors.Is(err, ErrTimeRewind) {
		t.Fatalf("rewind: %v, want ErrTimeRewind", err)
	}
}

func TestErrorPrecedence(t *testing.T) {
	c := newTestController(t, 10, 10, 5, 0, 1)
	mustWake(t, c, 0, 0)

	if err := c.Wake(-1, 99); !errors.Is(err, ErrInvalidCPU) {
		t.Fatalf("error = %v, want ErrInvalidCPU", err)
	}
	if err := c.Run(5, 99, 0); !errors.Is(err, ErrInvalidCPU) {
		t.Fatalf("error = %v, want ErrInvalidCPU", err)
	}
	if err := c.Run(-1, 0, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("error = %v, want ErrInvalidArg", err)
	}
	if err := c.Wake(5, 0); !errors.Is(err, ErrNotIdle) {
		t.Fatalf("error = %v, want ErrNotIdle", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := [][5]int64{
		{0, 1, 1, 0, 1},
		{1, 0, 1, 0, 1},
		{1, 1, 0, 0, 1},
		{1, 1, 1, -1, 1},
		{1, 1, 1, 1_000_000_001, 1},
		{1, 1, 1, 0, 0},
		{1, 1, 1, 0, 65},
	}
	for i, args := range cases {
		if _, err := New(args[0], args[1], args[2], args[3], args[4]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: error = %v, want ErrInvalidConfig", i, err)
		}
	}
}

func TestOperationArgumentUpperBounds(t *testing.T) {
	c := newTestController(t, 10, 10, 5, 0, 1)
	mustWake(t, c, 0, 0)
	if err := c.Wake(maxTime+1, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Wake upper bound: %v, want ErrInvalidArg", err)
	}
	if err := c.Run(maxTime+1, 0, 1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Run upper time: %v, want ErrInvalidArg", err)
	}
	if err := c.Run(maxTime, 0, maxRun+1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Run upper duration: %v, want ErrInvalidArg", err)
	}
}

func TestLargeEmptyQueueArithmeticNoOverflow(t *testing.T) {
	c := newTestController(t, 1_000_000_000, 2, 1, 1_000_000_000, 1)
	mustWake(t, c, 0, 0)
	c.boundaryIters = 0
	mustIdle(t, c, 2, 0)
	if got, want := c.Pool(), int64(2_000_000_000); got != want {
		t.Fatalf("Pool() = %d, want %d", got, want)
	}
	if c.boundaryIters != 1 {
		t.Fatalf("boundaryIters = %d, want 1", c.boundaryIters)
	}
}

func newTestController(t *testing.T, quota, period, slice, burst, cpus int64) *Controller {
	t.Helper()
	c, err := New(quota, period, slice, burst, cpus)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
