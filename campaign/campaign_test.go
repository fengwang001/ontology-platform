package campaign

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, tv int, m []int, c, r, b, d, f int) *Campaign {
	t.Helper()
	cp, err := New(tv, m, c, r, b, d, f)
	if err != nil {
		t.Fatalf("New(%d,%v,%d,%d,%d,%d,%d): %v", tv, m, c, r, b, d, f, err)
	}
	return cp
}

func mustAdd(t *testing.T, c *Campaign, now int64, id string, v int) {
	t.Helper()
	if err := c.AddDevice(now, id, v); err != nil {
		t.Fatalf("AddDevice(%d,%q,%d): %v", now, id, v, err)
	}
}

func mustDispatch(t *testing.T, c *Campaign, now int64, n int, want []Assignment) {
	t.Helper()
	got, err := c.Dispatch(now, n)
	if err != nil {
		t.Fatalf("Dispatch(%d,%d): %v", now, n, err)
	}
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Dispatch(%d,%d) = %v, want %v", now, n, got, want)
	}
}

func mustReport(t *testing.T, c *Campaign, now int64, id string, tok uint64, ver int, ok bool) {
	t.Helper()
	if err := c.Report(now, id, tok, ver, ok); err != nil {
		t.Fatalf("Report(%d,%q,%d,%d,%v): %v", now, id, tok, ver, ok, err)
	}
}

func wantErr(t *testing.T, got, want error, op string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s = %v, want %v", op, got, want)
	}
}

func wantState(t *testing.T, c *Campaign, id string, want State) {
	t.Helper()
	got, ok := c.StateOf(id)
	if !ok {
		t.Fatalf("StateOf(%q): device missing", id)
	}
	if got != want {
		t.Fatalf("StateOf(%q) = %v, want %v", id, got, want)
	}
}

// dev is a package-internal accessor for white-box assertions.
func (c *Campaign) dev(id string) *device { return c.devices[id] }

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	c := mustNew(t, 6, []int{3, 5}, 2, 2, 10, 100, 2)
	mustAdd(t, c, 0, "a", 1)
	mustAdd(t, c, 0, "b", 3)
	mustAdd(t, c, 0, "c", 5)
	mustAdd(t, c, 0, "d", 6)
	wantState(t, c, "d", Skipped)

	// c stays behind: only 2 slots.
	mustDispatch(t, c, 0, 5, []Assignment{{"a", 3, 1}, {"b", 5, 2}})
	mustReport(t, c, 10, "a", 1, 3, true)
	if got := c.dev("a").readyAt; got != 10 {
		t.Fatalf("a.readyAt = %d, want 10", got)
	}
	mustDispatch(t, c, 10, 5, []Assignment{{"c", 6, 3}})

	// b hits dl=100 exactly: timeout counts, failure time is dl.
	mustDispatch(t, c, 100, 1, []Assignment{{"a", 5, 4}})
	if got := c.dev("b").readyAt; got != 110 {
		t.Fatalf("b.readyAt = %d, want 110 (dl+B*attempts)", got)
	}
	if got := c.dev("b").attempts; got != 1 {
		t.Fatalf("b.attempts = %d, want 1", got)
	}

	// c's report lands exactly at dl=110: entry settlement times c out,
	// the report is rejected and the settlement is rolled back.
	wantErr(t, c.Report(110, "c", 3, 6, true), ErrNotInFlight, "Report c@110")
	wantState(t, c, "c", InFlight)
	if got := c.dev("c").attempts; got != 0 {
		t.Fatalf("c.attempts = %d after rollback, want 0", got)
	}

	// The next accepted operation lands the same settlement.
	mustDispatch(t, c, 110, 1, []Assignment{{"b", 5, 5}})
	wantState(t, c, "c", Pending)
	if got := c.dev("c").readyAt; got != 120 {
		t.Fatalf("c.readyAt = %d, want 120 (dl+B*attempts)", got)
	}
}

// TestTimeoutFailureTimeUsesDeadline checks that a timeout settled long
// after dl still uses dl as the failure time.
func TestTimeoutFailureTimeUsesDeadline(t *testing.T) {
	c := mustNew(t, 6, nil, 1, 3, 10, 100, 5)
	mustAdd(t, c, 0, "a", 1)
	mustDispatch(t, c, 0, 1, []Assignment{{"a", 6, 1}})
	// now=550 >> dl=100: backoff is dl + B*1 = 110, not 560.
	mustAdd(t, c, 550, "b", 1) // accepted op lands the settlement
	if got := c.dev("a").readyAt; got != 110 {
		t.Fatalf("a.readyAt = %d, want 110", got)
	}
	wantState(t, c, "a", Pending)
}

// TestAbortInflightOutcomes covers the three post-abort endings of an
// in-flight device: Done at T, Cancelled at a mandatory hop, Failed.
func TestAbortInflightOutcomes(t *testing.T) {
	c := mustNew(t, 6, []int{3}, 3, 1, 10, 1000, 1)
	mustAdd(t, c, 0, "a", 1)
	mustAdd(t, c, 0, "b", 1)
	mustAdd(t, c, 0, "g", 3) // exactly on a mandatory version
	mustAdd(t, c, 0, "p", 1)
	mustDispatch(t, c, 0, 3, []Assignment{{"a", 3, 1}, {"b", 3, 2}, {"g", 6, 3}})

	// a fails with R=1: Failed, breaker trips (F=1), p is Cancelled.
	mustReport(t, c, 10, "a", 1, 0, false)
	wantState(t, c, "a", Failed)
	wantState(t, c, "p", Cancelled)
	if !c.Aborted() {
		t.Fatal("breaker must trip at F=1")
	}
	_, err := c.Dispatch(20, 1)
	wantErr(t, err, ErrAborted, "Dispatch after abort")

	// b reaches a mandatory hop (not T): Cancelled, version kept at 3.
	mustReport(t, c, 30, "b", 2, 3, true)
	wantState(t, c, "b", Cancelled)
	if got := c.dev("b").version; got != 3 {
		t.Fatalf("b.version = %d, want 3", got)
	}

	// g reaches T: Done.
	mustReport(t, c, 40, "g", 3, 6, true)
	wantState(t, c, "g", Done)

	// Devices added after abort are Cancelled; v>=T still Skipped.
	mustAdd(t, c, 50, "late", 1)
	wantState(t, c, "late", Cancelled)
	mustAdd(t, c, 50, "new", 6)
	wantState(t, c, "new", Skipped)

	n := c.Counts()
	if n.Total() != 6 {
		t.Fatalf("Counts total = %d, want 6 (%+v)", n.Total(), n)
	}
}

// TestAbortInflightFailure checks that an in-flight device failing after
// abort becomes Failed (attempts reach R) or Cancelled, never Pending.
func TestAbortInflightFailure(t *testing.T) {
	c := mustNew(t, 6, nil, 3, 2, 10, 1000, 1)
	mustAdd(t, c, 0, "x", 1)
	mustAdd(t, c, 0, "y", 1)
	mustAdd(t, c, 0, "z", 1)
	mustDispatch(t, c, 0, 3, []Assignment{{"x", 6, 1}, {"y", 6, 2}, {"z", 6, 3}})
	mustReport(t, c, 5, "x", 1, 0, false) // x: attempts=1, back to Pending
	mustDispatch(t, c, 15, 1, []Assignment{{"x", 6, 4}})
	mustReport(t, c, 20, "x", 4, 0, false) // x: attempts=2=R -> Failed, abort trips
	if !c.Aborted() {
		t.Fatal("breaker must trip")
	}
	wantState(t, c, "x", Failed)
	// attempts 1 < R=2, but aborted -> Cancelled, never back to Pending.
	mustReport(t, c, 25, "y", 2, 0, false)
	wantState(t, c, "y", Cancelled)
	mustReport(t, c, 26, "z", 3, 0, false)
	wantState(t, c, "z", Cancelled)
}

// TestStaleToken covers token mismatch after re-dispatch.
func TestStaleToken(t *testing.T) {
	c := mustNew(t, 6, nil, 1, 2, 10, 100, 5)
	mustAdd(t, c, 0, "a", 1)
	mustDispatch(t, c, 0, 1, []Assignment{{"a", 6, 1}})
	mustReport(t, c, 10, "a", 1, 0, false) // back to Pending, readyAt=20
	mustDispatch(t, c, 20, 1, []Assignment{{"a", 6, 2}})
	wantErr(t, c.Report(30, "a", 1, 6, true), ErrStale, "old token")
	mustReport(t, c, 30, "a", 2, 6, true)
	wantState(t, c, "a", Done)
}

// TestRejectionOrder pins the error precedence chain.
func TestRejectionOrder(t *testing.T) {
	c := mustNew(t, 6, nil, 1, 1, 10, 100, 5)
	mustAdd(t, c, 10, "a", 1)

	// ErrInvalid before ErrClockBack: bad id AND old clock.
	wantErr(t, c.AddDevice(5, "", 1), ErrInvalid, "invalid+clockback")
	// ErrClockBack before ErrExists: duplicate id AND old clock.
	wantErr(t, c.AddDevice(5, "a", 1), ErrClockBack, "clockback+exists")
	// ErrExists.
	wantErr(t, c.AddDevice(10, "a", 1), ErrExists, "exists")
	// ErrUnknown before ErrNotInFlight.
	wantErr(t, c.Report(10, "ghost", 1, 6, true), ErrUnknown, "unknown")
	// ErrNotInFlight: a is Pending.
	wantErr(t, c.Report(10, "a", 1, 6, true), ErrNotInFlight, "pending report")

	mustDispatch(t, c, 10, 1, []Assignment{{"a", 6, 1}})
	// ErrStale before ErrVersion: wrong token AND wrong version.
	wantErr(t, c.Report(20, "a", 99, 5, true), ErrStale, "stale+version")
	// ErrVersion.
	wantErr(t, c.Report(20, "a", 1, 5, true), ErrVersion, "version")
	// Accepted report advances the clock to 20.
	mustReport(t, c, 20, "a", 1, 6, true)
	wantState(t, c, "a", Done)
	// Rejections never advanced the clock beyond 20.
	wantErr(t, c.Report(15, "a", 1, 6, true), ErrClockBack, "clockback after rejects")
}

// TestDispatchOrdering checks (readyAt, id) byte order and slot bounds.
func TestDispatchOrdering(t *testing.T) {
	c := mustNew(t, 9, nil, 2, 1, 10, 100, 5)
	mustAdd(t, c, 0, "z", 1)
	mustAdd(t, c, 0, "a", 1)
	mustAdd(t, c, 5, "b", 1)
	// readyAt: z=0, a=0, b=5 -> order a, z (id order), b.
	mustDispatch(t, c, 10, 3, []Assignment{{"a", 9, 1}, {"z", 9, 2}})
	if c.slots.Free() != 0 {
		t.Fatalf("free slots = %d, want 0", c.slots.Free())
	}
}

// TestPoppedCounters proves heap pops stay within handled+1 regardless
// of total device count (100 vs 10000, same 3 dispatches).
func TestPoppedCounters(t *testing.T) {
	for _, total := range []int{100, 10_000} {
		t.Run(fmt.Sprintf("n=%d", total), func(t *testing.T) {
			c := mustNew(t, 9, nil, 3, 1, 10, 100, total+1)
			for i := 0; i < total; i++ {
				mustAdd(t, c, 0, fmt.Sprintf("dev%05d", i), 1)
			}
			got, err := c.Dispatch(0, 3)
			if err != nil || len(got) != 3 {
				t.Fatalf("Dispatch = %v, %v", got, err)
			}
			if c.poppedReady > len(got)+1 {
				t.Fatalf("poppedReady = %d, want <= %d", c.poppedReady, len(got)+1)
			}
			// Entry settlement with no expired device: zero pops.
			before := c.poppedExpiry
			if _, err := c.Dispatch(50, 3); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if c.poppedExpiry-before > 1 {
				t.Fatalf("poppedExpiry delta = %d, want <= 1", c.poppedExpiry-before)
			}
		})
	}
}

// TestCountsInvariant checks the six-state sum at every step.
func TestCountsInvariant(t *testing.T) {
	c := mustNew(t, 5, []int{2}, 2, 1, 10, 50, 2)
	total := 0
	check := func() {
		t.Helper()
		if n := c.Counts(); n.Total() != total {
			t.Fatalf("counts total = %d, want %d (%+v)", n.Total(), total, n)
		}
	}
	mustAdd(t, c, 0, "a", 1)
	total++
	check()
	mustAdd(t, c, 0, "b", 5)
	total++
	check()
	mustDispatch(t, c, 0, 2, []Assignment{{"a", 2, 1}})
	check()
	mustReport(t, c, 10, "a", 1, 2, true)
	check()
	mustDispatch(t, c, 10, 1, []Assignment{{"a", 5, 2}})
	check()
	// Timeout at dl=60 with R=1: Failed; F=2 not yet reached.
	mustDispatch(t, c, 60, 1, nil)
	wantState(t, c, "a", Failed)
	check()
}
