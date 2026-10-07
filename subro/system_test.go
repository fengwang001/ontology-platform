package subro_test

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/subro"
)

func mustRegister(t *testing.T, s *subro.System, now int64, id string, totalLoss, paid, deadline, ratioBP int64) {
	t.Helper()
	if err := s.RegisterCase(now, id, totalLoss, paid, deadline, ratioBP); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

func mustRecover(t *testing.T, s *subro.System, now int64, id string, gross, expense int64) []subro.Adjustment {
	t.Helper()
	adjs, err := s.Recover(now, id, gross, expense)
	if err != nil {
		t.Fatalf("recover gross=%d expense=%d: %v", gross, expense, err)
	}
	return adjs
}

func wantErrKind(t *testing.T, err error, kind subro.ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", kind)
	}
	if !subro.IsKind(err, kind) {
		t.Fatalf("expected error kind %s, got %v", kind, err)
	}
}

// adjSummary renders adjustments as "party:delta" pairs for concise assertions.
func adjSummary(adjs []subro.Adjustment) []string {
	var out []string
	for _, a := range adjs {
		out = append(out, fmt.Sprintf("%s:%+d", a.Party, a.Delta))
	}
	return out
}

func wantAdjs(t *testing.T, adjs []subro.Adjustment, want ...string) {
	t.Helper()
	got := adjSummary(adjs)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("adjustments = %v, want %v", got, want)
	}
}

func TestRecoverExactlyAtDeadline(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 0, "c1", 1000, 400, 10, 10000)

	// now == deadline: accepted. net=500, cap=1000, uncomp=600.
	adjs := mustRecover(t, s, 10, "c1", 500, 0)
	wantAdjs(t, adjs, "insured:+500")

	// now == deadline+1: rejected, past deadline.
	_, err := s.Recover(11, "c1", 100, 0)
	wantErrKind(t, err, subro.ErrPastDeadline)
}

func TestNetExactlyEqualsCap(t *testing.T) {
	s := subro.NewSystem()
	// cap = 1000 * 5000 / 10000 = 500.
	mustRegister(t, s, 0, "c1", 1000, 600, 10, 5000)

	// net = 600 - 100 = 500 == cap: no excess, third party gets nothing.
	adjs := mustRecover(t, s, 1, "c1", 600, 100)
	wantAdjs(t, adjs, "insured:+400", "insurer:+100")

	snap, err := s.Snapshot("c1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Entitled.ThirdParty != 0 || snap.NetTotal != 500 || snap.Cap != 500 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestExpenseEqualsGross(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 0, "c1", 1000, 400, 10, 10000)

	// expense == gross: net = 0, nobody is entitled to anything.
	adjs := mustRecover(t, s, 1, "c1", 300, 300)
	wantAdjs(t, adjs)

	snap, err := s.Snapshot("c1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.NetTotal != 0 || snap.Entitled.Sum() != 0 || snap.Paid.Sum() != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if got := len(s.Recoveries("c1")); got != 1 {
		t.Fatalf("recoveries = %d, want 1 (rejected nothing, recovery registered)", got)
	}
}

func TestRatioDecreaseReverseClawback(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 0, "c1", 1000, 400, 10, 10000)

	// net=1000, cap=1000: insured 600, insurer 400.
	adjs := mustRecover(t, s, 1, "c1", 1000, 0)
	wantAdjs(t, adjs, "insured:+600", "insurer:+400")

	// cap drops to 900: excess 100 is clawed back from the insurer
	// first (reverse of the distribution order); insured untouched.
	adjs, err := s.AdjustRatio(2, "c1", 9000)
	if err != nil {
		t.Fatal(err)
	}
	wantAdjs(t, adjs, "insurer:-100", "third_party:+100")

	// cap drops to 500: insurer is fully clawed back first, then the
	// insured for the remaining 100; the excess goes to third party.
	adjs, err = s.AdjustRatio(3, "c1", 5000)
	if err != nil {
		t.Fatal(err)
	}
	wantAdjs(t, adjs, "insured:-100", "insurer:-300", "third_party:+400")

	snap, err := s.Snapshot("c1")
	if err != nil {
		t.Fatal(err)
	}
	want := subro.Entitlement{Insured: 500, Insurer: 0, ThirdParty: 500}
	if snap.Entitled != want || snap.Paid != want {
		t.Fatalf("snapshot = %+v, want entitled=paid=%+v", snap, want)
	}
}

func TestAdjustmentsAreMinimalDiffs(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 0, "c1", 1000, 400, 10, 10000)
	mustRecover(t, s, 1, "c1", 1000, 0)

	// One record per affected party, equal to the exact entitlement
	// diff (never a full clawback followed by a reissue).
	adjs, err := s.AdjustRatio(2, "c1", 9000)
	if err != nil {
		t.Fatal(err)
	}
	if len(adjs) != 2 {
		t.Fatalf("adjustments = %v, want exactly 2 records", adjSummary(adjs))
	}
	for _, a := range adjs {
		switch a.Party {
		case subro.PartyInsurer:
			if a.Delta != -100 || a.PaidAfter != 300 {
				t.Fatalf("insurer adjustment = %+v, want delta -100 paidAfter 300", a)
			}
		case subro.PartyThirdParty:
			if a.Delta != 100 || a.PaidAfter != 100 {
				t.Fatalf("third party adjustment = %+v, want delta +100 paidAfter 100", a)
			}
		default:
			t.Fatalf("unexpected adjustment for %s", a.Party)
		}
	}
}

func TestWaiveFallbackAndInsurerCap(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 0, "c1", 1000, 400, 10, 10000)

	// Waiver before any recovery: nothing to settle yet.
	adjs, err := s.Waive(5, "c1")
	if err != nil {
		t.Fatal(err)
	}
	wantAdjs(t, adjs)

	// net=1000, cap=1000: insured's 600-share slides to the insurer,
	// but the insurer is capped at its paid 400; 600 is excess.
	adjs = mustRecover(t, s, 6, "c1", 1000, 0)
	wantAdjs(t, adjs, "insurer:+400", "third_party:+600")

	snap, err := s.Snapshot("c1")
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Waived || snap.Entitled.Insured != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}

	// Second waiver: rejected, already waived.
	_, err = s.Waive(7, "c1")
	wantErrKind(t, err, subro.ErrAlreadyWaived)
}

func TestWaiveDeadlineAndPriority(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 0, "c1", 1000, 400, 10, 10000)

	// Waiver on the deadline day is accepted.
	if _, err := s.Waive(10, "c1"); err != nil {
		t.Fatal(err)
	}
	// After the deadline the past-deadline error wins over
	// already-waived.
	_, err := s.Waive(11, "c1")
	wantErrKind(t, err, subro.ErrPastDeadline)

	s2 := subro.NewSystem()
	mustRegister(t, s2, 0, "c2", 1000, 400, 10, 10000)
	if _, err := s2.Waive(11, "c2"); !subro.IsKind(err, subro.ErrPastDeadline) {
		t.Fatalf("waive after deadline: got %v", err)
	}
}

func TestSupplementChangesEntitlement(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 0, "c1", 1000, 200, 10, 10000)

	// net=1000: insured 800, insurer 200.
	adjs := mustRecover(t, s, 1, "c1", 1000, 0)
	wantAdjs(t, adjs, "insured:+800", "insurer:+200")

	// Supplement 300: insurer paid 200->500, uncomp 800->500.
	adjs, err := s.SupplementPayment(2, "c1", 300)
	if err != nil {
		t.Fatal(err)
	}
	wantAdjs(t, adjs, "insured:-300", "insurer:+300")

	snap, err := s.Snapshot("c1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.InsurerPaid != 500 || snap.Uncompensated != 500 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.InsurerPaid+snap.Uncompensated != snap.TotalLoss {
		t.Fatalf("paid + uncompensated != total loss: %+v", snap)
	}

	// 500 + 600 > 1000: exceeds total loss.
	_, err = s.SupplementPayment(3, "c1", 600)
	wantErrKind(t, err, subro.ErrExceedsTotalLoss)
	// Non-positive amount: invalid param.
	_, err = s.SupplementPayment(3, "c1", 0)
	wantErrKind(t, err, subro.ErrInvalidParam)
}

func TestClockRollback(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 5, "c1", 1000, 400, 10, 10000)

	_, err := s.Recover(3, "c1", 100, 0)
	wantErrKind(t, err, subro.ErrClockRollback)

	// The rejected op did not move the clock: now == lastNow is fine.
	adjs := mustRecover(t, s, 5, "c1", 100, 0)
	wantAdjs(t, adjs, "insured:+100")

	// Equal timestamps are allowed; smaller ones are not.
	if _, err := s.AdjustRatio(5, "c1", 5000); err != nil {
		t.Fatal(err)
	}
	_, err = s.AdjustRatio(4, "c1", 6000)
	wantErrKind(t, err, subro.ErrClockRollback)
}

func TestErrorPriority(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 10, "c1", 1000, 400, 10, 10000)

	// invalid param > clock rollback > case not found.
	_, err := s.Recover(5, "ghost", 100, 200) // expense > gross
	wantErrKind(t, err, subro.ErrInvalidParam)

	// clock rollback > case not found.
	_, err = s.Recover(5, "ghost", 100, 0)
	wantErrKind(t, err, subro.ErrClockRollback)

	// case not found > past deadline (unknown case, any now).
	_, err = s.Recover(10, "ghost", 100, 0)
	wantErrKind(t, err, subro.ErrCaseNotFound)

	// past deadline is reported for an existing case.
	_, err = s.Recover(11, "c1", 100, 0)
	wantErrKind(t, err, subro.ErrPastDeadline)
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 0, "c1", 1000, 400, 10, 10000)
	mustRecover(t, s, 5, "c1", 700, 100) // net 600: insured 600

	before, err := s.Snapshot("c1")
	if err != nil {
		t.Fatal(err)
	}
	beforeAdjs := s.Adjustments("c1")
	beforeRecs := s.Recoveries("c1")
	beforeStats := s.Stats()

	rejected := []func() error{
		func() error { _, e := s.Recover(-1, "c1", 10, 0); return e },        // invalid now
		func() error { _, e := s.Recover(5, "c1", 10, 20); return e },        // expense > gross
		func() error { _, e := s.Recover(4, "c1", 10, 0); return e },         // clock rollback
		func() error { _, e := s.Recover(5, "ghost", 10, 0); return e },      // no such case
		func() error { _, e := s.Recover(11, "c1", 10, 0); return e },        // past deadline
		func() error { _, e := s.SupplementPayment(5, "c1", 900); return e }, // exceeds total loss
		func() error { _, e := s.AdjustRatio(5, "c1", 10001); return e },     // ratio out of range
		func() error { return s.RegisterCase(5, "c1", 1, 0, 1, 0) },          // duplicate id
		func() error { return s.RegisterCase(5, "bad", 10, 20, 1, 0) },       // paid > loss
	}
	for i, op := range rejected {
		if err := op(); err == nil {
			t.Fatalf("rejected op %d unexpectedly accepted", i)
		}
	}

	after, err := s.Snapshot("c1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed by rejected ops:\nbefore %+v\nafter  %+v", before, after)
	}
	if !reflect.DeepEqual(beforeAdjs, s.Adjustments("c1")) {
		t.Fatal("adjustment log changed by rejected ops")
	}
	if !reflect.DeepEqual(beforeRecs, s.Recoveries("c1")) {
		t.Fatal("recovery log changed by rejected ops")
	}
	if beforeStats != s.Stats() {
		t.Fatalf("stats changed by rejected ops: %+v -> %+v", beforeStats, s.Stats())
	}

	// The clock was not advanced by any rejected op, in particular not
	// by the one carrying now=11: now=5 is still acceptable.
	if _, err := s.AdjustRatio(5, "c1", 8000); err != nil {
		t.Fatalf("clock moved by rejected op: %v", err)
	}
}

// runScript replays a fixed operation script and returns the system.
func runScript(t *testing.T) *subro.System {
	t.Helper()
	s := subro.NewSystem()
	mustRegister(t, s, 0, "a", 1000, 400, 10, 10000)
	mustRegister(t, s, 1, "b", 5000, 1000, 20, 2500)
	mustRecover(t, s, 2, "a", 500, 50)
	mustRecover(t, s, 3, "a", 700, 0)
	if _, err := s.Waive(4, "a"); err != nil {
		t.Fatal(err)
	}
	mustRecover(t, s, 5, "b", 3000, 300)
	if _, err := s.SupplementPayment(6, "b", 500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdjustRatio(7, "b", 5000); err != nil {
		t.Fatal(err)
	}
	mustRecover(t, s, 8, "b", 2000, 100)
	return s
}

func TestReplayDeterminism(t *testing.T) {
	s1 := runScript(t)
	s2 := runScript(t)
	for _, id := range []string{"a", "b"} {
		a1, err := s1.Snapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		a2, err := s2.Snapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a1, a2) {
			t.Fatalf("case %s snapshots differ:\n%+v\n%+v", id, a1, a2)
		}
		if !reflect.DeepEqual(s1.Adjustments(id), s2.Adjustments(id)) {
			t.Fatalf("case %s adjustment logs differ", id)
		}
		if !reflect.DeepEqual(s1.Recoveries(id), s2.Recoveries(id)) {
			t.Fatalf("case %s recovery logs differ", id)
		}
	}
}

func TestInvariantEntitledSumEqualsNet(t *testing.T) {
	s := runScript(t)
	for _, id := range []string{"a", "b"} {
		snap, err := s.Snapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Entitled.Sum() != snap.NetTotal {
			t.Fatalf("case %s: entitled sum %d != net total %d", id, snap.Entitled.Sum(), snap.NetTotal)
		}
		if snap.Paid != snap.Entitled {
			t.Fatalf("case %s: paid %+v != entitled %+v", id, snap.Paid, snap.Entitled)
		}
		if snap.InsurerPaid+snap.Uncompensated != snap.TotalLoss {
			t.Fatalf("case %s: paid + uncompensated != total loss", id)
		}
	}
}

func TestConcurrentRecoveries(t *testing.T) {
	s := subro.NewSystem()
	mustRegister(t, s, 0, "c1", 100000, 40000, 10, 10000)

	const workers = 8
	const perWorker = 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				// All calls carry now == deadline; equal timestamps are
				// allowed, so every call is accepted.
				if _, err := s.Recover(10, "c1", 20, 5); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()

	snap, err := s.Snapshot("c1")
	if err != nil {
		t.Fatal(err)
	}
	wantNet := int64(workers * perWorker * 15)
	if snap.NetTotal != wantNet {
		t.Fatalf("net total = %d, want %d", snap.NetTotal, wantNet)
	}
	if snap.Entitled.Sum() != snap.NetTotal || snap.Paid != snap.Entitled {
		t.Fatalf("invariant broken: %+v", snap)
	}
	// Per-party adjustment deltas sum to the final paid amounts.
	var sums [3]int64
	for _, a := range s.Adjustments("c1") {
		sums[a.Party] += a.Delta
	}
	if sums[0] != snap.Paid.Insured || sums[1] != snap.Paid.Insurer || sums[2] != snap.Paid.ThirdParty {
		t.Fatalf("adjustment sums %v != paid %+v", sums, snap.Paid)
	}
}

func TestSettleWorkIsConstant(t *testing.T) {
	s := subro.NewSystem()
	// Many cases and a long recovery history must not increase the
	// per-operation settlement work.
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("case-%d", i)
		mustRegister(t, s, 0, id, 1000, 400, 100000, 10000)
	}
	for i := 0; i < 2000; i++ {
		mustRecover(t, s, int64(i), "case-0", 10, 1)
	}
	st := s.Stats()
	// Every accepted operation settles exactly 3 parties, regardless
	// of the number of cases or the recovery history length.
	if st.SettleIterations != 3*st.AcceptedOps {
		t.Fatalf("settle iterations = %d, want 3 * accepted ops = %d",
			st.SettleIterations, 3*st.AcceptedOps)
	}
	if st.Recoveries != 2000 || st.Cases != 50 {
		t.Fatalf("stats = %+v", st)
	}
}
