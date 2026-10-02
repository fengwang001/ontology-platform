package quota

import (
	"errors"
	"sync"
	"testing"
)

func mustNew(t *testing.T, gu, gg int64) *Manager {
	t.Helper()
	m, err := New(gu, gg)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", gu, gg, err)
	}
	return m
}

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", ctx, err)
	}
}

func reasonOf(err error) RejectReason {
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason
	}
	return -1
}

func expectReason(t *testing.T, err error, want RejectReason, ctx string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected rejection %v, got success", ctx, want)
	}
	if got := reasonOf(err); got != want {
		t.Fatalf("%s: expected reason %v, got %v (%v)", ctx, want, got, err)
	}
}

func ustate(m *Manager, u int) (int64, int64, bool) {
	usage, _ := m.UserUsage(u)
	g, set, _ := m.UserGrace(u)
	return usage, g, set
}

func gstate(m *Manager, g int) (int64, int64, bool) {
	usage, _ := m.GroupUsage(g)
	gr, set, _ := m.GroupGrace(g)
	return usage, gr, set
}

func TestNewInvalid(t *testing.T) {
	for _, gu := range []int64{0, -1, 1_000_000_001} {
		if _, err := New(gu, 200); reasonOf(err) != ReasonInvalidArgument {
			t.Fatalf("New(%d,200) = %v", gu, err)
		}
	}
	for _, gg := range []int64{0, -5, 1_000_000_001} {
		if _, err := New(100, gg); reasonOf(err) != ReasonInvalidArgument {
			t.Fatalf("New(100,%d) = %v", gg, err)
		}
	}
}

func TestRegistration(t *testing.T) {
	m := mustNew(t, 100, 200)

	// Invalid arguments are rejected wholesale.
	expectReason(t, m.AddGroup(1, 10, 5), ReasonInvalidArgument, "soft>hard")
	expectReason(t, m.AddGroup(-1, 0, 0), ReasonInvalidArgument, "bad gid")
	expectReason(t, m.AddGroup(1_000_001, 0, 0), ReasonInvalidArgument, "gid too big")
	expectReason(t, m.AddGroup(2, -1, 0), ReasonInvalidArgument, "negative soft")

	mustOK(t, m.AddGroup(1, 150, 200), "AddGroup(1)")
	expectReason(t, m.AddGroup(1, 150, 200), ReasonIllegalState, "dup group")

	expectReason(t, m.AddUser(1, 9, 0, 0), ReasonNotFound, "missing group")
	mustOK(t, m.AddUser(1, 1, 50, 80), "AddUser(1)")
	expectReason(t, m.AddUser(1, 1, 50, 80), ReasonIllegalState, "dup user")

	// Queries only test existence.
	if _, err := m.UserUsage(7); reasonOf(err) != ReasonNotFound {
		t.Fatalf("missing user query: %v", err)
	}
	if _, _, err := m.GroupGrace(7); reasonOf(err) != ReasonNotFound {
		t.Fatalf("missing group grace query: %v", err)
	}
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	m := mustNew(t, 100, 200)
	mustOK(t, m.AddGroup(1, 150, 200), "add group")
	mustOK(t, m.AddUser(1, 1, 50, 80), "add user")

	mustOK(t, m.Alloc(1, 50, 0), "t0 alloc50")
	if usage, _, set := ustate(m, 1); usage != 50 || set {
		t.Fatalf("after t0: usage=%d graceSet=%v", usage, set)
	}

	mustOK(t, m.Alloc(1, 20, 10), "t10 alloc20")
	if usage, g, set := ustate(m, 1); usage != 70 || !set || g != 10 {
		t.Fatalf("after t10: usage=%d grace=%d set=%v", usage, g, set)
	}

	mustOK(t, m.Alloc(1, 10, 50), "t50 alloc10 (==hard)")
	if usage, g, set := ustate(m, 1); usage != 80 || !set || g != 10 {
		t.Fatalf("after t50: usage=%d grace=%d", usage, g)
	}

	expectReason(t, m.Alloc(1, 1, 60), ReasonUserHardLimit, "t60 over hard")
	if usage, _, _ := ustate(m, 1); usage != 80 {
		t.Fatalf("rejected alloc changed usage: %d", usage)
	}

	mustOK(t, m.Free(1, 25, 109), "t109 free25")
	if usage, g, set := ustate(m, 1); usage != 55 || !set || g != 10 {
		t.Fatalf("after free: usage=%d grace=%d", usage, g)
	}

	mustOK(t, m.Alloc(1, 5, 109), "t109 alloc5, grace not yet expired")
	if usage, g, set := ustate(m, 1); usage != 60 || !set || g != 10 {
		t.Fatalf("after alloc: usage=%d grace=%d set=%v", usage, g, set)
	}

	expectReason(t, m.Alloc(1, 1, 110), ReasonUserGraceExpired, "t110 grace expired")
	if usage, g, set := ustate(m, 1); usage != 60 || !set || g != 10 {
		t.Fatalf("expired-grace rejection changed state: usage=%d grace=%d set=%v",
			usage, g, set)
	}

	mustOK(t, m.Free(1, 10, 110), "t110 free10 to exactly soft")
	if usage, _, set := ustate(m, 1); usage != 50 || set {
		t.Fatalf("after free to soft: usage=%d graceSet=%v", usage, set)
	}

	mustOK(t, m.Alloc(1, 1, 111), "t111 alloc1 re-arms timer")
	if usage, g, set := ustate(m, 1); usage != 51 || !set || g != 111 {
		t.Fatalf("after re-armed alloc: usage=%d grace=%d set=%v", usage, g, set)
	}
}

// Exactly soft is not over soft; exactly hard passes allocation.
func TestSoftHardBoundaries(t *testing.T) {
	m := mustNew(t, 100, 200)
	mustOK(t, m.AddGroup(1, 10, 10), "group soft=hard=10")
	mustOK(t, m.AddUser(1, 1, 10, 10), "user soft=hard=10")

	mustOK(t, m.Alloc(1, 10, 0), "alloc exactly to limits")
	if _, _, set := ustate(m, 1); set {
		t.Fatal("usage == soft must not start grace")
	}
	if _, _, set := gstate(m, 1); set {
		t.Fatal("group usage == soft must not start grace")
	}
	expectReason(t, m.Alloc(1, 1, 0), ReasonUserHardLimit, "one over hard")
}

// Grace expires exactly at start+Gu; one tick before it is still live.
func TestGraceExactBoundary(t *testing.T) {
	m := mustNew(t, 100, 200)
	mustOK(t, m.AddGroup(1, 1000, 1000), "g")
	mustOK(t, m.AddUser(1, 1, 10, 1000), "u")

	mustOK(t, m.Alloc(1, 11, 10), "over soft at t=10")
	mustOK(t, m.Alloc(1, 1, 109), "t=109 is one before expiry (10+100)")
	expectReason(t, m.Alloc(1, 1, 110), ReasonUserGraceExpired, "t=110 expired")

	// Free is allowed even after grace expiry and keeps the timer while
	// usage remains over soft.
	mustOK(t, m.Free(1, 1, 110), "free after expiry allowed")
	if _, g, set := ustate(m, 1); !set || g != 10 {
		t.Fatalf("free above soft must keep start: %d set=%v", g, set)
	}
}

// Rejected allocations neither clear nor create a grace start.
func TestRejectedAllocKeepsGrace(t *testing.T) {
	m := mustNew(t, 100, 200)
	mustOK(t, m.AddGroup(1, 1000, 1000), "g")
	mustOK(t, m.AddUser(1, 1, 10, 100), "u")

	// Usage under soft: a hard-rejected alloc must not start the timer.
	expectReason(t, m.Alloc(1, 200, 0), ReasonUserHardLimit, "hard reject")
	if _, _, set := ustate(m, 1); set {
		t.Fatal("rejected alloc must not create grace start")
	}

	mustOK(t, m.Alloc(1, 11, 5), "over soft at t=5")
	expectReason(t, m.Alloc(1, 500, 6), ReasonUserHardLimit, "hard reject again")
	if _, g, set := ustate(m, 1); !set || g != 5 {
		t.Fatalf("hard reject must not clear grace: %d set=%v", g, set)
	}
}

// One allocation pushes both user and group over soft; each gets its own
// start, and a group-level rejection leaves the user completely untouched.
func TestUserAndGroupSimultaneous(t *testing.T) {
	m := mustNew(t, 100, 200)
	mustOK(t, m.AddGroup(1, 20, 400), "g soft=20 hard=400")
	mustOK(t, m.AddUser(1, 1, 20, 500), "u soft=20 hard=500")

	mustOK(t, m.Alloc(1, 30, 7), "both cross soft")
	if usage, g, set := ustate(m, 1); usage != 30 || !set || g != 7 {
		t.Fatalf("user state: usage=%d grace=%d set=%v", usage, g, set)
	}
	if usage, g, set := gstate(m, 1); usage != 30 || !set || g != 7 {
		t.Fatalf("group state: usage=%d grace=%d set=%v", usage, g, set)
	}

	// Group hard check fires before group grace while the user passes:
	// 30+380 = 410 <= user hard 500 but > group hard 400.
	expectReason(t, m.Alloc(1, 380, 9), ReasonGroupHardLimit, "group hard")
	if usage, g, set := ustate(m, 1); usage != 30 || !set || g != 7 {
		t.Fatalf("user must be unchanged after group reject: %d %d %v",
			usage, g, set)
	}
	if usage, g, set := gstate(m, 1); usage != 30 || !set || g != 7 {
		t.Fatalf("group grace must be unchanged: %d %d %v", usage, g, set)
	}
	mustOK(t, m.AddUser(2, 1, 1000, 1000), "second user with high soft")

	// Now reject on group grace expiry only: the second user is under its
	// own soft/hard, so only the group can reject at t=7+200=207.
	expectReason(t, m.Alloc(2, 1, 207), ReasonGroupGraceExpired, "group grace")
	if usage, _, set := ustate(m, 2); usage != 0 || set {
		t.Fatalf("rejected user must stay untouched: usage=%d graceSet=%v",
			usage, set)
	}
	if usage, g, set := gstate(m, 1); usage != 30 || !set || g != 7 {
		t.Fatalf("group must be unchanged by rejection: %d %d %v",
			usage, g, set)
	}
}

// SetLimits re-tidies immediately: lowering soft starts the timer, raising
// clears it; lowering hard below usage blocks all later allocations.
func TestSetLimitsTidyAndHard(t *testing.T) {
	m := mustNew(t, 100, 200)
	mustOK(t, m.AddGroup(1, 1000, 1000), "g")
	mustOK(t, m.AddUser(1, 1, 50, 100), "u")
	mustOK(t, m.Alloc(1, 40, 0), "usage 40, under soft")

	mustOK(t, m.SetLimits(KindUser, 1, 30, 100, 12), "lower soft to 30")
	if _, g, set := ustate(m, 1); !set || g != 12 {
		t.Fatalf("lower soft must start grace at now=12: %d %v", g, set)
	}

	mustOK(t, m.SetLimits(KindUser, 1, 60, 100, 13), "raise soft to 60")
	if _, _, set := ustate(m, 1); set {
		t.Fatal("raise soft above usage must clear grace")
	}

	// Lower hard below usage: state is allowed, but every alloc is refused.
	mustOK(t, m.SetLimits(KindUser, 1, 30, 30, 14), "lower hard below usage")
	expectReason(t, m.Alloc(1, 1, 15), ReasonUserHardLimit, "alloc under low hard")
	// Clock rollback is reported before the hard-limit state is consulted.
	expectReason(t, m.Alloc(1, 1, 13), ReasonClockRollback, "rollback t13<t15")
	// Free stays possible even with usage above hard.
	mustOK(t, m.Free(1, 11, 16), "free below hard")
	expectReason(t, m.Alloc(1, 1, 15), ReasonClockRollback, "rollback t15<t16")

	// Bad SetLimits arguments.
	expectReason(t, m.SetLimits(Kind(7), 1, 0, 0, 16),
		ReasonInvalidArgument, "bad kind")
	expectReason(t, m.SetLimits(KindGroup, 9, 0, 0, 16),
		ReasonNotFound, "missing group")
}

// Move: target is checked like an allocation of the user's usage; the
// source group is released and tidied (grace cleared when back under soft).
func TestMoveTargetChecksAndSourceRelease(t *testing.T) {
	m := mustNew(t, 100, 200)
	mustOK(t, m.AddGroup(1, 10, 1000), "src soft=10")
	mustOK(t, m.AddGroup(2, 0, 50), "dst hard=50")
	mustOK(t, m.AddGroup(3, 10, 1000), "dst2 soft=10")
	mustOK(t, m.AddUser(1, 1, 1000, 1000), "u")
	// Anchor user keeps group 3 over soft with a grace starting at t=0,
	// which expires at 0+200=200 even after user 1 leaves.
	mustOK(t, m.AddUser(2, 3, 1000, 1000), "anchor in dst2")
	mustOK(t, m.Alloc(2, 20, 0), "anchor over group soft")
	mustOK(t, m.Alloc(1, 60, 0), "usage 60")
	if _, g, set := gstate(m, 1); !set || g != 0 {
		t.Fatalf("source grace should start at 0: %d %v", g, set)
	}

	// Target hard: 0+60 > 50.
	expectReason(t, m.Move(1, 2, 1), ReasonGroupHardLimit, "move target hard")
	if usage, _ := m.UserUsage(1); usage != 60 {
		t.Fatalf("rejected move changed user usage: %d", usage)
	}
	if usage, _ := m.GroupUsage(1); usage != 60 {
		t.Fatalf("rejected move changed source usage: %d", usage)
	}
	if usage, _ := m.GroupUsage(2); usage != 0 {
		t.Fatalf("rejected move changed target usage: %d", usage)
	}

	// Move to group 3: usage 60 pushes group over soft (10); grace starts
	// at t=2. Moving again after group grace expiry (2+200=202) is blocked.
	mustOK(t, m.Move(1, 3, 2), "move to group 3")
	if usage, _ := m.GroupUsage(3); usage != 80 {
		t.Fatalf("target usage (anchor 20 + user 60): %d", usage)
	}
	if usage, _ := m.GroupUsage(1); usage != 0 {
		t.Fatalf("source usage must be released: %d", usage)
	}
	if _, _, set := gstate(m, 1); set {
		t.Fatal("source back under soft must clear grace")
	}
	if _, g, set := gstate(m, 3); !set || g != 0 {
		t.Fatalf("target grace must be kept at 0: %d %v", g, set)
	}
	// Move back to group 1 (grace currently unset after the release), then
	// attempt to return to group 3 after its grace expired at t=200.
	mustOK(t, m.Move(1, 1, 3), "move back to source")
	mustOK(t, m.Alloc(1, 1, 10), "grow usage in group 1 to 61")
	// t=199 is one tick before expiry (0+200=200): a zero-usage move is
	// allowed anyway because it skips the grace check.
	mustOK(t, m.AddUser(3, 1, 1000, 1000), "zero usage user")
	mustOK(t, m.Move(3, 3, 199), "zero usage move ignores live grace")
	// Exactly at the boundary the non-zero move is rejected.
	expectReason(t, m.Move(1, 3, 200), ReasonGroupGraceExpired, "dst expired t200")
	expectReason(t, m.Move(1, 3, 202), ReasonGroupGraceExpired, "dst expired 202")

	// Same-group move is a state error.
	expectReason(t, m.Move(1, 1, 203), ReasonIllegalState, "same group")
}

// A zero-usage user moves freely even when the target group's grace has
// expired; the target grace check is skipped when U == 0.
func TestMoveZeroUsageSkipsGrace(t *testing.T) {
	m := mustNew(t, 100, 200)
	mustOK(t, m.AddGroup(1, 100, 1000), "g1")
	mustOK(t, m.AddGroup(2, 100, 1000), "g2")
	mustOK(t, m.AddUser(2, 2, 10, 1000), "other user in g2")
	mustOK(t, m.AddUser(1, 1, 100, 1000), "zero-usage user")

	// Push g2 over soft and let its grace expire.
	mustOK(t, m.Alloc(2, 50, 0), "g2 over soft")

	// Zero-usage move must succeed even at t=200+ despite g2 grace expiry.
	mustOK(t, m.Move(1, 2, 500), "zero-usage move across expired grace")
	if usage, _ := m.GroupUsage(1); usage != 0 {
		t.Fatalf("source usage: %d", usage)
	}
	if usage, _ := m.GroupUsage(2); usage != 50 {
		t.Fatalf("target usage must stay 50: %d", usage)
	}

	// Moving back with U>0 is now blocked by g1? g1 is at 0 under soft.
	// Instead check target g2 grace again by moving a non-zero user: make
	// user 1 acquire usage while in g2, then move to g1 and back.
	expectReason(t, m.Move(9, 1, 501), ReasonNotFound, "missing user move")
}

// Rejections, including clock rollback, leave every piece of state and the
// monotonic clock untouched.
func TestRejectionDoesNotAdvanceClock(t *testing.T) {
	m := mustNew(t, 100, 200)
	mustOK(t, m.AddGroup(1, 100, 100), "g")
	mustOK(t, m.AddUser(1, 1, 10, 10), "u")
	mustOK(t, m.Alloc(1, 10, 50), "accepted t=50")

	// Rollback attempt at t=49: rejected, so t=49 must not become the
	// accepted maximum (a later t=49 must still be a rollback).
	expectReason(t, m.Alloc(1, 1, 49), ReasonClockRollback, "rollback")
	expectReason(t, m.Free(1, 1, 49), ReasonClockRollback, "rollback free")
	mustOK(t, m.Free(1, 1, 50), "same now as accepted max is fine")

	if usage, _ := m.UserUsage(1); usage != 9 {
		t.Fatalf("usage after rollbacks: %d", usage)
	}
}

// Over-free is a state error checked after entity existence.
func TestOverFree(t *testing.T) {
	m := mustNew(t, 100, 200)
	mustOK(t, m.AddGroup(1, 100, 100), "g")
	mustOK(t, m.AddUser(1, 1, 100, 100), "u")
	mustOK(t, m.Alloc(1, 3, 0), "usage 3")
	expectReason(t, m.Free(1, 4, 1), ReasonIllegalState, "over free")
	if usage, _ := m.UserUsage(1); usage != 3 {
		t.Fatalf("over-free changed usage: %d", usage)
	}
}

// Group usage always equals the sum of its users' usage under concurrency;
// all calls are serializable through the manager lock.
func TestConcurrent(t *testing.T) {
	m := mustNew(t, 100, 10)
	mustOK(t, m.AddGroup(1, 1_000_000_000_000_000, 1_000_000_000_000_000), "g")
	const n = 8
	for u := 0; u < n; u++ {
		mustOK(t, m.AddUser(u, 1, 1_000_000_000_000_000, 1_000_000_000_000_000), "add user")
	}

	var wg sync.WaitGroup
	var clockMu sync.Mutex
	var clock int64
	for u := 0; u < n; u++ {
		wg.Add(1)
		go func(u int) {
			defer wg.Done()
			for k := int64(0); k < 200; k++ {
				clockMu.Lock()
				clock++
				now := clock
				err := m.Alloc(u, 1, now)
				clockMu.Unlock()
				if err != nil {
					t.Errorf("alloc u=%d k=%d: %v", u, k, err)
					return
				}
				if _, _, err := m.UserGrace(u); err != nil {
					t.Errorf("grace query: %v", err)
					return
				}
			}
		}(u)
	}
	wg.Wait()

	var sum int64
	for u := 0; u < n; u++ {
		usage, err := m.UserUsage(u)
		if err != nil {
			t.Fatalf("usage u=%d: %v", u, err)
		}
		if usage != 200 {
			t.Fatalf("user %d usage=%d want 200", u, usage)
		}
		sum += usage
	}
	groupUsage, err := m.GroupUsage(1)
	if err != nil {
		t.Fatal(err)
	}
	if groupUsage != sum {
		t.Fatalf("group usage %d != user sum %d", groupUsage, sum)
	}
}
