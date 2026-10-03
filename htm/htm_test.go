package htm

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, cfg Config) *Controller {
	t.Helper()
	c, err := NewController(cfg)
	if err != nil {
		t.Fatalf("NewController(%+v) rejected: %v", cfg, err)
	}
	return c
}

func lock(t *testing.T, c *Controller, thread int) LockResult {
	t.Helper()
	res, err := c.Lock(thread)
	if err != nil {
		t.Fatalf("Lock(%d) rejected: %v", thread, err)
	}
	t.Logf("Lock(%d) -> rule=%v aborted=%v", thread, res.Rule, res.Aborted)
	return res
}

func access(t *testing.T, c *Controller, thread, addr int, write bool) AccessResult {
	t.Helper()
	res, err := c.Access(thread, addr, write)
	if err != nil {
		t.Fatalf("Access(%d,%d,%v) rejected: %v", thread, addr, write, err)
	}
	t.Logf("Access(%d,%d,write=%v) -> outcome=%v aborted=%v",
		thread, addr, write, res.Outcome, res.Aborted)
	return res
}

func unlock(t *testing.T, c *Controller, thread int) {
	t.Helper()
	if err := c.Unlock(thread); err != nil {
		t.Fatalf("Unlock(%d) rejected: %v", thread, err)
	}
	t.Logf("Unlock(%d) -> ok", thread)
}

func checkThread(t *testing.T, c *Controller, thread int, state State, reason AbortReason, r int) {
	t.Helper()
	snap := c.Snapshot()
	got := snap.Threads[thread]
	if got.State != state || got.Reason != reason || got.R != r {
		t.Fatalf("thread %d: got state=%v reason=%v r=%d, want state=%v reason=%v r=%d",
			thread, got.State, got.Reason, got.R, state, reason, r)
	}
}

func checkGlobals(t *testing.T, c *Controller, holder, skip, fb int) {
	t.Helper()
	snap := c.Snapshot()
	if snap.Holder != holder || snap.Skip != skip || snap.Fb != fb {
		t.Fatalf("globals: got holder=%d skip=%d fb=%d, want holder=%d skip=%d fb=%d",
			snap.Holder, snap.Skip, snap.Fb, holder, skip, fb)
	}
}

// TestSpecExample replays the walkthrough from the specification.
func TestSpecExample(t *testing.T) {
	c := mustNew(t, Config{N: 2, S: 2, W: 1, A: 4, R: 1, SK: 2, F: 2})

	if res := lock(t, c, 0); res.Rule != RuleSpeculate {
		t.Fatalf("T0 lock: got %v, want speculate", res.Rule)
	}
	if res := access(t, c, 0, 0, true); res.Outcome != AccessOK || len(res.Aborted) != 0 {
		t.Fatalf("T0 write 0: got %+v", res)
	}
	if res := lock(t, c, 1); res.Rule != RuleSpeculate {
		t.Fatalf("T1 lock: got %v, want speculate", res.Rule)
	}
	// T1 reads address 0: conflicts with T0's write set.
	if res := access(t, c, 1, 0, false); res.Outcome != AccessOK || !reflect.DeepEqual(res.Aborted, []int{0}) {
		t.Fatalf("T1 read 0: got %+v, want aborted=[0]", res)
	}
	checkThread(t, c, 0, Aborted, Conflict, 1)
	// T1 reads address 2: set 0 now holds {0,2}, exceeding W=1.
	if res := access(t, c, 1, 2, false); res.Outcome != AccessCapacityAbort {
		t.Fatalf("T1 read 2: got %+v, want capacity abort", res)
	}
	checkThread(t, c, 1, Aborted, Capacity, 0)
	checkGlobals(t, c, -1, 2, 0)
	// T0 lock: skip 2 -> 1, fallback via rule 3.
	if res := lock(t, c, 0); res.Rule != RuleSkip {
		t.Fatalf("T0 lock: got %v, want skip fallback", res.Rule)
	}
	checkGlobals(t, c, 0, 1, 0)
	// T1 lock: holder exists, waits without consuming skip or r.
	if res := lock(t, c, 1); res.Rule != RuleWait {
		t.Fatalf("T1 lock: got %v, want wait", res.Rule)
	}
	checkGlobals(t, c, 0, 1, 0)
	unlock(t, c, 0)
	// T1 lock: capacity-aborted, falls back via rule 2 without
	// decrementing skip; fb becomes 1 (F=2 not reached).
	if res := lock(t, c, 1); res.Rule != RuleCapacityAbort {
		t.Fatalf("T1 lock: got %v, want capacity fallback", res.Rule)
	}
	checkGlobals(t, c, 1, 1, 1)
	unlock(t, c, 1)
	checkGlobals(t, c, -1, 1, 1)
	// T0 lock: skip 1 -> 0, still fallback via rule 3.
	if res := lock(t, c, 0); res.Rule != RuleSkip {
		t.Fatalf("T0 lock: got %v, want skip fallback", res.Rule)
	}
	checkGlobals(t, c, 0, 0, 1)
	unlock(t, c, 0)
	// skip exhausted: T0 speculates again.
	if res := lock(t, c, 0); res.Rule != RuleSpeculate {
		t.Fatalf("T0 lock: got %v, want speculate", res.Rule)
	}
}

// TestReadThenWriteSameAddressCountsOnce: a read followed by a write of
// the same address contributes a single distinct address to its set.
func TestReadThenWriteSameAddressCountsOnce(t *testing.T) {
	c := mustNew(t, Config{N: 1, S: 1, W: 1, A: 4, R: 0, SK: 0, F: 1})
	lock(t, c, 0)
	if res := access(t, c, 0, 0, false); res.Outcome != AccessOK {
		t.Fatalf("read 0: got %+v", res)
	}
	if res := access(t, c, 0, 0, true); res.Outcome != AccessOK {
		t.Fatalf("write 0 after read 0: got %+v, want ok (one distinct address)", res)
	}
	if res := access(t, c, 0, 1, false); res.Outcome != AccessCapacityAbort {
		t.Fatalf("read 1: got %+v, want capacity abort ({0,1} > W=1)", res)
	}
}

// TestExactlyWaysPass: exactly W distinct addresses per set pass, W+1 aborts.
func TestExactlyWaysPass(t *testing.T) {
	c := mustNew(t, Config{N: 1, S: 2, W: 2, A: 8, R: 0, SK: 0, F: 1})
	lock(t, c, 0)
	// Addresses 0,2,4 map to set 0; 1,3 map to set 1.
	for _, a := range []int{0, 2, 1, 3} {
		if res := access(t, c, 0, a, false); res.Outcome != AccessOK {
			t.Fatalf("read %d: got %+v, want ok", a, res)
		}
	}
	if res := access(t, c, 0, 4, false); res.Outcome != AccessCapacityAbort {
		t.Fatalf("read 4: got %+v, want capacity abort (3 > W=2 in set 0)", res)
	}
	checkThread(t, c, 0, Aborted, Capacity, 0)
}

// TestCapacityAbortStillAbortsConflictFirst: on a write access that
// capacity-aborts the writer, conflicting threads are aborted first and
// reported in the same result.
func TestCapacityAbortStillAbortsConflictFirst(t *testing.T) {
	c := mustNew(t, Config{N: 2, S: 1, W: 1, A: 4, R: 0, SK: 3, F: 1})
	lock(t, c, 0)
	access(t, c, 0, 1, false) // T0 reads 1
	lock(t, c, 1)
	access(t, c, 1, 0, true) // T1 writes 0 (no conflict with T0's read of 1)
	// T1 writes 1: conflicts with T0's read set, then T1's write set
	// {0,1} overflows W=1 in set 0.
	res := access(t, c, 1, 1, true)
	if res.Outcome != AccessCapacityAbort {
		t.Fatalf("T1 write 1: got outcome %v, want capacity abort", res.Outcome)
	}
	if !reflect.DeepEqual(res.Aborted, []int{0}) {
		t.Fatalf("T1 write 1: got aborted=%v, want [0]", res.Aborted)
	}
	checkThread(t, c, 0, Aborted, Conflict, 1)
	checkThread(t, c, 1, Aborted, Capacity, 0)
	checkGlobals(t, c, -1, 3, 0)
}

// TestWaitConsumesNothing: a waiting Lock changes no state at all.
func TestWaitConsumesNothing(t *testing.T) {
	c := mustNew(t, Config{N: 2, S: 1, W: 2, A: 4, R: 1, SK: 2, F: 2})
	lock(t, c, 0)
	access(t, c, 0, 0, false)
	lock(t, c, 1)
	access(t, c, 1, 0, true) // T0 conflict-aborted, r=1
	access(t, c, 1, 1, false)
	access(t, c, 1, 2, false) // T1 capacity-aborted, skip=2
	lock(t, c, 0)             // rule 3: skip 2->1, T0 fallback
	checkGlobals(t, c, 0, 1, 0)
	before := c.Snapshot()
	if res := lock(t, c, 1); res.Rule != RuleWait {
		t.Fatalf("T1 lock: got %v, want wait", res.Rule)
	}
	after := c.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("wait mutated state:\nbefore=%+v\nafter=%+v", before, after)
	}
}

// TestCapacityFallbackKeepsSkip: rule 2 fallback does not decrement skip.
func TestCapacityFallbackKeepsSkip(t *testing.T) {
	c := mustNew(t, Config{N: 1, S: 1, W: 1, A: 2, R: 0, SK: 2, F: 2})
	lock(t, c, 0)
	access(t, c, 0, 0, false)
	access(t, c, 0, 1, false) // capacity abort, skip=2
	checkGlobals(t, c, -1, 2, 0)
	if res := lock(t, c, 0); res.Rule != RuleCapacityAbort {
		t.Fatalf("lock: got %v, want capacity fallback", res.Rule)
	}
	checkGlobals(t, c, 0, 2, 1) // skip untouched, fb=1
}

// TestRetryBudgetBoundary: r == R still speculates, r == R+1 falls back.
func TestRetryBudgetBoundary(t *testing.T) {
	// R=0: a single conflict abort already exceeds the budget.
	// F=1: the first rule-4 fallback completes the streak, so fb resets
	// to 0 and skip is set to SK=1.
	c := mustNew(t, Config{N: 2, S: 1, W: 4, A: 4, R: 0, SK: 1, F: 1})
	lock(t, c, 0)
	access(t, c, 0, 0, false)
	lock(t, c, 1)
	access(t, c, 1, 0, true) // T0 conflict-aborted, r=1
	unlock(t, c, 1)
	if res := lock(t, c, 0); res.Rule != RuleRetryBudget {
		t.Fatalf("R=0, r=1: got %v, want retry-budget fallback", res.Rule)
	}
	checkGlobals(t, c, 0, 1, 0)

	// R=1: r=1 speculates, r=2 falls back.
	c = mustNew(t, Config{N: 2, S: 1, W: 4, A: 4, R: 1, SK: 0, F: 2})
	lock(t, c, 0)
	access(t, c, 0, 0, false)
	lock(t, c, 1)
	access(t, c, 1, 0, true) // T0 conflict-aborted, r=1
	unlock(t, c, 1)
	if res := lock(t, c, 0); res.Rule != RuleSpeculate {
		t.Fatalf("R=1, r=1: got %v, want speculate", res.Rule)
	}
	access(t, c, 0, 0, false)
	lock(t, c, 1)
	access(t, c, 1, 0, true) // T0 conflict-aborted, r=2
	unlock(t, c, 1)
	if res := lock(t, c, 0); res.Rule != RuleRetryBudget {
		t.Fatalf("R=1, r=2: got %v, want retry-budget fallback", res.Rule)
	}
	checkGlobals(t, c, 0, 0, 1) // rule 4 counts fb
}

// TestSkipZeroLockOrder: with skip == 0, idle threads speculate freely.
func TestSkipZeroLockOrder(t *testing.T) {
	c := mustNew(t, Config{N: 2, S: 1, W: 4, A: 4, R: 0, SK: 0, F: 1})
	if res := lock(t, c, 0); res.Rule != RuleSpeculate {
		t.Fatalf("T0: got %v, want speculate", res.Rule)
	}
	if res := lock(t, c, 1); res.Rule != RuleSpeculate {
		t.Fatalf("T1: got %v, want speculate", res.Rule)
	}
	checkGlobals(t, c, -1, 0, 0)
}

// TestUnlockClears: both commit and fallback unlock clear r, but only a
// speculative commit clears fb.
func TestUnlockClears(t *testing.T) {
	c := mustNew(t, Config{N: 2, S: 1, W: 1, A: 4, R: 0, SK: 0, F: 2})
	lock(t, c, 0)
	access(t, c, 0, 0, false)
	lock(t, c, 1)
	access(t, c, 1, 0, true)  // T0 conflict-aborted, r=1
	access(t, c, 1, 1, false) // T1 capacity-aborted (read set {0,1} > W=1)
	// T0: r=1 > R=0 -> rule 4 fallback, fb=1.
	if res := lock(t, c, 0); res.Rule != RuleRetryBudget {
		t.Fatalf("T0 lock: got %v, want retry-budget fallback", res.Rule)
	}
	checkGlobals(t, c, 0, 0, 1)
	unlock(t, c, 0) // fallback unlock: clears r, keeps fb
	checkThread(t, c, 0, Idle, NoAbort, 0)
	checkGlobals(t, c, -1, 0, 1)
	// T0 speculates and commits: fb cleared.
	lock(t, c, 0)
	access(t, c, 0, 2, false)
	unlock(t, c, 0)
	checkThread(t, c, 0, Idle, NoAbort, 0)
	checkGlobals(t, c, -1, 0, 0)
}

// TestFallbackStreakSetsSkip: the F-th fallback via rules 2/4 sets
// skip=SK and resets fb; rule 3 fallbacks do not count fb.
func TestFallbackStreakSetsSkip(t *testing.T) {
	c := mustNew(t, Config{N: 2, S: 1, W: 2, A: 4, R: 0, SK: 2, F: 2})
	// abortT0 makes T0 speculate and then conflict-abort (r=1) via T1.
	// T1 never commits here: T0's fallback aborts it with LockHeld, so
	// the fb streak is not reset by a speculative commit.
	abortT0 := func() {
		lock(t, c, 0)
		access(t, c, 0, 0, false)
		lock(t, c, 1)
		access(t, c, 1, 0, true) // T0 conflict-aborted, r=1
	}
	// First rule-4 fallback: fb=1, T1 aborted with LockHeld.
	abortT0()
	res := lock(t, c, 0)
	if res.Rule != RuleRetryBudget {
		t.Fatalf("T0 lock: got %v, want retry-budget fallback", res.Rule)
	}
	if !reflect.DeepEqual(res.Aborted, []int{1}) {
		t.Fatalf("T0 fallback: got aborted=%v, want [1]", res.Aborted)
	}
	checkGlobals(t, c, 0, 0, 1)
	unlock(t, c, 0)
	checkGlobals(t, c, -1, 0, 1)
	// Second rule-4 fallback: fb reaches F=2 -> skip=SK=2, fb=0.
	abortT0()
	if res := lock(t, c, 0); res.Rule != RuleRetryBudget {
		t.Fatalf("T0 lock: got %v, want retry-budget fallback", res.Rule)
	}
	checkGlobals(t, c, 0, 2, 0)
	unlock(t, c, 0)
	// Rule-3 fallbacks consume skip but never count fb.
	if res := lock(t, c, 0); res.Rule != RuleSkip {
		t.Fatalf("T0 lock: got %v, want skip fallback", res.Rule)
	}
	checkGlobals(t, c, 0, 1, 0)
	unlock(t, c, 0)
	if res := lock(t, c, 0); res.Rule != RuleSkip {
		t.Fatalf("T0 lock: got %v, want skip fallback", res.Rule)
	}
	checkGlobals(t, c, 0, 0, 0)
	unlock(t, c, 0)
	if res := lock(t, c, 0); res.Rule != RuleSpeculate {
		t.Fatalf("T0 lock: got %v, want speculate", res.Rule)
	}
}

// TestFallbackAccessIsNoOp: a fallback thread's Access has no effect.
func TestFallbackAccessIsNoOp(t *testing.T) {
	c := mustNew(t, Config{N: 2, S: 1, W: 1, A: 4, R: 0, SK: 1, F: 1})
	lock(t, c, 0)
	access(t, c, 0, 0, false)
	access(t, c, 0, 1, false) // capacity abort, skip=1
	lock(t, c, 0)             // rule 2 fallback
	before := c.Snapshot()
	res := access(t, c, 0, 3, true)
	if res.Outcome != AccessFallbackNoOp || len(res.Aborted) != 0 {
		t.Fatalf("fallback access: got %+v, want no-op", res)
	}
	if after := c.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("fallback access mutated state:\nbefore=%+v\nafter=%+v", before, after)
	}
}

// TestLockHeldAbortThenRelock: a thread aborted by LockHeld can re-lock
// and speculates again; the lock-held abort did not bump its r.
func TestLockHeldAbortThenRelock(t *testing.T) {
	c := mustNew(t, Config{N: 2, S: 1, W: 1, A: 4, R: 0, SK: 0, F: 1})
	lock(t, c, 0)
	access(t, c, 0, 0, false)
	lock(t, c, 1)
	access(t, c, 1, 2, false)
	access(t, c, 1, 3, false) // T1 capacity abort
	res := lock(t, c, 1)      // rule 2 fallback aborts T0 with LockHeld
	if res.Rule != RuleCapacityAbort || !reflect.DeepEqual(res.Aborted, []int{0}) {
		t.Fatalf("T1 fallback: got %+v, want aborted=[0]", res)
	}
	checkThread(t, c, 0, Aborted, LockHeld, 0) // r untouched
	if res := lock(t, c, 0); res.Rule != RuleWait {
		t.Fatalf("T0 lock: got %v, want wait", res.Rule)
	}
	unlock(t, c, 1)
	if res := lock(t, c, 0); res.Rule != RuleSpeculate {
		t.Fatalf("T0 relock: got %v, want speculate", res.Rule)
	}
	checkThread(t, c, 0, Speculating, NoAbort, 0)
}

// TestRejections: rejection reasons are reported in the fixed order
// thread -> state -> address, and rejected calls change nothing.
func TestRejections(t *testing.T) {
	c := mustNew(t, Config{N: 2, S: 1, W: 2, A: 4, R: 0, SK: 0, F: 1})
	lock(t, c, 0) // T0 speculating, T1 idle
	access(t, c, 0, 0, false)

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"lock bad thread", func() error { _, err := c.Lock(-1); return err }, ErrInvalidThread},
		{"lock bad thread high", func() error { _, err := c.Lock(2); return err }, ErrInvalidThread},
		{"lock bad state", func() error { _, err := c.Lock(0); return err }, ErrInvalidState},
		{"access bad thread", func() error { _, err := c.Access(9, 0, false); return err }, ErrInvalidThread},
		{"access bad state", func() error { _, err := c.Access(1, 0, false); return err }, ErrInvalidState},
		{"access state before address", func() error { _, err := c.Access(1, 99, false); return err }, ErrInvalidState},
		{"access bad address low", func() error { _, err := c.Access(0, -1, false); return err }, ErrInvalidAddress},
		{"access bad address high", func() error { _, err := c.Access(0, 4, false); return err }, ErrInvalidAddress},
		{"unlock bad thread", func() error { return c.Unlock(-1) }, ErrInvalidThread},
		{"unlock idle", func() error { return c.Unlock(1) }, ErrInvalidState},
	}
	for _, tc := range cases {
		before := c.Snapshot()
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got err=%v, want %v", tc.name, err, tc.want)
		}
		if after := c.Snapshot(); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: rejected call mutated state", tc.name)
		}
	}

	// Aborted thread cannot unlock or access.
	lock(t, c, 1)
	access(t, c, 1, 0, true) // T0 conflict-aborted
	if err := c.Unlock(0); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("unlock aborted: got %v, want ErrInvalidState", err)
	}
	if _, err := c.Access(0, 1, false); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("access aborted: got %v, want ErrInvalidState", err)
	}
}

// TestConfigValidation: boundary configs pass, any out-of-range
// parameter rejects the whole configuration.
func TestConfigValidation(t *testing.T) {
	valid := []Config{
		{N: 1, S: 1, W: 1, A: 1, R: 0, SK: 0, F: 1},
		{N: 16, S: 8, W: 4, A: 64, R: 8, SK: 8, F: 8},
	}
	for _, cfg := range valid {
		if _, err := NewController(cfg); err != nil {
			t.Fatalf("valid config %+v rejected: %v", cfg, err)
		}
	}
	base := Config{N: 2, S: 2, W: 2, A: 4, R: 1, SK: 1, F: 1}
	bad := []Config{}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.N = 0 }, func(c *Config) { c.N = 17 },
		func(c *Config) { c.S = 0 }, func(c *Config) { c.S = 9 },
		func(c *Config) { c.W = 0 }, func(c *Config) { c.W = 5 },
		func(c *Config) { c.A = 0 }, func(c *Config) { c.A = 65 },
		func(c *Config) { c.R = -1 }, func(c *Config) { c.R = 9 },
		func(c *Config) { c.SK = -1 }, func(c *Config) { c.SK = 9 },
		func(c *Config) { c.F = 0 }, func(c *Config) { c.F = 9 },
	} {
		cfg := base
		mutate(&cfg)
		bad = append(bad, cfg)
	}
	for _, cfg := range bad {
		if _, err := NewController(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("config %+v: got err=%v, want ErrInvalidConfig", cfg, err)
		}
	}
}
