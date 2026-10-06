package rotation

import (
	"fmt"
	"strings"
	"testing"
)

type testLog struct{ b strings.Builder }

func (l *testLog) Logf(format string, args ...any) {
	fmt.Fprintf(&l.b, format+"\n", args...)
}

func newTestScheduler(t *testing.T) (*Scheduler, *testLog) {
	t.Helper()
	l := &testLog{}
	return New(l), l
}

func failLog(t *testing.T, l *testLog, format string, args ...any) {
	t.Helper()
	t.Fatalf(format+"\n--- log ---\n%s", append(args, l.b.String())...)
}

func must(t *testing.T, err error, l *testLog, what string) {
	t.Helper()
	if err != nil {
		failLog(t, l, "%s: unexpected error: %v", what, err)
	}
}

func rejectKind(err error) RejectKind {
	tag, ok := err.(*Error)
	if !ok {
		return -1
	}
	return tag.Kind
}

func assertInts(t *testing.T, got, want []int, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

func assertSet(t *testing.T, got, want []int, what string) {
	t.Helper()
	ms := map[int]int{}
	for _, v := range got {
		ms[v]++
	}
	for _, v := range want {
		ms[v]--
	}
	for v, d := range ms {
		if d != 0 {
			t.Fatalf("%s = %v, want set %v (mismatch at %d)", what, got, want, v)
		}
	}
}

func setupRegion(t *testing.T, s *Scheduler, l *testLog, groups int, users map[int][]string) {
	t.Helper()
	must(t, s.AddRegion("R", 10), l, "AddRegion")
	for g := 1; g <= groups; g++ {
		must(t, s.AddGroup("R", g), l, "AddGroup")
	}
	for g, us := range users {
		for _, u := range us {
			must(t, s.AddUser(u, "R", g, Normal, 0), l, "AddUser")
		}
	}
}

func mustDecision(t *testing.T, s *Scheduler, start int64) []int {
	t.Helper()
	d, ok := s.Decisions("R", start)
	if !ok {
		t.Fatalf("slot %d not decided", start)
	}
	return d
}

func TestTieBreakByGroupID(t *testing.T) {
	s, l := newTestScheduler(t)
	setupRegion(t, s, l, 3, nil)
	_, err := s.Issue("R", 1, 10, 50)
	must(t, err, l, "Issue")
	must(t, s.Advance(40), l, "Advance")
	assertInts(t, mustDecision(t, s, 10), []int{1}, "slot[10,20)")
	assertInts(t, mustDecision(t, s, 20), []int{2}, "slot[20,30)")
	assertInts(t, mustDecision(t, s, 30), []int{3}, "slot[30,40)")
	// Order covering [10,50) from the start; after 1,2,3 each served once,
	// slot [40,50) ties again and goes back to group 1.
	must(t, s.Advance(50), l, "Advance50")
	assertInts(t, mustDecision(t, s, 40), []int{1}, "slot[40,50) tie back to 1")
}

func TestLevelEqualsGroupCount(t *testing.T) {
	s, l := newTestScheduler(t)
	setupRegion(t, s, l, 3, nil)
	_, err := s.Issue("R", 3, 10, 20)
	must(t, err, l, "Issue")
	must(t, s.Advance(20), l, "Advance")
	assertInts(t, mustDecision(t, s, 10), []int{1, 2, 3}, "all groups")
	for g := 1; g <= 3; g++ {
		a, err := s.Accrued("R", g)
		must(t, err, l, "Accrued")
		if a != 10 {
			failLog(t, l, "group %d accrued %d want 10", g, a)
		}
	}
}

func TestBoundaryVsInteriorEffect(t *testing.T) {
	s, l := newTestScheduler(t)
	setupRegion(t, s, l, 3, nil)
	o, err := s.Issue("R", 1, 10, 30)
	must(t, err, l, "Issue")
	must(t, s.Advance(10), l, "stop on boundary 10")
	must(t, s.ChangeLevel(o, 2), l, "change at boundary")
	must(t, s.Advance(20), l, "leave 10")
	assertInts(t, mustDecision(t, s, 10), []int{1, 2}, "boundary edit applies")

	s2, l2 := newTestScheduler(t)
	setupRegion(t, s2, l2, 3, nil)
	o2, err := s2.Issue("R", 1, 10, 30)
	must(t, err, l2, "Issue")
	must(t, s2.Advance(11), l2, "inside slot")
	must(t, s2.ChangeLevel(o2, 2), l2, "change inside")
	must(t, s2.Advance(20), l2, "to boundary 20")
	assertInts(t, mustDecision(t, s2, 10), []int{1}, "interior edit leaves slot 10")
	must(t, s2.Advance(30), l2, "leave 20")
	// Slot10 g1 accrued 10; for slot20 the least-accrued are g2,g3 -> add g3.
	assertInts(t, mustDecision(t, s2, 20), []int{2, 3}, "applies from next slot by rank")
}

func TestOverlayMaxAndFallback(t *testing.T) {
	s, l := newTestScheduler(t)
	setupRegion(t, s, l, 4, nil)
	_, err := s.Issue("R", 2, 10, 40)
	must(t, err, l, "issue A level2")
	o2, err := s.Issue("R", 3, 10, 30)
	must(t, err, l, "issue B level3")
	must(t, s.Advance(20), l, "advance")
	assertInts(t, mustDecision(t, s, 10), []int{1, 2, 3}, "max=3")
	must(t, s.Advance(21), l, "enter slot [20,30)")
	must(t, s.Cancel(o2), l, "cancel B inside [20,30), effective 30")
	must(t, s.Advance(40), l, "advance")
	assertSet(t, mustDecision(t, s, 20), []int{1, 2, 4}, "max 3 in [20,30): g1..g3 accrue, g4 joins")
	got30 := mustDecision(t, s, 30)
	// After slot10 {1,2,3} and slot20 {4,1,2}: g1=g2=20, g3=g4=10; falling
	// back to level 2 picks the two least-accrued: g3 and g4.
	assertSet(t, got30, []int{3, 4}, "fallback to level 2 picks the two least-accrued")
}

func TestExemptAndReservedNotices(t *testing.T) {
	s, l := newTestScheduler(t)
	setupRegion(t, s, l, 2, nil)
	must(t, s.AddUser("u-exempt", "R", 1, Exempt, 0), l, "add exempt")
	must(t, s.AddUser("u-res", "R", 1, Reserved, 7), l, "add reserved")
	must(t, s.AddUser("u-norm", "R", 1, Normal, 0), l, "add normal")
	_, err := s.Issue("R", 1, 10, 20)
	must(t, err, l, "issue")
	pending, err := s.PendingNotices("R")
	must(t, err, l, "pending")
	seen := map[string]int64{}
	for _, n := range pending {
		seen[n.UserID] = n.Limit
	}
	if _, ok := seen["u-exempt"]; ok {
		failLog(t, l, "exempt user must not be notified: %+v", pending)
	}
	if seen["u-res"] != 7 {
		failLog(t, l, "reserved notice limit = %d want 7", seen["u-res"])
	}
	if seen["u-norm"] != 0 {
		failLog(t, l, "normal notice limit = %d want 0", seen["u-norm"])
	}
}

func TestConfirmAtSlotStartRejected(t *testing.T) {
	s, l := newTestScheduler(t)
	setupRegion(t, s, l, 2, map[int][]string{1: {"u1", "u2"}})
	_, err := s.Issue("R", 1, 10, 30)
	must(t, err, l, "issue")
	// Before the slot starts, confirmation is accepted.
	must(t, s.Confirm("u1", "R"), l, "early confirm")
	must(t, s.Advance(10), l, "stop exactly at slot start")
	// At/after the slot started, confirmation is rejected.
	if err := s.Confirm("u2", "R"); rejectKind(err) != RejectAfterDeadline {
		failLog(t, l, "confirm at/after start kind=%v want RejectAfterDeadline", err)
	}
	must(t, s.Advance(20), l, "freeze slot 10")
	assess := s.Assessments()
	var found bool
	for _, a := range assess {
		if a.UserID == "u2" && a.SlotStart == 10 {
			found = true
		}
	}
	if !found {
		failLog(t, l, "u2 should be assessed unconfirmed for slot 10: %+v", assess)
	}
}

func TestMoveRejectedInLimitedSlot(t *testing.T) {
	s, l := newTestScheduler(t)
	setupRegion(t, s, l, 2, map[int][]string{1: {"u1"}})
	_, err := s.Issue("R", 1, 10, 30)
	must(t, err, l, "issue")
	must(t, s.Advance(10), l, "arrive 10")
	if err := s.MoveUser("u1", "R", 2); rejectKind(err) != RejectInSlot {
		failLog(t, l, "move during limited slot kind=%v want RejectInSlot", err)
	}
	must(t, s.Advance(30), l, "leave window")
	if err := s.MoveUser("u1", "R", 2); err != nil {
		failLog(t, l, "move after window should succeed: %v", err)
	}
}

func TestAdvanceAcrossMultipleBoundaries(t *testing.T) {
	s, l := newTestScheduler(t)
	setupRegion(t, s, l, 2, nil)
	_, err := s.Issue("R", 1, 10, 50)
	must(t, err, l, "issue")
	must(t, s.Advance(50), l, "jump four slots")
	assertInts(t, mustDecision(t, s, 10), []int{1}, "slot10")
	assertInts(t, mustDecision(t, s, 20), []int{2}, "slot20")
	assertInts(t, mustDecision(t, s, 30), []int{1}, "slot30")
	assertInts(t, mustDecision(t, s, 40), []int{2}, "slot40")
	a1, _ := s.Accrued("R", 1)
	a2, _ := s.Accrued("R", 2)
	if a1 != 20 || a2 != 20 {
		failLog(t, l, "accrued %d/%d want 20/20", a1, a2)
	}
}

func TestFairnessInvariant(t *testing.T) {
	s, l := newTestScheduler(t)
	setupRegion(t, s, l, 4, nil)
	_, err := s.Issue("R", 2, 10, 130)
	must(t, err, l, "issue")
	must(t, s.Advance(130), l, "12 slots at level 2")
	var mn, mx int64 = 1 << 62, 0
	for g := 1; g <= 4; g++ {
		a, _ := s.Accrued("R", g)
		if a < mn {
			mn = a
		}
		if a > mx {
			mx = a
		}
	}
	if mx-mn > 10 {
		failLog(t, l, "accrued spread %d > slotLen 10 (min=%d max=%d)", mx-mn, mn, mx)
	}
}

func TestRejectPrecedence(t *testing.T) {
	s, l := newTestScheduler(t)
	setupRegion(t, s, l, 2, map[int][]string{1: {"u1"}})
	if _, err := s.Issue("R", 9, 10, 20); rejectKind(err) != RejectInvalid {
		failLog(t, l, "level overflow kind=%v", err)
	}
	if _, err := s.Issue("missing", 1, 10, 20); rejectKind(err) != RejectInvalid {
		failLog(t, l, "missing region kind=%v", err)
	}
	if err := s.ChangeLevel(999, 1); rejectKind(err) != RejectNoOrder {
		failLog(t, l, "missing order kind=%v", err)
	}
	// After the clock advances, a backward attempt is RejectClockBack even
	// when other arguments are also bad.
	must(t, s.Advance(10), l, "advance")
	if err := s.Advance(5); rejectKind(err) != RejectClockBack {
		failLog(t, l, "clock back kind=%v", err)
	}
	// Confirm for a user without any notice: no-notice is the last resort.
	must(t, s.AddUser("uGhost", "R", 2, Normal, 0), l, "add ghost user")
	if err := s.Confirm("uGhost", "R"); rejectKind(err) != RejectNoNotice {
		failLog(t, l, "no notice kind=%v", err)
	}
	// Precedence example: unknown user on Confirm must be Invalid (earlier
	// than AfterDeadline/NoNotice).
	if err := s.Confirm("nobody", "R"); rejectKind(err) != RejectInvalid {
		failLog(t, l, "unknown user confirm kind=%v", err)
	}
}
