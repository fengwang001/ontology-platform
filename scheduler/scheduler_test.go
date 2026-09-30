package scheduler_test

import (
	"errors"
	"math/big"
	"sync"
	"testing"

	"ontology/scheduler"
)

func mustRegister(t *testing.T, s *scheduler.Scheduler, ts int64, id string, share int64) {
	t.Helper()
	t.Logf("input: Register(t=%d, account=%q, share=%d)", ts, id, share)
	if err := s.Register(ts, id, share); err != nil {
		t.Fatalf("Register(%q) failed: %v", id, err)
	}
}

func mustSubmit(t *testing.T, s *scheduler.Scheduler, ts int64, account string, job scheduler.Job) {
	t.Helper()
	t.Logf("input: Submit(t=%d, account=%q, job=%+v)", ts, account, job)
	if err := s.Submit(ts, account, job); err != nil {
		t.Fatalf("Submit(%q, %q) failed: %v", account, job.ID, err)
	}
}

func mustDispatch(t *testing.T, s *scheduler.Scheduler, ts int64) *scheduler.DispatchResult {
	t.Helper()
	t.Logf("input: Dispatch(t=%d)", ts)
	res, err := s.Dispatch(ts)
	if err != nil {
		t.Fatalf("Dispatch(t=%d) failed: %v", ts, err)
	}
	t.Logf("output: job=%+v account=%q boundariesApplied=%d", res.Job, res.AccountID, res.BoundariesApplied)
	for _, c := range res.Candidates {
		t.Logf("basis: candidate account=%q U=%s share=%d", c.AccountID, c.Usage, c.Share)
	}
	t.Logf("basis: reason=%s", res.Reason)
	return res
}

func mustSnapshot(t *testing.T, s *scheduler.Scheduler, ts int64) scheduler.Snapshot {
	t.Helper()
	t.Logf("input: Snapshot(t=%d)", ts)
	snap, err := s.Snapshot(ts)
	if err != nil {
		t.Fatalf("Snapshot(t=%d) failed: %v", ts, err)
	}
	for _, a := range snap.Accounts {
		t.Logf("output: account=%q share=%d U=%s queue=%v", a.AccountID, a.Share, a.Usage, a.Queue)
	}
	t.Logf("output: boundariesApplied=%d lastTime=%d", snap.BoundariesApplied, snap.LastTime)
	return snap
}

func usageOf(t *testing.T, snap scheduler.Snapshot, accountID string) *big.Int {
	t.Helper()
	for _, a := range snap.Accounts {
		if a.AccountID == accountID {
			return a.Usage
		}
	}
	t.Fatalf("account %q missing from snapshot", accountID)
	return nil
}

func newScheduler(t *testing.T, t0, period int64) *scheduler.Scheduler {
	t.Helper()
	t.Logf("input: New(t0=%d, period=%d)", t0, period)
	s, err := scheduler.New(t0, period)
	if err != nil {
		t.Fatalf("New(%d, %d) failed: %v", t0, period, err)
	}
	return s
}

// A call made exactly on a boundary must observe that boundary as applied.
func TestBoundaryAppliesAtExactBoundaryTime(t *testing.T) {
	s := newScheduler(t, 1000, 100) // boundaries: 1100, 1200, ...
	mustRegister(t, s, 1000, "a", 1)
	mustSubmit(t, s, 1000, "a", scheduler.Job{ID: "j1", Cost: 7})
	mustDispatch(t, s, 1000) // U(a) = 7

	snap := mustSnapshot(t, s, 1099)
	if got := usageOf(t, snap, "a"); got.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("t=1099: want U=7 (no boundary yet), got %s", got)
	}
	if snap.BoundariesApplied != 0 {
		t.Fatalf("t=1099: want 0 boundaries applied, got %d", snap.BoundariesApplied)
	}

	snap = mustSnapshot(t, s, 1100) // exactly on the boundary
	if got := usageOf(t, snap, "a"); got.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("t=1100: want U=3 (floor(7/2)), got %s", got)
	}
	if snap.BoundariesApplied != 1 {
		t.Fatalf("t=1100: want 1 boundary applied, got %d", snap.BoundariesApplied)
	}

	snap = mustSnapshot(t, s, 1100) // same instant again: no double application
	if got := usageOf(t, snap, "a"); got.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("t=1100 repeat: want U=3, got %s", got)
	}
}

// One call crossing k boundaries must equal k sequential halvings.
func TestMultiBoundaryJumpEqualsSequentialHalving(t *testing.T) {
	const t0, period = 0, 10

	jump := newScheduler(t, t0, period)
	mustRegister(t, jump, t0, "a", 1)
	mustSubmit(t, jump, t0, "a", scheduler.Job{ID: "j1", Cost: 999})
	mustDispatch(t, jump, t0)                      // U = 999
	snapJump := mustSnapshot(t, jump, t0+5*period) // cross 5 boundaries at once

	step := newScheduler(t, t0, period)
	mustRegister(t, step, t0, "a", 1)
	mustSubmit(t, step, t0, "a", scheduler.Job{ID: "j1", Cost: 999})
	mustDispatch(t, step, t0)
	var snapStep scheduler.Snapshot
	for k := int64(1); k <= 5; k++ {
		snapStep = mustSnapshot(t, step, t0+k*period) // one boundary at a time
	}

	uJump := usageOf(t, snapJump, "a")
	uStep := usageOf(t, snapStep, "a")
	t.Logf("check: jump U=%s, sequential U=%s", uJump, uStep)
	if uJump.Cmp(uStep) != 0 {
		t.Fatalf("jump U=%s != sequential U=%s", uJump, uStep)
	}
	// 999 -> 499 -> 249 -> 124 -> 62 -> 31
	if uJump.Cmp(big.NewInt(31)) != 0 {
		t.Fatalf("want U=31 after 5 halvings of 999, got %s", uJump)
	}
	if snapJump.BoundariesApplied != 5 || snapStep.BoundariesApplied != 5 {
		t.Fatalf("want 5 boundaries applied, got jump=%d step=%d",
			snapJump.BoundariesApplied, snapStep.BoundariesApplied)
	}
}

// Accounts with empty queues decay exactly like the others.
func TestEmptyQueueAccountsAlsoDecay(t *testing.T) {
	s := newScheduler(t, 0, 50)
	mustRegister(t, s, 0, "busy", 1)
	mustRegister(t, s, 0, "idle", 1)
	mustSubmit(t, s, 0, "busy", scheduler.Job{ID: "j1", Cost: 9})
	mustDispatch(t, s, 0) // U(busy)=9, U(idle)=0

	snap := mustSnapshot(t, s, 200) // 4 boundaries crossed
	if got := usageOf(t, snap, "busy"); got.Cmp(big.NewInt(0)) != 0 {
		// 9 -> 4 -> 2 -> 1 -> 0
		t.Fatalf("busy: want U=0 after 4 halvings of 9, got %s", got)
	}
	if got := usageOf(t, snap, "idle"); got.Sign() != 0 {
		t.Fatalf("idle: want U=0, got %s", got)
	}
	if snap.BoundariesApplied != 4 {
		t.Fatalf("want 4 boundaries applied, got %d", snap.BoundariesApplied)
	}

	// The idle account now wins the next dispatch: its decayed U is minimal.
	mustSubmit(t, s, 200, "busy", scheduler.Job{ID: "j2", Cost: 3})
	mustSubmit(t, s, 200, "idle", scheduler.Job{ID: "j3", Cost: 3})
	res := mustDispatch(t, s, 200)
	if res.AccountID != "busy" || res.Job.ID != "j2" {
		// both U=0, tie broken by account ID ascending: "busy" < "idle"
		t.Fatalf("want busy/j2, got %s/%s", res.AccountID, res.Job.ID)
	}
}

// A freshly registered account has U=0 and wins against loaded accounts.
func TestNewAccountWithZeroUsageWins(t *testing.T) {
	s := newScheduler(t, 0, 1000)
	mustRegister(t, s, 0, "old", 10)
	mustSubmit(t, s, 0, "old", scheduler.Job{ID: "j1", Cost: 1})
	mustDispatch(t, s, 0) // U(old)=1

	mustRegister(t, s, 1, "new", 1) // U(new)=0
	mustSubmit(t, s, 1, "old", scheduler.Job{ID: "j2", Cost: 5})
	mustSubmit(t, s, 1, "new", scheduler.Job{ID: "j3", Cost: 5})

	res := mustDispatch(t, s, 1)
	// old: U/share = 1/10 = 0.1; new: 0/1 = 0 -> new wins despite smaller share.
	if res.AccountID != "new" || res.Job.ID != "j3" {
		t.Fatalf("want new/j3, got %s/%s", res.AccountID, res.Job.ID)
	}
}

// Equal usage/share ratios are broken by account ID ascending.
func TestRatioTieBrokenByAccountID(t *testing.T) {
	s := newScheduler(t, 0, 1000)
	mustRegister(t, s, 0, "b", 3)
	mustRegister(t, s, 0, "a", 2)
	// Charge U so that U(a)/2 == U(b)/3: dispatch cost 2 on a, cost 3 on b.
	mustSubmit(t, s, 0, "a", scheduler.Job{ID: "ja0", Cost: 2})
	mustDispatch(t, s, 0) // a: U=2
	mustSubmit(t, s, 0, "b", scheduler.Job{ID: "jb0", Cost: 3})
	mustDispatch(t, s, 0) // b: U=3

	mustSubmit(t, s, 0, "a", scheduler.Job{ID: "ja1", Cost: 1})
	mustSubmit(t, s, 0, "b", scheduler.Job{ID: "jb1", Cost: 1})
	res := mustDispatch(t, s, 0)
	// 2/2 == 3/3 -> tie -> "a" < "b".
	if res.AccountID != "a" || res.Job.ID != "ja1" {
		t.Fatalf("want a/ja1 on tie, got %s/%s", res.AccountID, res.Job.ID)
	}
}

// The charged cost must immediately change the next dispatch order.
func TestChargedCostChangesNextDispatchImmediately(t *testing.T) {
	s := newScheduler(t, 0, 1000)
	mustRegister(t, s, 0, "a", 1)
	mustRegister(t, s, 0, "b", 1)
	mustSubmit(t, s, 0, "a", scheduler.Job{ID: "ja1", Cost: 5})
	mustSubmit(t, s, 0, "a", scheduler.Job{ID: "ja2", Cost: 1})
	mustSubmit(t, s, 0, "b", scheduler.Job{ID: "jb1", Cost: 1})

	res := mustDispatch(t, s, 0) // tie at U=0 -> "a" wins, U(a)=5 now
	if res.AccountID != "a" || res.Job.ID != "ja1" {
		t.Fatalf("first dispatch: want a/ja1, got %s/%s", res.AccountID, res.Job.ID)
	}
	res = mustDispatch(t, s, 0) // U(a)=5 > U(b)=0 -> b wins
	if res.AccountID != "b" || res.Job.ID != "jb1" {
		t.Fatalf("second dispatch: want b/jb1, got %s/%s", res.AccountID, res.Job.ID)
	}
	res = mustDispatch(t, s, 0) // U(a)=5 > U(b)=1 -> b has no jobs left; a wins
	if res.AccountID != "a" || res.Job.ID != "ja2" {
		t.Fatalf("third dispatch: want a/ja2, got %s/%s", res.AccountID, res.Job.ID)
	}
	snap := mustSnapshot(t, s, 0)
	if got := usageOf(t, snap, "a"); got.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("want U(a)=6, got %s", got)
	}
	if got := usageOf(t, snap, "b"); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("want U(b)=1, got %s", got)
	}
}

// Every rejection reason must be distinguishable, and a rejected call must
// not change usage, queues or the number of applied boundaries.
func TestRejections(t *testing.T) {
	if _, err := scheduler.New(0, 0); !errors.Is(err, scheduler.ErrNonPositivePeriod) {
		t.Fatalf("New(period=0): want ErrNonPositivePeriod, got %v", err)
	}
	if _, err := scheduler.New(0, -5); !errors.Is(err, scheduler.ErrNonPositivePeriod) {
		t.Fatalf("New(period=-5): want ErrNonPositivePeriod, got %v", err)
	}

	s := newScheduler(t, 100, 10)
	mustRegister(t, s, 100, "a", 2)
	mustSubmit(t, s, 100, "a", scheduler.Job{ID: "j1", Cost: 4})
	before := mustSnapshot(t, s, 100)

	type op struct {
		name string
		run  func() error
		want error
	}
	ops := []op{
		{"non-positive share", func() error { return s.Register(100, "b", 0) }, scheduler.ErrNonPositiveShare},
		{"negative share", func() error { return s.Register(100, "b", -3) }, scheduler.ErrNonPositiveShare},
		{"duplicate account", func() error { return s.Register(100, "a", 1) }, scheduler.ErrDuplicateAccount},
		{"unknown account", func() error {
			return s.Submit(100, "ghost", scheduler.Job{ID: "j2", Cost: 1})
		}, scheduler.ErrUnknownAccount},
		{"non-positive cost", func() error {
			return s.Submit(100, "a", scheduler.Job{ID: "j2", Cost: 0})
		}, scheduler.ErrNonPositiveCost},
		{"negative cost", func() error {
			return s.Submit(100, "a", scheduler.Job{ID: "j2", Cost: -2})
		}, scheduler.ErrNonPositiveCost},
		{"duplicate job ID", func() error {
			return s.Submit(100, "a", scheduler.Job{ID: "j1", Cost: 1})
		}, scheduler.ErrDuplicateJobID},
		{"clock rollback", func() error { return s.Register(99, "b", 1) }, scheduler.ErrClockRollback},
	}
	for _, tc := range ops {
		err := tc.run()
		t.Logf("input: %s -> rejected: %v", tc.name, err)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}

	// Dispatch with every queue empty.
	s2 := newScheduler(t, 0, 10)
	mustRegister(t, s2, 0, "a", 1)
	if _, err := s2.Dispatch(0); !errors.Is(err, scheduler.ErrNoJobs) {
		t.Fatalf("empty dispatch: want ErrNoJobs, got %v", err)
	}
	if snap := mustSnapshot(t, s2, 0); snap.BoundariesApplied != 0 {
		t.Fatalf("rejected dispatch must not apply boundaries, got %d", snap.BoundariesApplied)
	}

	// Nothing changed after all the rejections above.
	after := mustSnapshot(t, s, 100)
	if before.BoundariesApplied != after.BoundariesApplied {
		t.Fatalf("boundaries changed by rejected ops: %d -> %d",
			before.BoundariesApplied, after.BoundariesApplied)
	}
	if usageOf(t, before, "a").Cmp(usageOf(t, after, "a")) != 0 {
		t.Fatalf("usage changed by rejected ops: %s -> %s",
			usageOf(t, before, "a"), usageOf(t, after, "a"))
	}
	if len(after.Accounts) != 1 || len(after.Accounts[0].Queue) != 1 {
		t.Fatalf("accounts/queues changed by rejected ops: %+v", after.Accounts)
	}
}

// When several rejection reasons hold at once, clock rollback wins; for
// submissions the order is: unknown account, cost, duplicate job ID.
func TestRejectionPriority(t *testing.T) {
	s := newScheduler(t, 100, 10)
	mustRegister(t, s, 100, "a", 1)
	mustSubmit(t, s, 100, "a", scheduler.Job{ID: "j1", Cost: 1})

	// Rollback beats every other reason.
	err := s.Submit(50, "ghost", scheduler.Job{ID: "j1", Cost: 0})
	t.Logf("input: Submit(t=50 rollback, unknown account, bad cost, dup ID) -> %v", err)
	if !errors.Is(err, scheduler.ErrClockRollback) {
		t.Fatalf("want ErrClockRollback, got %v", err)
	}

	// Unknown account beats bad cost and duplicate ID.
	err = s.Submit(100, "ghost", scheduler.Job{ID: "j1", Cost: 0})
	t.Logf("input: Submit(unknown account, bad cost, dup ID) -> %v", err)
	if !errors.Is(err, scheduler.ErrUnknownAccount) {
		t.Fatalf("want ErrUnknownAccount, got %v", err)
	}

	// Bad cost beats duplicate ID.
	err = s.Submit(100, "a", scheduler.Job{ID: "j1", Cost: 0})
	t.Logf("input: Submit(bad cost, dup ID) -> %v", err)
	if !errors.Is(err, scheduler.ErrNonPositiveCost) {
		t.Fatalf("want ErrNonPositiveCost, got %v", err)
	}

	// Rollback also beats the empty-queue dispatch rejection.
	s2 := newScheduler(t, 0, 10)
	mustRegister(t, s2, 5, "a", 1)
	if _, err := s2.Dispatch(4); !errors.Is(err, scheduler.ErrClockRollback) {
		t.Fatalf("want ErrClockRollback, got %v", err)
	}
}

// Concurrent calls must be race-free, equivalent to some serial order, and
// every job must be dispatched exactly once.
func TestConcurrentUse(t *testing.T) {
	s := newScheduler(t, 0, 1000)
	mustRegister(t, s, 0, "a", 1)
	mustRegister(t, s, 0, "b", 2)
	mustRegister(t, s, 0, "c", 3)

	const jobsPerAccount = 200
	var wg sync.WaitGroup
	for _, acct := range []string{"a", "b", "c"} {
		for i := 0; i < jobsPerAccount; i++ {
			wg.Add(1)
			go func(acct string, i int) {
				defer wg.Done()
				job := scheduler.Job{ID: acct + "-" + big.NewInt(int64(i)).String(), Cost: int64(i%7 + 1)}
				if err := s.Submit(0, acct, job); err != nil {
					t.Errorf("Submit failed: %v", err)
				}
			}(acct, i)
		}
	}
	wg.Wait()

	var mu sync.Mutex
	dispatched := make(map[string]int)
	remaining := 3 * jobsPerAccount
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if remaining == 0 {
					mu.Unlock()
					return
				}
				remaining--
				mu.Unlock()
				res, err := s.Dispatch(0)
				if err != nil {
					t.Errorf("Dispatch failed: %v", err)
					return
				}
				mu.Lock()
				dispatched[res.Job.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(dispatched) != 3*jobsPerAccount {
		t.Fatalf("want %d distinct jobs dispatched, got %d", 3*jobsPerAccount, len(dispatched))
	}
	for id, n := range dispatched {
		if n != 1 {
			t.Fatalf("job %q dispatched %d times", id, n)
		}
	}
	snap := mustSnapshot(t, s, 0)
	for _, a := range snap.Accounts {
		if len(a.Queue) != 0 {
			t.Fatalf("account %q still has %d queued jobs", a.AccountID, len(a.Queue))
		}
		if a.Usage.Sign() < 0 {
			t.Fatalf("account %q has negative usage %s", a.AccountID, a.Usage)
		}
	}
	t.Logf("output: all %d jobs dispatched exactly once", len(dispatched))
}

// Replaying the same operation sequence must reproduce the exact same
// dispatch order and usage values.
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]string, map[string]string) {
		s := newScheduler(t, 1000, 250)
		mustRegister(t, s, 1000, "alpha", 2)
		mustRegister(t, s, 1000, "beta", 3)
		mustRegister(t, s, 1200, "gamma", 1)
		mustSubmit(t, s, 1200, "alpha", scheduler.Job{ID: "a1", Cost: 7})
		mustSubmit(t, s, 1200, "alpha", scheduler.Job{ID: "a2", Cost: 3})
		mustSubmit(t, s, 1200, "beta", scheduler.Job{ID: "b1", Cost: 5})
		mustSubmit(t, s, 1500, "beta", scheduler.Job{ID: "b2", Cost: 2})
		mustSubmit(t, s, 1500, "gamma", scheduler.Job{ID: "g1", Cost: 1})
		var order []string
		for _, ts := range []int64{1500, 1750, 2000, 2000, 2500} {
			res := mustDispatch(t, s, ts)
			order = append(order, res.AccountID+"/"+res.Job.ID)
		}
		snap := mustSnapshot(t, s, 3000)
		usage := make(map[string]string)
		for _, a := range snap.Accounts {
			usage[a.AccountID] = a.Usage.String()
		}
		return order, usage
	}

	order1, usage1 := run()
	order2, usage2 := run()
	t.Logf("output: replay1 order=%v usage=%v", order1, usage1)
	t.Logf("output: replay2 order=%v usage=%v", order2, usage2)
	if len(order1) != len(order2) {
		t.Fatalf("dispatch order length differs: %v vs %v", order1, order2)
	}
	for i := range order1 {
		if order1[i] != order2[i] {
			t.Fatalf("dispatch %d differs: %q vs %q", i, order1[i], order2[i])
		}
	}
	for id, u1 := range usage1 {
		if usage2[id] != u1 {
			t.Fatalf("usage of %q differs: %s vs %s", id, u1, usage2[id])
		}
	}
}

// Long-run sanity: usage ratios drift toward share ratios across decays.
func TestLongRunProportionalFairness(t *testing.T) {
	s := newScheduler(t, 0, 100)
	mustRegister(t, s, 0, "small", 1)
	mustRegister(t, s, 0, "large", 3)

	// Keep both queues non-empty for many periods.
	counts := map[string]int{"small": 0, "large": 0}
	for i := 0; i < 400; i++ {
		ts := int64(i) * 25 // 4 dispatches per period
		for _, acct := range []string{"small", "large"} {
			id := acct + "-" + big.NewInt(int64(i)).String()
			if err := s.Submit(ts, acct, scheduler.Job{ID: id, Cost: 1}); err != nil {
				t.Fatalf("Submit failed: %v", err)
			}
		}
		res := mustDispatch(t, s, ts)
		counts[res.AccountID]++
	}
	t.Logf("output: dispatch counts over 400 rounds: %v", counts)
	// With shares 1:3, "large" should receive roughly 3x the dispatches.
	ratio := float64(counts["large"]) / float64(counts["small"])
	if ratio < 2.0 || ratio > 4.5 {
		t.Fatalf("dispatch ratio large/small = %.2f, want roughly 3", ratio)
	}
}
