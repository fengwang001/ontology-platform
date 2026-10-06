package lease_test

// All lease service tests: scenario coverage, O(depth) liability proof and
// randomized differential testing against the independent naive model.
import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"ontology/lease"
	"ontology/lease/naivemodel"
)

// Verifiable proof that answering liability questions for one arrears touches
// only that arrears' frozen chain (length = depth), never the system-wide
// lease table.
//
// The service builds Arrears.Chain = [debtor, ..., root] at the moment the
// arrears is raised (see lease.liabilityChain). Pay/LiableParty/
// ReimbursableParties iterate that slice only. The benchmark below grows the
// total lease count far beyond the chain depth while the queried chain stays
// short: cost must remain flat in the total count. The test additionally
// asserts the structural fact directly (Chain length == depth).

func buildLiableFixture(t *testing.T, totalLeases int) (*lease.Service, int) {
	t.Helper()
	cfg := lease.Config{RentFactorPct: 200, MaxDepth: totalLeases + 10, GraceDays: 1, PayDay: 1}
	s := lease.NewService(cfg)
	// One long chain of exactly depth 3: master -> sub -> subsub.
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 100000, Rent: 100, Now: 0}), "m")
	must(t, s.GrantGeneral(lease.GrantGeneralOp{Landlord: 10, Tenant: 11, Root: 1, Now: 0}), "g1")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 2, Parent: 1, Tenant: 12, Start: 0, End: 100000, Rent: 100, Now: 0}), "s1")
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 3, Landlord: 10, Root: 1, Now: 0}), "g2")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 3, Parent: 2, Tenant: 13, Start: 0, End: 100000, Rent: 100, Now: 0}), "s2")
	// Many unrelated masters, growing the global lease table.
	for id := lease.LeaseID(100); id < lease.LeaseID(100+totalLeases); id++ {
		must(t, s.CreateMaster(lease.CreateMasterOp{
			ID: id, Landlord: 10, Tenant: lease.Party(1000 + id),
			Start: 0, End: 100000, Rent: 100, Now: 0,
		}), "filler")
	}
	must(t, s.Advance(lease.AdvanceOp{Now: 2}), "accrue")
	var deepID int
	for _, a := range s.Snapshot().Arrears {
		if a.Lease == 3 {
			deepID = a.ID
			if len(a.Chain) != 3 {
				t.Fatalf("chain length %d, want 3", len(a.Chain))
			}
		}
	}
	if deepID == 0 {
		t.Fatal("deep arrears missing")
	}
	return s, deepID
}

func TestLiabilityChainIsDepthSized(t *testing.T) {
	s, id := buildLiableFixture(t, 50)
	party, ok := s.LiableParty(id)
	if !ok || party != 13 {
		t.Fatalf("liable party = %v ok=%v, want 13", party, ok)
	}
	if got := s.ReimbursableParties(id); len(got) != 0 {
		t.Fatalf("no settlements yet, got %v", got)
	}
	must(t, s.Pay(lease.PayOp{ArrearsID: id, By: 11, AtLease: 1, Amount: 100, Now: 3}), "pay")
	if got := s.ReimbursableParties(id); len(got) != 1 || got[0] != 11 {
		t.Fatalf("reimbursable = %v, want [11]", got)
	}
}

func BenchmarkLiableParty(b *testing.B) {
	// Query cost for one depth-3 arrears while the global lease table grows
	// 10 -> 100 -> 1000. ns/op must stay roughly flat; see test output.
	for _, n := range []int{10, 100, 1000} {
		n := n
		b.Run("", func(b *testing.B) {
			s, id := buildLiableFixture(&testing.T{}, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, ok := s.LiableParty(id); !ok {
					b.Fatal("missing")
				}
			}
		})
	}
}

func testCfg() lease.Config {
	return lease.Config{RentFactorPct: 120, MaxDepth: 3, GraceDays: 5, PayDay: 1}
}

func codeOf(err error) lease.Code {
	if oe, ok := err.(*lease.OpError); ok {
		return oe.Code
	}
	return 0
}

func must(t *testing.T, err error, step string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", step, err)
	}
}

func wantErr(t *testing.T, err error, c lease.Code, step string) {
	t.Helper()
	if codeOf(err) != c {
		t.Fatalf("%s: want %v, got %v", step, c, err)
	}
}

// Sublease end exactly equal to parent end is allowed.
func TestEndEqualsParentEnd(t *testing.T) {
	s := lease.NewService(testCfg())
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 60, Rent: 100, Now: 0}), "master")
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 2, Landlord: 10, Root: 1, Now: 0}), "consent")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 2, Parent: 1, Tenant: 12, Start: 0, End: 60, Rent: 100, Now: 0}), "sub")
	if l := s.Snapshot().Leases[2]; !l.Active || l.End != 60 {
		t.Fatalf("sublease not active with equal end: %+v", l)
	}
}

// Rent exactly at the factor limit passes; one unit above fails.
func TestRentFactorBoundary(t *testing.T) {
	cfg := testCfg() // 120%
	s := lease.NewService(cfg)
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 90, Rent: 100, Now: 0}), "master")
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 2, Landlord: 10, Root: 1, Now: 0}), "consent2")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 2, Parent: 1, Tenant: 12, Start: 0, End: 60, Rent: 120, Now: 0}), "rent=120")
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 3, Landlord: 10, Root: 1, Now: 1}), "consent3")
	wantErr(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 3, Parent: 1, Tenant: 13, Start: 0, End: 60, Rent: 121, Now: 1}),
		lease.ErrRentExceedsLimit, "rent=121")
}

// One-shot consent authorizes exactly one sublease; the second fails.
func TestOneShotConsentUsedOnce(t *testing.T) {
	s := lease.NewService(testCfg())
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 120, Rent: 100, Now: 0}), "master")
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 2, Landlord: 10, Root: 1, Now: 0}), "consent")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 2, Parent: 1, Tenant: 12, Start: 0, End: 60, Rent: 100, Now: 0}), "first")
	must(t, s.Advance(lease.AdvanceOp{Now: 60}), "expire first")
	wantErr(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 3, Parent: 1, Tenant: 13, Start: 60, End: 90, Rent: 100, Now: 60}),
		lease.ErrNoConsent, "second without consent")
}

// General consent covers all later subleases until revoked; revocation does
// not touch established subleases.
func TestGeneralConsentRevoke(t *testing.T) {
	s := lease.NewService(testCfg())
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 300, Rent: 100, Now: 0}), "master")
	must(t, s.GrantGeneral(lease.GrantGeneralOp{Landlord: 10, Tenant: 11, Root: 1, Now: 0}), "grant")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 2, Parent: 1, Tenant: 12, Start: 0, End: 60, Rent: 100, Now: 0}), "sub a")
	must(t, s.Advance(lease.AdvanceOp{Now: 60}), "advance")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 3, Parent: 1, Tenant: 13, Start: 60, End: 90, Rent: 100, Now: 60}), "sub b")
	must(t, s.RevokeGeneral(lease.RevokeGeneralOp{Landlord: 10, Tenant: 11, Root: 1, Now: 61}), "revoke")
	wantErr(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 4, Parent: 1, Tenant: 14, Start: 90, End: 120, Rent: 100, Now: 90}),
		lease.ErrNoConsent, "after revoke")
	if !s.Snapshot().Leases[3].Active || s.Snapshot().Leases[2].End <= 0 {
		t.Fatal("established subleases must survive revocation")
	}
}

// Depth exactly D is allowed; one level deeper is rejected.
func TestDepthLimit(t *testing.T) {
	cfg := testCfg() // MaxDepth 3
	s := lease.NewService(cfg)
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 300, Rent: 1000, Now: 0}), "master")
	must(t, s.GrantGeneral(lease.GrantGeneralOp{Landlord: 10, Tenant: 11, Root: 1, Now: 0}), "g1")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 2, Parent: 1, Tenant: 12, Start: 0, End: 300, Rent: 1000, Now: 0}), "d2")
	// consent for tenant 12's onward sublease: grant one-shots named by id
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 3, Landlord: 10, Root: 1, Now: 0}), "g2")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 3, Parent: 2, Tenant: 13, Start: 0, End: 300, Rent: 1000, Now: 0}), "d3")
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 4, Landlord: 10, Root: 1, Now: 1}), "g3")
	wantErr(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 4, Parent: 3, Tenant: 14, Start: 0, End: 300, Rent: 1000, Now: 1}),
		lease.ErrDepthExceeded, "d4 rejected")
}

// Arrears appear exactly at due + G and not one day earlier.
func TestGraceBoundary(t *testing.T) {
	cfg := testCfg() // PayDay 1, G 5
	s := lease.NewService(cfg)
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 90, Rent: 100, Now: 0}), "master")
	must(t, s.Advance(lease.AdvanceOp{Now: 5}), "day 5")
	if len(s.Snapshot().Arrears) != 0 {
		t.Fatalf("no arrears before day 6, got %+v", s.Snapshot().Arrears)
	}
	must(t, s.Advance(lease.AdvanceOp{Now: 6}), "day 6")
	if len(s.Snapshot().Arrears) != 1 || s.Snapshot().Arrears[0].Due != 1 || s.Snapshot().Arrears[0].Amount != 100 {
		t.Fatalf("expected one arrears due day 1: %+v", s.Snapshot().Arrears)
	}
}

// Multi-level settlement: the ancestor's payment discharges the debt below it
// and creates exactly one equal recourse against the actual debtor.
func TestMultiLevelSettlement(t *testing.T) {
	cfg := testCfg()
	s := lease.NewService(cfg)
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 300, Rent: 100, Now: 0}), "master")
	must(t, s.GrantGeneral(lease.GrantGeneralOp{Landlord: 10, Tenant: 11, Root: 1, Now: 0}), "g1")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 2, Parent: 1, Tenant: 12, Start: 0, End: 300, Rent: 100, Now: 0}), "d2")
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 3, Landlord: 10, Root: 1, Now: 0}), "g2")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 3, Parent: 2, Tenant: 13, Start: 0, End: 300, Rent: 100, Now: 0}), "d3")
	must(t, s.Advance(lease.AdvanceOp{Now: 6}), "accrue")
	a := s.Snapshot().Arrears
	if len(a) != 3 {
		t.Fatalf("want 3 arrears (one per lease), got %d", len(a))
	}
	// find the deepest lease's arrears (snapshot order is deterministic)
	var deep lease.Arrears
	for _, x := range a {
		if x.Lease == 3 {
			deep = x
		}
	}
	if deep.ID == 0 {
		t.Fatal("expected arrears for lease 3")
	}
	must(t, s.Pay(lease.PayOp{ArrearsID: deep.ID, By: 11, AtLease: 1, Amount: 40, Now: 7}), "partial")
	must(t, s.Pay(lease.PayOp{ArrearsID: deep.ID, By: 11, AtLease: 1, Amount: 60, Now: 8}), "rest")
	wantErr(t, s.Pay(lease.PayOp{ArrearsID: deep.ID, By: 11, AtLease: 1, Amount: 1, Now: 9}),
		lease.ErrStateNotAllowed, "double settle")
	snap := s.Snapshot()
	if len(snap.Recourses) != 2 {
		t.Fatalf("want 2 recourses, got %+v", snap.Recourses)
	}
	total := 0
	for _, r := range snap.Recourses {
		if r.From != 13 || r.To != 11 {
			t.Fatalf("recourse parties wrong: %+v", r)
		}
		total += r.Amount
	}
	if total != 100 {
		t.Fatalf("recourse total %d != arrears 100", total)
	}
	// stranger to the chain cannot settle
	wantErr(t, s.Pay(lease.PayOp{ArrearsID: snap.Arrears[0].ID, By: 99, AtLease: 1, Amount: 1, Now: 9}),
		lease.ErrStateNotAllowed, "stranger")
}

// Cascade termination and independent recognition: the recognized child is
// promoted to a direct lease with unchanged term/rent; its subtree shifts up.
func TestCascadeAndPromotion(t *testing.T) {
	s := lease.NewService(testCfg())
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 60, Rent: 100, Now: 0}), "master")
	must(t, s.GrantGeneral(lease.GrantGeneralOp{Landlord: 10, Tenant: 11, Root: 1, Now: 0}), "g1")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 2, Parent: 1, Tenant: 12, Start: 0, End: 60, Rent: 100, Now: 0}), "d2")
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 3, Landlord: 10, Root: 1, Now: 0}), "g2")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 3, Parent: 2, Tenant: 13, Start: 0, End: 60, Rent: 100, Now: 0}), "d3")
	must(t, s.Recognize(lease.RecognizeOp{Lease: 2, Landlord: 10, Now: 1}), "recognize 2")
	must(t, s.Terminate(lease.TerminateOp{Lease: 1, By: 10, Now: 20}), "terminate master")
	snap := s.Snapshot()
	if snap.Leases[1].Active {
		t.Fatal("master must be inactive")
	}
	l2 := snap.Leases[2]
	if !l2.Active || l2.Parent != 0 || l2.Depth != 1 || l2.Root != 2 || l2.Landlord != 10 ||
		l2.Start != 0 || l2.End != 60 || l2.Rent != 100 {
		t.Fatalf("lease 2 not promoted correctly: %+v", l2)
	}
	l3 := snap.Leases[3]
	if !l3.Active || l3.Parent != 2 || l3.Depth != 2 || l3.Root != 2 {
		t.Fatalf("lease 3 not shifted under promoted root: %+v", l3)
	}
}

// Without recognition the whole subtree is terminated by the cascade.
func TestCascadeWithoutRecognition(t *testing.T) {
	s := lease.NewService(testCfg())
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 60, Rent: 100, Now: 0}), "master")
	must(t, s.GrantGeneral(lease.GrantGeneralOp{Landlord: 10, Tenant: 11, Root: 1, Now: 0}), "g1")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 2, Parent: 1, Tenant: 12, Start: 0, End: 60, Rent: 100, Now: 0}), "d2")
	must(t, s.Terminate(lease.TerminateOp{Lease: 1, By: 10, Now: 20}), "terminate")
	snap := s.Snapshot()
	if snap.Leases[1].Active || snap.Leases[2].Active || snap.Leases[2].End != 20 {
		t.Fatalf("whole chain should be terminated at 20: %+v %+v", snap.Leases[1], snap.Leases[2])
	}
}

// Exit is refused while an unrecognized active child exists, allowed once the
// child is recognized (it then promotes), and surviving debts remain.
func TestExitRules(t *testing.T) {
	s := lease.NewService(testCfg())
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 300, Rent: 100, Now: 0}), "master")
	must(t, s.GrantGeneral(lease.GrantGeneralOp{Landlord: 10, Tenant: 11, Root: 1, Now: 0}), "g1")
	must(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 2, Parent: 1, Tenant: 12, Start: 0, End: 300, Rent: 100, Now: 0}), "d2")
	wantErr(t, s.Exit(lease.ExitOp{Lease: 1, By: 11, Now: 10}), lease.ErrStateNotAllowed, "exit blocked")
	if !s.Snapshot().Leases[1].Active {
		t.Fatal("rejected exit must leave master active")
	}
	must(t, s.Advance(lease.AdvanceOp{Now: 6}), "accrue master arrears")
	must(t, s.Recognize(lease.RecognizeOp{Lease: 2, Landlord: 10, Now: 10}), "recognize")
	must(t, s.Exit(lease.ExitOp{Lease: 1, By: 11, Now: 11}), "exit allowed")
	snap := s.Snapshot()
	if snap.Leases[1].Active {
		t.Fatal("master should be gone")
	}
	if !snap.Leases[2].Active || snap.Leases[2].Depth != 1 {
		t.Fatalf("child should be promoted: %+v", snap.Leases[2])
	}
	var debt bool
	for _, a := range snap.Arrears {
		if a.Lease == 1 && a.Amount == 100 {
			debt = true
		}
	}
	if !debt {
		t.Fatal("arrears of exited tenant must survive")
	}
}

// Fixed error precedence: invalid params beat clock rollback; clock rollback
// beats missing lease; and a rejected op leaves no trace.
func TestErrorOrderAndNoTrace(t *testing.T) {
	s := lease.NewService(testCfg())
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 60, Rent: 100, Now: 10}), "master")
	// invalid params despite clock going backwards
	wantErr(t, s.CreateMaster(lease.CreateMasterOp{ID: 0, Now: 5}), lease.ErrInvalid, "invalid before clock")
	// clock rollback with valid params
	wantErr(t, s.Advance(lease.AdvanceOp{Now: 9}), lease.ErrClockRollback, "clock rollback")
	// missing lease with valid clock
	wantErr(t, s.Recognize(lease.RecognizeOp{Lease: 99, Landlord: 10, Now: 10}), lease.ErrLeaseNotFound, "missing")
	// no consent precedes term/rent/depth/state checks
	wantErr(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 7, Parent: 1, Tenant: 77, Start: 0, End: 9999, Rent: 9999, Now: 10}),
		lease.ErrNoConsent, "consent ordered first")
	// term check precedes rent and depth
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 7, Landlord: 10, Root: 1, Now: 10}), "consent")
	wantErr(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 7, Parent: 1, Tenant: 77, Start: 0, End: 9999, Rent: 9999, Now: 10}),
		lease.ErrTermOutOfRange, "term before rent")
	// self sublease is a state error
	must(t, s.GrantOneShot(lease.GrantOneShotOp{ID: 8, Landlord: 10, Root: 1, Now: 10}), "grant")
	wantErr(t, s.CreateSublease(lease.CreateSubleaseOp{ID: 8, Parent: 1, Tenant: 11, Start: 0, End: 30, Rent: 10, Now: 10}),
		lease.ErrStateNotAllowed, "self sublease")
	snap := s.Snapshot()
	if _, exists := snap.Leases[7]; exists {
		t.Fatal("rejected sublease must not exist")
	}
	if snap.Now != 10 {
		t.Fatalf("rejected op moved clock to %d", snap.Now)
	}
}

// At most one active child under a lease: concurrent attempts serialize, only
// one wins.
func TestConcurrentSubleases(t *testing.T) {
	s := lease.NewService(testCfg())
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 300, Rent: 100, Now: 0}), "master")
	must(t, s.GrantGeneral(lease.GrantGeneralOp{Landlord: 10, Tenant: 11, Root: 1, Now: 0}), "grant")
	const n = 16
	errs := make(chan error, n)
	for i := 2; i < 2+n; i++ {
		go func(id int) {
			errs <- s.CreateSublease(lease.CreateSubleaseOp{
				ID: lease.LeaseID(id), Parent: 1, Tenant: lease.Party(100 + id),
				Start: 0, End: 60, Rent: 100, Now: 1,
			})
		}(i)
	}
	ok := 0
	for i := 0; i < n; i++ {
		if <-errs == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one concurrent sublease must win, got %d", ok)
	}
}

// Concurrent settlements of the same arrears must not overpay it: exactly the
// first full payment wins, all later attempts are rejected.
func TestConcurrentPaymentsNoDoubleSettle(t *testing.T) {
	s := lease.NewService(testCfg())
	must(t, s.CreateMaster(lease.CreateMasterOp{ID: 1, Landlord: 10, Tenant: 11, Start: 0, End: 90, Rent: 100, Now: 0}), "master")
	must(t, s.Advance(lease.AdvanceOp{Now: 6}), "accrue")
	id := s.Snapshot().Arrears[0].ID
	const n = 24
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			errs <- s.Pay(lease.PayOp{ArrearsID: id, By: 11, AtLease: 1, Amount: 100, Now: 7})
		}()
	}
	ok := 0
	for i := 0; i < n; i++ {
		if <-errs == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one payment must win, got %d", ok)
	}
	a := s.Snapshot().Arrears[0]
	if a.Paid != 100 || len(s.Snapshot().Recourses) != 1 {
		t.Fatalf("paid=%d recourses=%+v", a.Paid, s.Snapshot().Recourses)
	}
}

// Differential testing: random operation sequences are replayed against the
// real service and the independently written naive model. Every step logs its
// input, result (ok / first error code) and the rule that decided it; after
// each step the full snapshots must be identical.

type dlog struct{ b strings.Builder }

func (l *dlog) logf(format string, args ...any) { fmt.Fprintf(&l.b, format+"\n", args...) }

func codeStr(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func cmpSnap(a, b lease.Snapshot) string {
	if a.Now != b.Now {
		return fmt.Sprintf("now %d != %d", a.Now, b.Now)
	}
	if !reflect.DeepEqual(a.Leases, b.Leases) {
		return fmt.Sprintf("leases differ\nreal: %v\nnaive:%v", a.Leases, b.Leases)
	}
	if !reflect.DeepEqual(a.Arrears, b.Arrears) {
		return fmt.Sprintf("arrears differ\nreal: %v\nnaive:%v", a.Arrears, b.Arrears)
	}
	if !reflect.DeepEqual(a.Recourses, b.Recourses) {
		return fmt.Sprintf("recourses differ\nreal: %v\nnaive:%v", a.Recourses, b.Recourses)
	}
	if !reflect.DeepEqual(a.Consents, b.Consents) {
		return fmt.Sprintf("consents differ\nreal: %+v\nnaive:%+v", a.Consents, b.Consents)
	}
	return ""
}

type dstep struct {
	name   string
	why    string
	applyR func(*lease.Service) error
	applyN func(*naivemodel.Model) error
}

type dstate struct {
	rng      *rand.Rand
	cfg      lease.Config
	known    []lease.LeaseID
	nextID   lease.LeaseID
	now      int
	landlord lease.Party
	tenantOf map[lease.LeaseID]lease.Party
}

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			runSequence(t, seed, 400)
		})
	}
}

func runSequence(t *testing.T, seed int64, nsteps int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	cfg := lease.Config{
		RentFactorPct: 100 + rng.Intn(4)*25,
		MaxDepth:      2 + rng.Intn(3),
		GraceDays:     3 + rng.Intn(5),
		PayDay:        1 + rng.Intn(10),
	}
	real := lease.NewService(cfg)
	naive := naivemodel.New(cfg)
	lg := &dlog{}
	lg.logf("seed=%d cfg=%+v", seed, cfg)

	st := &dstate{
		rng: rng, cfg: cfg, nextID: 1, landlord: 1,
		tenantOf: map[lease.LeaseID]lease.Party{},
	}

	for i := 0; i < nsteps; i++ {
		var step dstep
		if len(st.known) == 0 || rng.Intn(7) == 0 {
			step = st.masterStep()
		} else {
			step = st.randomStep(real.Snapshot())
		}
		er := step.applyR(real)
		en := step.applyN(naive)
		lg.logf("step %3d %-24s | %-60s | real=%-32s naive=%s",
			i, step.name, step.why, codeStr(er), codeStr(en))
		if codeStr(er) != codeStr(en) {
			t.Fatalf("seed %d step %d %s result mismatch:\nreal=%v\nnaive=%v\n\nLOG:\n%s",
				seed, i, step.name, er, en, lg.b.String())
		}
		if diff := cmpSnap(real.Snapshot(), naive.Snapshot()); diff != "" {
			t.Fatalf("seed %d step %d %s snapshot mismatch:\n%s\n\nLOG:\n%s",
				seed, i, step.name, diff, lg.b.String())
		}
		snap := real.Snapshot()
		for id, l := range snap.Leases {
			if _, seen := st.tenantOf[id]; !seen {
				st.tenantOf[id] = l.Tenant
				st.known = append(st.known, id)
			} else {
				st.tenantOf[id] = l.Tenant
			}
		}
		if rng.Intn(2) == 0 {
			st.now += rng.Intn(10)
		}
	}
	t.Logf("seed %d: %d steps matched; log tail:\n%s", seed, nsteps, tailLog(lg.b.String(), 900))
}

func tailLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

func (st *dstate) masterStep() dstep {
	id := st.nextID
	st.nextID++
	tenant := lease.Party(100 + int(id))
	start := st.now
	end := start + 30*(1+st.rng.Intn(8))
	rent := 50 + st.rng.Intn(200)
	op := lease.CreateMasterOp{
		ID: id, Landlord: st.landlord, Tenant: tenant,
		Start: start, End: end, Rent: rent, Now: st.now,
	}
	return dstep{
		name:   "CreateMaster",
		why:    fmt.Sprintf("id=%d tenant=%d [%d,%d) rent=%d now=%d", id, tenant, start, end, rent, st.now),
		applyR: func(s *lease.Service) error { return s.CreateMaster(op) },
		applyN: func(m *naivemodel.Model) error { return m.CreateMaster(op) },
	}
}

func (st *dstate) randomStep(snap lease.Snapshot) dstep {
	switch st.rng.Intn(10) {
	case 0, 1:
		return st.subleaseStep(snap)
	case 2:
		return st.consentStep(snap, false)
	case 3:
		return st.consentStep(snap, true)
	case 4:
		return st.recognizeStep(snap)
	case 5:
		return st.payStep(snap)
	case 6:
		return st.terminateStep(snap)
	case 7:
		return st.exitStep(snap)
	default:
		return st.advanceStep()
	}
}

func (st *dstate) activeLeases(snap lease.Snapshot) []lease.Lease {
	var out []lease.Lease
	for _, l := range snap.Leases {
		if l.Active {
			out = append(out, l)
		}
	}
	return out
}

func (st *dstate) subleaseStep(snap lease.Snapshot) dstep {
	act := st.activeLeases(snap)
	if len(act) == 0 {
		return st.advanceStep()
	}
	parent := act[st.rng.Intn(len(act))]
	id := st.nextID
	st.nextID++
	tenant := lease.Party(200 + st.rng.Intn(100000))
	// half the time pick a term/rent inside the parent envelope
	start, end := parent.Start, parent.End
	if st.rng.Intn(2) == 0 && parent.End-parent.Start > 2 {
		lo := parent.Start
		hi := parent.End
		start = lo + st.rng.Intn(hi-lo)
		end = start + 1 + st.rng.Intn(hi-start)
	}
	rent := st.rng.Intn(parent.Rent*st.cfg.RentFactorPct/100 + 40)
	op := lease.CreateSubleaseOp{
		ID: id, Parent: parent.ID, Tenant: tenant,
		Start: start, End: end, Rent: rent, Now: st.now,
	}
	return dstep{
		name: "CreateSublease",
		why: fmt.Sprintf("id=%d parent=%d tenant=%d [%d,%d) rent=%d now=%d (parent [%d,%d) rent=%d depth=%d)",
			id, parent.ID, tenant, start, end, rent, st.now,
			parent.Start, parent.End, parent.Rent, parent.Depth),
		applyR: func(s *lease.Service) error { return s.CreateSublease(op) },
		applyN: func(m *naivemodel.Model) error { return m.CreateSublease(op) },
	}
}

func (st *dstate) consentStep(snap lease.Snapshot, oneShot bool) dstep {
	// choose a master/direct root
	var roots []lease.Lease
	for _, l := range snap.Leases {
		if l.Active && l.Depth == 1 {
			roots = append(roots, l)
		}
	}
	if len(roots) == 0 {
		return st.advanceStep()
	}
	root := roots[st.rng.Intn(len(roots))]
	if oneShot {
		id := st.nextID
		st.nextID++
		op := lease.GrantOneShotOp{ID: id, Landlord: st.landlord, Root: root.ID, Now: st.now}
		return dstep{
			name:   "GrantOneShot",
			why:    fmt.Sprintf("consent names future sublease %d on root %d", id, root.ID),
			applyR: func(s *lease.Service) error { return s.GrantOneShot(op) },
			applyN: func(m *naivemodel.Model) error { return m.GrantOneShot(op) },
		}
	}
	// general grant for a tenant appearing in the chain (or a fresh one)
	var tenant lease.Party
	if st.rng.Intn(2) == 0 {
		tenant = lease.Party(11 + st.rng.Intn(30))
	} else {
		for _, l := range snap.Leases {
			if l.Root == root.Root {
				tenant = l.Tenant
				break
			}
		}
	}
	if st.rng.Intn(3) == 0 {
		op := lease.RevokeGeneralOp{Landlord: st.landlord, Tenant: tenant, Root: root.Root, Now: st.now}
		return dstep{
			name:   "RevokeGeneral",
			why:    fmt.Sprintf("revoke general consent tenant=%d root=%d", tenant, root.Root),
			applyR: func(s *lease.Service) error { return s.RevokeGeneral(op) },
			applyN: func(m *naivemodel.Model) error { return m.RevokeGeneral(op) },
		}
	}
	op := lease.GrantGeneralOp{Landlord: st.landlord, Tenant: tenant, Root: root.Root, Now: st.now}
	return dstep{
		name:   "GrantGeneral",
		why:    fmt.Sprintf("general consent tenant=%d root=%d", tenant, root.Root),
		applyR: func(s *lease.Service) error { return s.GrantGeneral(op) },
		applyN: func(m *naivemodel.Model) error { return m.GrantGeneral(op) },
	}
}

func (st *dstate) recognizeStep(snap lease.Snapshot) dstep {
	var subs []lease.Lease
	for _, l := range snap.Leases {
		if l.Active && l.Depth > 1 {
			subs = append(subs, l)
		}
	}
	if len(subs) == 0 {
		return st.advanceStep()
	}
	l := subs[st.rng.Intn(len(subs))]
	op := lease.RecognizeOp{Lease: l.ID, Landlord: st.landlord, Now: st.now}
	return dstep{
		name:   "Recognize",
		why:    fmt.Sprintf("recognize lease=%d depth=%d recognized=%v", l.ID, l.Depth, l.Recognized),
		applyR: func(s *lease.Service) error { return s.Recognize(op) },
		applyN: func(m *naivemodel.Model) error { return m.Recognize(op) },
	}
}

func (st *dstate) payStep(snap lease.Snapshot) dstep {
	if len(snap.Arrears) == 0 {
		return st.advanceStep()
	}
	a := snap.Arrears[st.rng.Intn(len(snap.Arrears))]
	// pick a level on the liability chain, or sometimes a bogus lease id
	var atLease lease.LeaseID
	var by lease.Party
	if st.rng.Intn(8) == 0 {
		atLease = lease.LeaseID(9000 + st.rng.Intn(50))
		by = lease.Party(500 + st.rng.Intn(50))
	} else {
		atLease = a.Chain[st.rng.Intn(len(a.Chain))]
		if l, ok := snap.Leases[atLease]; ok {
			by = l.Tenant
		}
		if st.rng.Intn(6) == 0 {
			by = lease.Party(999) // wrong payer
		}
	}
	remaining := a.Amount - a.Paid
	amount := 1
	if remaining > 1 && st.rng.Intn(2) == 0 {
		amount = remaining
	} else if remaining > 0 {
		amount = 1 + st.rng.Intn(remaining)
	}
	if st.rng.Intn(10) == 0 {
		amount = remaining + 1 // overpay
	}
	op := lease.PayOp{ArrearsID: a.ID, By: by, AtLease: atLease, Amount: amount, Now: st.now}
	return dstep{
		name: "Pay",
		why: fmt.Sprintf("arrears=%d lease=%d amount=%d remaining=%d by=%d at=%d",
			a.ID, a.Lease, amount, remaining, by, atLease),
		applyR: func(s *lease.Service) error { return s.Pay(op) },
		applyN: func(m *naivemodel.Model) error { return m.Pay(op) },
	}
}

func (st *dstate) terminateStep(snap lease.Snapshot) dstep {
	act := st.activeLeases(snap)
	if len(act) == 0 {
		return st.advanceStep()
	}
	l := act[st.rng.Intn(len(act))]
	by := l.Tenant
	if st.rng.Intn(2) == 0 {
		by = st.landlord
	}
	if st.rng.Intn(8) == 0 {
		by = lease.Party(4242) // unauthorized
	}
	day := 0 // defaults to now
	op := lease.TerminateOp{Lease: l.ID, By: by, Day: day, Now: st.now}
	return dstep{
		name:   "Terminate",
		why:    fmt.Sprintf("lease=%d by=%d day=now(%d)", l.ID, by, st.now),
		applyR: func(s *lease.Service) error { return s.Terminate(op) },
		applyN: func(m *naivemodel.Model) error { return m.Terminate(op) },
	}
}

func (st *dstate) exitStep(snap lease.Snapshot) dstep {
	act := st.activeLeases(snap)
	if len(act) == 0 {
		return st.advanceStep()
	}
	l := act[st.rng.Intn(len(act))]
	by := l.Tenant
	if st.rng.Intn(8) == 0 {
		by = lease.Party(4242)
	}
	op := lease.ExitOp{Lease: l.ID, By: by, Now: st.now}
	return dstep{
		name:   "Exit",
		why:    fmt.Sprintf("lease=%d tenant=%d by=%d", l.ID, l.Tenant, by),
		applyR: func(s *lease.Service) error { return s.Exit(op) },
		applyN: func(m *naivemodel.Model) error { return m.Exit(op) },
	}
}

func (st *dstate) advanceStep() dstep {
	now := st.now + 1 + st.rng.Intn(8)
	op := lease.AdvanceOp{Now: now}
	return dstep{
		name:   "Advance",
		why:    fmt.Sprintf("now -> %d (current %d)", now, st.now),
		applyR: func(s *lease.Service) error { return s.Advance(op) },
		applyN: func(m *naivemodel.Model) error { return m.Advance(op) },
	}
}
