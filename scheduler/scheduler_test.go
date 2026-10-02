package scheduler

import (
	"fmt"
	"math"
	"math/big"
	"sync"
	"testing"
)

func mustNew(t *testing.T, t0, period int64) *Scheduler {
	t.Helper()
	t.Logf("input: New(t0=%d, period=%d)", t0, period)
	s, err := New(t0, period)
	if err != nil {
		t.Fatalf("New: unexpected rejection: %v", err)
	}
	return s
}

func mustRegister(t *testing.T, s *Scheduler, tm int64, id string, share int64) {
	t.Helper()
	t.Logf("input: Register(t=%d, account=%q, share=%d)", tm, id, share)
	if err := s.Register(tm, id, share); err != nil {
		t.Fatalf("Register(%q): unexpected rejection: %v", id, err)
	}
}

func mustSubmit(t *testing.T, s *Scheduler, tm int64, account, job string, cost int64) {
	t.Helper()
	t.Logf("input: Submit(t=%d, account=%q, job=%q, cost=%d)", tm, account, job, cost)
	if err := s.Submit(tm, account, job, cost); err != nil {
		t.Fatalf("Submit(%q): unexpected rejection: %v", job, err)
	}
}

func mustDispatch(t *testing.T, s *Scheduler, tm int64) *DispatchResult {
	t.Helper()
	t.Logf("input: Dispatch(t=%d)", tm)
	res, err := s.Dispatch(tm)
	if err != nil {
		t.Fatalf("Dispatch: unexpected rejection: %v", err)
	}
	t.Logf("output: job=%q winner=%q reason=%s", res.Job.ID, res.Winner, res.Reason)
	for _, c := range res.Candidates {
		t.Logf("basis: candidate account=%q U=%s share=%d", c.AccountID, c.Usage, c.Share)
	}
	return res
}

func mustQuery(t *testing.T, s *Scheduler, tm int64) *Snapshot {
	t.Helper()
	t.Logf("input: Query(t=%d)", tm)
	snap, err := s.Query(tm)
	if err != nil {
		t.Fatalf("Query: unexpected rejection: %v", err)
	}
	for _, a := range snap.Accounts {
		t.Logf("output: account=%q U=%s share=%d queued=%v", a.ID, a.Usage, a.Share, a.Queued)
	}
	t.Logf("output: boundariesApplied=%d", snap.BoundariesApplied)
	return snap
}

func expectReject(t *testing.T, op string, err error, want Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected rejection %q, got success", op, want)
	}
	se, ok := err.(*Error)
	if !ok {
		t.Fatalf("%s: expected *Error, got %T (%v)", op, err, err)
	}
	t.Logf("output: %s rejected reason=%q detail=%q", op, se.Reason, se.Detail)
	if se.Reason != want {
		t.Fatalf("%s: expected reason %q, got %q", op, want, se.Reason)
	}
}

func usageOf(t *testing.T, snap *Snapshot, id string) *big.Int {
	t.Helper()
	for _, a := range snap.Accounts {
		if a.ID == id {
			return a.Usage
		}
	}
	t.Fatalf("account %q missing from snapshot", id)
	return nil
}

func TestBoundaryExactInstant(t *testing.T) {
	s := mustNew(t, 1000, 100)
	mustRegister(t, s, 1000, "a", 1)
	mustSubmit(t, s, 1000, "a", "j1", 7)
	mustDispatch(t, s, 1000) // U(a) = 7

	snap := mustQuery(t, s, 1099) // just before the boundary
	if got := usageOf(t, snap, "a"); got.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("before boundary: U=%s, want 7", got)
	}
	if snap.BoundariesApplied != 0 {
		t.Fatalf("before boundary: boundaries=%d, want 0", snap.BoundariesApplied)
	}

	snap = mustQuery(t, s, 1100) // exactly on the boundary: it has taken effect
	if got := usageOf(t, snap, "a"); got.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("on boundary: U=%s, want 3 (7/2 floor)", got)
	}
	if snap.BoundariesApplied != 1 {
		t.Fatalf("on boundary: boundaries=%d, want 1", snap.BoundariesApplied)
	}
}

func TestMultiBoundaryJumpEqualsStepwise(t *testing.T) {
	const ops = 5
	jump := mustNew(t, 0, 100)
	step := mustNew(t, 0, 100)
	for _, s := range []*Scheduler{jump, step} {
		mustRegister(t, s, 0, "a", 1)
		mustSubmit(t, s, 0, "a", "j1", 93)
		mustDispatch(t, s, 0) // U(a) = 93
	}

	snapJump := mustQuery(t, jump, 500) // crosses 5 boundaries at once
	for i := 1; i <= ops; i++ {
		mustQuery(t, step, int64(i*100)) // one boundary at a time
	}
	snapStep := mustQuery(t, step, 500)

	gotJump := usageOf(t, snapJump, "a")
	gotStep := usageOf(t, snapStep, "a")
	t.Logf("output: jump U=%s step U=%s", gotJump, gotStep)
	// 93 -> 46 -> 23 -> 11 -> 5 -> 2
	if gotJump.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("jump: U=%s, want 2", gotJump)
	}
	if gotJump.Cmp(gotStep) != 0 {
		t.Fatalf("jump U=%s differs from stepwise U=%s", gotJump, gotStep)
	}
	if snapJump.BoundariesApplied != snapStep.BoundariesApplied {
		t.Fatalf("boundaries differ: jump=%d step=%d", snapJump.BoundariesApplied, snapStep.BoundariesApplied)
	}
}

func TestEmptyQueueAccountStillDecays(t *testing.T) {
	s := mustNew(t, 0, 100)
	mustRegister(t, s, 0, "busy", 1)
	mustRegister(t, s, 0, "idle", 1)
	mustSubmit(t, s, 0, "busy", "j1", 10)
	mustDispatch(t, s, 0) // U(busy)=10, queue now empty

	snap := mustQuery(t, s, 200) // two boundaries
	if got := usageOf(t, snap, "busy"); got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("empty-queue account: U=%s, want 2 (10 -> 5 -> 2)", got)
	}
	if got := usageOf(t, snap, "idle"); got.Sign() != 0 {
		t.Fatalf("idle account: U=%s, want 0", got)
	}
}

func TestNewAccountZeroUsageWins(t *testing.T) {
	s := mustNew(t, 0, 100)
	mustRegister(t, s, 0, "old", 1)
	mustSubmit(t, s, 0, "old", "j-old", 5)
	mustDispatch(t, s, 0) // U(old) = 5

	mustRegister(t, s, 10, "new", 1) // U(new) = 0
	mustSubmit(t, s, 10, "old", "j-old-2", 1)
	mustSubmit(t, s, 10, "new", "j-new", 1)

	res := mustDispatch(t, s, 20)
	if res.Winner != "new" || res.Job.ID != "j-new" {
		t.Fatalf("winner=%q job=%q, want new/j-new (U=0 beats U=5)", res.Winner, res.Job.ID)
	}
}

func TestTieBreakByAccountID(t *testing.T) {
	s := mustNew(t, 0, 100)
	mustRegister(t, s, 0, "zeta", 1)
	mustRegister(t, s, 0, "mid", 2)
	// Non-zero equal ratios: mid U=2 share=2, zeta U=1 share=1.
	mustSubmit(t, s, 0, "mid", "m1", 2)
	mustSubmit(t, s, 0, "mid", "m2", 1)
	mustSubmit(t, s, 0, "zeta", "z1", 1)
	mustSubmit(t, s, 0, "zeta", "z2", 1)

	if res := mustDispatch(t, s, 0); res.Winner != "mid" { // 0/2 ties 0/1, ID wins
		t.Fatalf("d1 winner=%q, want mid", res.Winner)
	} else if res.Job.ID != "m1" {
		t.Fatalf("d1 job=%q, want m1 (FIFO head)", res.Job.ID)
	}
	if res := mustDispatch(t, s, 0); res.Winner != "zeta" { // 0/1 < 2/2
		t.Fatalf("d2 winner=%q, want zeta", res.Winner)
	}
	// Now mid: U=2 share=2, zeta: U=1 share=1 -> exact tie, "mid" < "zeta".
	res := mustDispatch(t, s, 0)
	if res.Winner != "mid" {
		t.Fatalf("d3 winner=%q, want mid (tie broken by ID)", res.Winner)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("d3 candidates=%d, want 2", len(res.Candidates))
	}
}

func TestCostChargedImmediatelyChangesOrder(t *testing.T) {
	s := mustNew(t, 0, 100)
	mustRegister(t, s, 0, "a", 1)
	mustRegister(t, s, 0, "b", 1)
	mustSubmit(t, s, 0, "a", "a1", 10)
	mustSubmit(t, s, 0, "a", "a2", 1)
	mustSubmit(t, s, 0, "b", "b1", 1)
	mustSubmit(t, s, 0, "b", "b2", 1)
	mustSubmit(t, s, 0, "b", "b3", 1)

	var order []string
	for i := 0; i < 5; i++ {
		order = append(order, mustDispatch(t, s, 0).Job.ID)
	}
	t.Logf("output: dispatch order=%v", order)
	// a wins the 0/0 tie, then U(a)=10 makes b win until it catches up.
	want := []string{"a1", "b1", "b2", "b3", "a2"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v, want %v", order, want)
		}
	}
}

func TestFIFOWithinAccount(t *testing.T) {
	s := mustNew(t, 0, 100)
	mustRegister(t, s, 0, "a", 1)
	mustSubmit(t, s, 0, "a", "first", 1)
	mustSubmit(t, s, 0, "a", "second", 1)
	if res := mustDispatch(t, s, 0); res.Job.ID != "first" {
		t.Fatalf("job=%q, want first", res.Job.ID)
	}
	if res := mustDispatch(t, s, 0); res.Job.ID != "second" {
		t.Fatalf("job=%q, want second", res.Job.ID)
	}
}

func TestInvalidPeriod(t *testing.T) {
	t.Logf("input: New(t0=0, period=0)")
	_, err := New(0, 0)
	expectReject(t, "New(period=0)", err, ReasonInvalidPeriod)
	t.Logf("input: New(t0=0, period=-5)")
	_, err = New(0, -5)
	expectReject(t, "New(period=-5)", err, ReasonInvalidPeriod)
}

func TestRegisterRejections(t *testing.T) {
	s := mustNew(t, 0, 100)
	mustRegister(t, s, 0, "a", 1)

	t.Logf("input: Register(t=1, account=%q, share=0)", "b")
	expectReject(t, "Register(share=0)", s.Register(1, "b", 0), ReasonInvalidShare)
	t.Logf("input: Register(t=1, account=%q, share=-3)", "b")
	expectReject(t, "Register(share=-3)", s.Register(1, "b", -3), ReasonInvalidShare)
	t.Logf("input: Register(t=1, account=%q, share=1)", "a")
	expectReject(t, "Register(duplicate)", s.Register(1, "a", 1), ReasonDuplicateAccount)
}

func TestSubmitRejectionsAndPriority(t *testing.T) {
	s := mustNew(t, 0, 100)
	mustRegister(t, s, 0, "a", 1)
	mustSubmit(t, s, 0, "a", "j1", 1)

	t.Logf("input: Submit(t=5, account=%q, job=%q, cost=1)", "ghost", "j2")
	expectReject(t, "Submit(unregistered)", s.Submit(5, "ghost", "j2", 1), ReasonAccountNotRegistered)
	t.Logf("input: Submit(t=5, account=%q, job=%q, cost=0)", "a", "j2")
	expectReject(t, "Submit(cost=0)", s.Submit(5, "a", "j2", 0), ReasonInvalidCost)
	t.Logf("input: Submit(t=5, account=%q, job=%q, cost=1)", "a", "j1")
	expectReject(t, "Submit(dup id)", s.Submit(5, "a", "j1", 1), ReasonDuplicateJobID)

	// Multiple simultaneous causes: only the first in
	// account -> cost -> job-ID order is reported.
	t.Logf("input: Submit(t=5, account=%q, job=%q, cost=0) [all three bad]", "ghost", "j1")
	expectReject(t, "Submit(all bad)", s.Submit(5, "ghost", "j1", 0), ReasonAccountNotRegistered)
	t.Logf("input: Submit(t=5, account=%q, job=%q, cost=-1) [cost+id bad]", "a", "j1")
	expectReject(t, "Submit(cost+id bad)", s.Submit(5, "a", "j1", -1), ReasonInvalidCost)

	// Clock rollback beats every other cause.
	mustSubmit(t, s, 50, "a", "j3", 1)
	t.Logf("input: Submit(t=10, account=%q, job=%q, cost=0) [clock+all bad]", "ghost", "j1")
	expectReject(t, "Submit(clock first)", s.Submit(10, "ghost", "j1", 0), ReasonClockBackwards)
}

func TestDispatchEmptyQueues(t *testing.T) {
	s := mustNew(t, 0, 100)
	t.Logf("input: Dispatch(t=0) with no accounts")
	_, err := s.Dispatch(0)
	expectReject(t, "Dispatch(no accounts)", err, ReasonNoJobs)

	mustRegister(t, s, 0, "a", 1)
	t.Logf("input: Dispatch(t=0) with empty queue")
	_, err = s.Dispatch(0)
	expectReject(t, "Dispatch(empty queue)", err, ReasonNoJobs)

	mustSubmit(t, s, 0, "a", "j1", 1)
	mustDispatch(t, s, 0)
	t.Logf("input: Dispatch(t=0) after queue drained")
	_, err = s.Dispatch(0)
	expectReject(t, "Dispatch(drained)", err, ReasonNoJobs)
}

func TestClockBackwards(t *testing.T) {
	s := mustNew(t, 100, 100)
	mustRegister(t, s, 100, "a", 1)
	mustSubmit(t, s, 500, "a", "j1", 1)

	t.Logf("input: Register(t=499, ...) after t=500 was seen")
	expectReject(t, "Register(backwards)", s.Register(499, "b", 1), ReasonClockBackwards)
	t.Logf("input: Submit(t=499, ...) after t=500 was seen")
	expectReject(t, "Submit(backwards)", s.Submit(499, "a", "j2", 1), ReasonClockBackwards)
	t.Logf("input: Dispatch(t=499) after t=500 was seen")
	_, err := s.Dispatch(499)
	expectReject(t, "Dispatch(backwards)", err, ReasonClockBackwards)
	t.Logf("input: Query(t=499) after t=500 was seen")
	_, err = s.Query(499)
	expectReject(t, "Query(backwards)", err, ReasonClockBackwards)

	// Equal time is not a rollback.
	mustSubmit(t, s, 500, "a", "j2", 1)
}

func TestRejectedOpsLeaveStateUntouched(t *testing.T) {
	s := mustNew(t, 0, 100)
	mustRegister(t, s, 0, "a", 1)
	mustSubmit(t, s, 0, "a", "j1", 7)
	mustDispatch(t, s, 0) // U(a) = 7
	before := mustQuery(t, s, 50)

	// Each of these is rejected and must not change U, queues or the
	// applied-boundary count (all would cross a boundary if accepted).
	expectReject(t, "Register(bad share)", s.Register(250, "b", 0), ReasonInvalidShare)
	expectReject(t, "Register(dup)", s.Register(250, "a", 1), ReasonDuplicateAccount)
	expectReject(t, "Submit(unregistered)", s.Submit(250, "ghost", "j2", 1), ReasonAccountNotRegistered)
	expectReject(t, "Submit(bad cost)", s.Submit(250, "a", "j2", 0), ReasonInvalidCost)
	expectReject(t, "Submit(dup id)", s.Submit(250, "a", "j1", 1), ReasonDuplicateJobID)
	_, err := s.Dispatch(250) // would cross boundaries, but queue a is empty
	expectReject(t, "Dispatch(empty)", err, ReasonNoJobs)
	expectReject(t, "Submit(backwards)", s.Submit(10, "a", "j9", 1), ReasonClockBackwards)

	after := mustQuery(t, s, 50)
	if before.BoundariesApplied != after.BoundariesApplied {
		t.Fatalf("boundaries changed by rejected ops: %d -> %d", before.BoundariesApplied, after.BoundariesApplied)
	}
	if usageOf(t, before, "a").Cmp(usageOf(t, after, "a")) != 0 {
		t.Fatalf("U changed by rejected ops: %s -> %s", usageOf(t, before, "a"), usageOf(t, after, "a"))
	}
	if after.Accounts[0].QueueLen != 0 {
		t.Fatalf("queue changed by rejected ops: len=%d", after.Accounts[0].QueueLen)
	}
}

func TestLargeValuesExactComparison(t *testing.T) {
	s := mustNew(t, 0, 1)
	mustRegister(t, s, 0, "a", math.MaxInt64)
	mustRegister(t, s, 0, "b", math.MaxInt64)
	// Equal huge ratios: U(a)=U(b)=MaxInt64, shares equal -> exact tie.
	// The cross products (~2^126) would overflow int64; big.Int keeps them exact.
	mustSubmit(t, s, 0, "a", "a1", math.MaxInt64)
	mustSubmit(t, s, 0, "a", "a2", math.MaxInt64)
	mustSubmit(t, s, 0, "b", "b1", math.MaxInt64)
	mustSubmit(t, s, 0, "b", "b2", 1)

	if res := mustDispatch(t, s, 0); res.Winner != "a" { // 0/0 tie -> ID
		t.Fatalf("d1 winner=%q, want a", res.Winner)
	}
	if res := mustDispatch(t, s, 0); res.Winner != "b" { // 0 < MaxInt64
		t.Fatalf("d2 winner=%q, want b", res.Winner)
	}
	// Now U(a)==U(b)==MaxInt64 with equal shares: exact tie -> "a".
	if res := mustDispatch(t, s, 0); res.Winner != "a" {
		t.Fatalf("d3 winner=%q, want a (exact tie on huge values)", res.Winner)
	}
	// U(a) = 2*MaxInt64-... would overflow int64; big.Int keeps U non-negative
	// and exact. b has U=MaxInt64 < U(a), so b wins.
	res := mustDispatch(t, s, 0)
	if res.Winner != "b" {
		t.Fatalf("d4 winner=%q, want b", res.Winner)
	}
	snap := mustQuery(t, s, 0)
	for _, acc := range snap.Accounts {
		if acc.Usage.Sign() < 0 {
			t.Fatalf("account %q has negative U=%s", acc.ID, acc.Usage)
		}
	}
	wantA := new(big.Int).Add(big.NewInt(math.MaxInt64), big.NewInt(math.MaxInt64))
	if got := usageOf(t, snap, "a"); got.Cmp(wantA) != 0 {
		t.Fatalf("U(a)=%s, want %s (exceeds int64)", got, wantA)
	}
}

func TestDeterministicReplay(t *testing.T) {
	run := func() ([]string, map[string]string) {
		s := mustNew(t, 1000, 100)
		mustRegister(t, s, 1000, "alpha", 3)
		mustRegister(t, s, 1000, "beta", 1)
		mustRegister(t, s, 1050, "gamma", 2)
		mustSubmit(t, s, 1060, "alpha", "a1", 4)
		mustSubmit(t, s, 1060, "alpha", "a2", 9)
		mustSubmit(t, s, 1060, "beta", "b1", 2)
		mustSubmit(t, s, 1070, "gamma", "g1", 1)
		mustSubmit(t, s, 1200, "beta", "b2", 3)
		var order []string
		for _, tm := range []int64{1210, 1220, 1350, 1350, 2000} {
			order = append(order, mustDispatch(t, s, tm).Job.ID)
		}
		snap := mustQuery(t, s, 2000)
		usage := make(map[string]string)
		for _, a := range snap.Accounts {
			usage[a.ID] = a.Usage.String()
		}
		return order, usage
	}
	order1, usage1 := run()
	order2, usage2 := run()
	t.Logf("output: replay1 order=%v usage=%v", order1, usage1)
	t.Logf("output: replay2 order=%v usage=%v", order2, usage2)
	for i := range order1 {
		if order1[i] != order2[i] {
			t.Fatalf("replay diverged at %d: %v vs %v", i, order1, order2)
		}
	}
	for id, u := range usage1 {
		if usage2[id] != u {
			t.Fatalf("usage diverged for %q: %s vs %s", id, u, usage2[id])
		}
	}
}

func TestConcurrentUseEquivalentToSerial(t *testing.T) {
	s := mustNew(t, 0, 10)
	const accounts = 8
	const jobsPerAccount = 25
	for i := 0; i < accounts; i++ {
		mustRegister(t, s, 0, fmt.Sprintf("acc-%02d", i), int64(i+1))
	}

	var wg sync.WaitGroup
	// Concurrent submitters.
	for i := 0; i < accounts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("acc-%02d", i)
			for j := 0; j < jobsPerAccount; j++ {
				// All submits share the same instant so that interleaved
				// goroutines never roll the shared clock backwards.
				if err := s.Submit(0, id, fmt.Sprintf("%s-job-%02d", id, j), int64(j%5+1)); err != nil {
					t.Errorf("submit: %v", err)
				}
			}
		}(i)
	}
	// Concurrent readers.
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				// Concurrent calls race on the clock, so rejections are
				// expected here; the point is safety under -race.
				_, _ = s.Query(0)
			}
		}
	}()
	wg.Wait()
	close(stop)
	<-readerDone

	// Concurrent dispatchers: every job must be dispatched exactly once.
	total := accounts * jobsPerAccount
	results := make(chan string, total)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				res, err := s.Dispatch(1000)
				if err != nil {
					return // queues drained
				}
				results <- res.Job.ID
			}
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[string]int)
	count := 0
	for id := range results {
		seen[id]++
		count++
	}
	if count != total {
		t.Fatalf("dispatched %d jobs, want %d", count, total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %q dispatched %d times", id, n)
		}
	}
	snap := mustQuery(t, s, 1000)
	for _, a := range snap.Accounts {
		if a.Usage.Sign() < 0 {
			t.Fatalf("account %q has negative U=%s", a.ID, a.Usage)
		}
		if a.QueueLen != 0 {
			t.Fatalf("account %q still has %d queued jobs", a.ID, a.QueueLen)
		}
	}
	t.Logf("output: all %d jobs dispatched exactly once; final snapshot consistent", count)
}
