package ontology

import (
	"errors"
	"testing"
)

func mustAdd(t *testing.T, c *Coalescer, id string, period, slack, next uint64) {
	t.Helper()
	if err := c.Add(id, period, slack, next); err != nil {
		t.Fatalf("Add(%q): %v", id, err)
	}
}

func requireWake(t *testing.T, c *Coalescer, want WakeResult) {
	t.Helper()
	got, err := c.Wake()
	if err != nil {
		t.Fatalf("Wake(): %v", err)
	}
	if !wakeResultEqual(got, want) {
		t.Fatalf("Wake() = %+v, want %+v", got, want)
	}
}

func wakeResultEqual(got, want WakeResult) bool {
	if got.At != want.At || got.Left != want.Left || len(got.Fired) != len(want.Fired) {
		return false
	}
	for i := range got.Fired {
		if got.Fired[i] != want.Fired[i] {
			return false
		}
	}
	return true
}

func TestSpecExample(t *testing.T) {
	c, err := NewCoalescer(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c, "a", 10, 2, 0)
	mustAdd(t, c, "b", 6, 3, 5)
	mustAdd(t, c, "c", 5, 0, 9)

	requireWake(t, c, WakeResult{At: 2, Fired: []FiredEvent{{ID: "a"}}, Left: 0})
	requireWake(t, c, WakeResult{At: 8, Fired: []FiredEvent{{ID: "b"}}, Left: 0})
	requireWake(t, c, WakeResult{At: 12, Fired: []FiredEvent{{ID: "c", Late: 3}, {ID: "a"}}, Left: 1})
	requireWake(t, c, WakeResult{At: 16, Fired: []FiredEvent{{ID: "b", Late: 2}, {ID: "c", Late: 2}}, Left: 0})
	requireWake(t, c, WakeResult{At: 20, Fired: []FiredEvent{{ID: "c", Late: 1}, {ID: "b"}}, Left: 1})
}

func TestCatchUpSkips(t *testing.T) {
	c, err := NewCoalescer(10, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c, "x", 3, 0, 0)
	requireWake(t, c, WakeResult{At: 0, Fired: []FiredEvent{{ID: "x"}}})
	requireWake(t, c, WakeResult{At: 10, Fired: []FiredEvent{{ID: "x", Late: 7, Skip: 2}}, Left: 0})

	next, err := c.Next()
	if err != nil || next != 20 {
		t.Fatalf("Next() = (%d, %v), want (20, nil)", next, err)
	}
	stats := c.Stats()
	if stats.WakeCount != 2 || stats.FiredCount != 2 || stats.LateCount != 1 || stats.SkippedCount != 2 {
		t.Fatalf("Stats() = %+v", stats)
	}
}

func TestInvalidArguments(t *testing.T) {
	cases := []struct {
		name  string
		gap   uint64
		batch int
	}{
		{name: "gap too large", gap: maxInterval + 1, batch: 1},
		{name: "batch zero", gap: 0, batch: 0},
		{name: "batch too large", gap: 0, batch: maxTimerCount + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewCoalescer(tc.gap, tc.batch); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("NewCoalescer(%d,%d) error = %v", tc.gap, tc.batch, err)
			}
		})
	}

	c, err := NewCoalescer(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	addCases := []struct {
		id     string
		period uint64
		slack  uint64
		next   uint64
	}{
		{"", 1, 0, 0},
		{"33-bytes-id-abcdefghijklmnopqrstuv", 1, 0, 0},
		{"zero-period", 0, 0, 0},
		{"large-period", maxInterval + 1, 0, 0},
		{"slack-equal", 1, 1, 0},
		{"large-next", 1, 0, 100_000_000_000_001},
	}
	for _, tc := range addCases {
		if err := c.Add(tc.id, tc.period, tc.slack, tc.next); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Add(%+v) error = %v", tc, err)
		}
	}
	if _, err := c.AdvanceTo(100_000_000_000_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AdvanceTo overflow error = %v", err)
	}
	if _, err := c.Next(); !errors.Is(err, ErrNoTimers) {
		t.Fatalf("Next() no timer error = %v", err)
	}
	if _, err := c.Wake(); !errors.Is(err, ErrNoTimers) {
		t.Fatalf("Wake() no timer error = %v", err)
	}
}

func TestCandidateBoundaryAndNoLateAtDeadline(t *testing.T) {
	c, err := NewCoalescer(0, 3)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c, "equal", 100, 10, 5)
	mustAdd(t, c, "one-late", 100, 0, 16)
	mustAdd(t, c, "deadline", 100, 5, 10)

	requireWake(t, c, WakeResult{At: 15, Fired: []FiredEvent{{ID: "deadline"}, {ID: "equal"}}, Left: 0})
	next, err := c.Next()
	if err != nil || next != 16 {
		t.Fatalf("Next() = (%d, %v), want 16", next, err)
	}
	requireWake(t, c, WakeResult{At: 16, Fired: []FiredEvent{{ID: "one-late"}}})
	if err := c.Remove("equal"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove("deadline"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove("one-late"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Next(); !errors.Is(err, ErrNoTimers) {
		t.Fatalf("Next() after all fired: %v", err)
	}
}

func TestGridExactlyMeetsDeadlineAndZeroGap(t *testing.T) {
	c, err := NewCoalescer(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c, "first", 100, 0, 1)
	requireWake(t, c, WakeResult{At: 1, Fired: []FiredEvent{{ID: "first"}}})
	mustAdd(t, c, "second", 100, 0, 5)
	next, err := c.Next()
	if err != nil || next != 5 {
		t.Fatalf("Next() = (%d,%v), want 5", next, err)
	}
	requireWake(t, c, WakeResult{At: 5, Fired: []FiredEvent{{ID: "second"}}})

	c2, err := NewCoalescer(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c2, "z", 5, 0, 0)
	requireWake(t, c2, WakeResult{At: 0, Fired: []FiredEvent{{ID: "z"}}})
	requireWake(t, c2, WakeResult{At: 5, Fired: []FiredEvent{{ID: "z"}}})
}

func TestRejectionOrderAndNoStateChange(t *testing.T) {
	c, err := NewCoalescer(4, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c, "a", 10, 0, 0)
	requireWake(t, c, WakeResult{At: 0, Fired: []FiredEvent{{ID: "a"}}})

	if err := c.Add("", 10, 0, 4); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty id error = %v", err)
	}
	if err := c.Add("a", 10, 0, 4); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate error = %v", err)
	}
	mustAdd(t, c, "b", 100, 0, 4)
	results, err := c.AdvanceTo(4)
	if err != nil || len(results) != 1 {
		t.Fatalf("AdvanceTo(4) = (%+v, %v), want one wake", results, err)
	}
	if err := c.Add("old", 10, 0, 3); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback error = %v", err)
	}
	if err := c.Remove("missing"); !errors.Is(err, ErrTimerNotFound) {
		t.Fatalf("missing remove error = %v", err)
	}
	if _, err := c.AdvanceTo(3); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("advance rollback error = %v", err)
	}
}
