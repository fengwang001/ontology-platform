package leasechain

import (
	"errors"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := New(Config{P: 100, D: 3, G: 5, PayDay: 10})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return s
}

func masterChain(t *testing.T, s *Service) (int64, int64) {
	t.Helper()
	// 主租约：[5,100)，月租 1000；首个付款日 10。
	m, err := s.CreateMaster(0, "LL", "T1", 5, 100, 1000)
	if err != nil {
		t.Fatalf("create master: %v", err)
	}
	return m.ID, 0
}

func TestSubleaseEndEqualsParentEnd(t *testing.T) {
	s := newTestService(t)
	mid, _ := masterChain(t, s)
	if err := s.GrantGeneral(0, "LL", "T2"); err != nil {
		t.Fatal(err)
	}
	// 终止日恰等于上级终止日：允许。
	sub, err := s.Sublease(1, mid, "T2", 10, 100, 1000)
	if err != nil {
		t.Fatalf("end == parent end must be allowed: %v", err)
	}
	if sub.End != 100 {
		t.Fatalf("unexpected end %d", sub.End)
	}
	// 超出一天：期限越界。
	if _, err := s.GrantOneTime(2, "LL", "T3", mid, 10, 101, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sublease(2, mid, "T3", 10, 101, 1000); !errors.Is(err, ErrTermOutOfRange) {
		t.Fatalf("want ErrTermOutOfRange, got %v", err)
	}
}

func TestRentLimitExactAndOverOne(t *testing.T) {
	s := newTestService(t)
	mid, _ := masterChain(t, s)
	// P=100：恰等 1000 允许；1001 拒绝。
	if _, err := s.GrantOneTime(0, "LL", "E", mid, 10, 50, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sublease(1, mid, "E", 10, 50, 1000); err != nil {
		t.Fatalf("rent at exact cap: %v", err)
	}
	m2, err := s.CreateMaster(5, "LL", "S2", 0, 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantOneTime(5, "LL", "E", m2.ID, 10, 50, 1001); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sublease(6, m2.ID, "E", 10, 50, 1001); !errors.Is(err, ErrRentTooHigh) {
		t.Fatalf("rent one over cap: got %v", err)
	}
}

func TestOneTimeConsentUsedOnce(t *testing.T) {
	s := newTestService(t)
	mid, _ := masterChain(t, s)
	if _, err := s.GrantOneTime(0, "LL", "E", mid, 10, 50, 900); err != nil {
		t.Fatal(err)
	}
	sub, err := s.Sublease(1, mid, "E", 10, 50, 900)
	if err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := s.Terminate(2, sub.ID, "E"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sublease(3, mid, "E", 10, 50, 900); !errors.Is(err, ErrNoConsent) {
		t.Fatalf("reuse one-time consent: got %v", err)
	}
}

func TestGeneralConsentRevokeTimeline(t *testing.T) {
	s := newTestService(t)
	m1, _ := s.CreateMaster(0, "LL", "G1", 0, 200, 1000)
	m2, _ := s.CreateMaster(0, "LL", "G2", 0, 200, 1000)
	if err := s.GrantGeneral(0, "LL", "T"); err != nil {
		t.Fatal(err)
	}
	before, err := s.Sublease(1, m1.ID, "T", 10, 80, 800)
	if err != nil {
		t.Fatalf("before revoke: %v", err)
	}
	if err := s.RevokeGeneral(2, "LL", "T"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sublease(3, m2.ID, "T", 10, 80, 800); !errors.Is(err, ErrNoConsent) {
		t.Fatalf("after revoke: got %v", err)
	}
	got, err := s.GetLease(before.ID)
	if err != nil || got.Terminated {
		t.Fatalf("existing sublease affected by revoke: %v", got)
	}
}

func TestDepthExactAndOver(t *testing.T) {
	s := newTestService(t)
	mid, _ := masterChain(t, s)
	parent := mid
	for i := 1; i <= 3; i++ {
		tenant := "D" + string(rune('A'+i-1))
		if err := s.GrantGeneral(i, "LL", tenant); err != nil {
			t.Fatal(err)
		}
		l, err := s.Sublease(i, parent, tenant, i*10, 90-i, int64(900-i))
		if err != nil {
			t.Fatalf("depth %d must be allowed: %v", i, err)
		}
		parent = l.ID
	}
	if err := s.GrantGeneral(4, "LL", "D4"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sublease(5, parent, "D4", 41, 60, 500); !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("depth D+1: got %v", err)
	}
}

func TestOverdueExactlyGDays(t *testing.T) {
	s := newTestService(t)
	m, _ := s.CreateMaster(0, "LL", "T", 5, 100, 1000)
	due := installments(5, 100, 10, 1000)[0].due
	if err := s.Advance(due + 4); err != nil {
		t.Fatal(err)
	}
	if len(s.allArrears()) != 0 {
		t.Fatalf("arrear before G days: %+v", s.allArrears())
	}
	if err := s.Advance(due + 5); err != nil {
		t.Fatal(err)
	}
	ars := s.allArrears()
	if len(ars) != 1 || ars[0].LeaseID != m.ID || ars[0].Due != due || ars[0].Amount != 1000 {
		t.Fatalf("arrear at exactly G: %+v", ars)
	}
}

func TestMultiLevelPaymentAndRecourse(t *testing.T) {
	s := newTestService(t)
	m, _ := s.CreateMaster(0, "LL", "A", 5, 200, 1000)
	mustGrantGeneral(t, s, "B", "C")
	b, err := s.Sublease(0, m.ID, "B", 5, 200, 1000)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Sublease(0, b.ID, "C", 5, 200, 1000)
	if err != nil {
		t.Fatal(err)
	}
	due := installments(5, 200, 10, 1000)[0].due
	if err := s.Advance(due + 5); err != nil {
		t.Fatal(err)
	}
	var cArrear int64
	for _, a := range s.allArrears() {
		if a.LeaseID == c.ID {
			cArrear = a.ID
		}
	}
	if cArrear == 0 {
		t.Fatal("C arrear missing")
	}
	r1, err := s.PayArrear(due+6, cArrear, b.ID, 400)
	if err != nil {
		t.Fatalf("B pays: %v", err)
	}
	if r1.PayerID != b.ID || r1.Amount != 400 {
		t.Fatalf("recourse1 %+v", r1)
	}
	a, _ := s.GetArrear(cArrear)
	if a.Paid != 400 {
		t.Fatalf("paid=%d", a.Paid)
	}
	if _, err := s.PayArrear(due+7, cArrear, m.ID, 601); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("overpay: got %v", err)
	}
	r2, err := s.PayArrear(due+8, cArrear, m.ID, 600)
	if err != nil {
		t.Fatalf("A pays: %v", err)
	}
	if r2.Amount != 600 || r2.PayerID != m.ID {
		t.Fatalf("recourse2 %+v", r2)
	}
	if _, err := s.PayArrear(due+9, cArrear, m.ID, 1); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("double settle: got %v", err)
	}
	var rec int64
	for _, r := range s.RecourseParties(cArrear) {
		rec += r.Amount
	}
	if rec != 1000 {
		t.Fatalf("recourse total %d", rec)
	}
}

func mustGrantGeneral(t *testing.T, s *Service, tenants ...string) {
	t.Helper()
	for _, tn := range tenants {
		if err := s.GrantGeneral(0, "LL", tn); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCascadeAndRecognitionPromotion(t *testing.T) {
	s := newTestService(t)
	m, _ := s.CreateMaster(0, "LL", "A", 0, 200, 1000)
	mustGrantGeneral(t, s, "B", "C")
	b, _ := s.Sublease(0, m.ID, "B", 10, 190, 900)
	c, _ := s.Sublease(0, b.ID, "C", 20, 180, 800)
	if err := s.Recognize(1, b.ID, "LL"); err != nil {
		t.Fatal(err)
	}
	if err := s.Terminate(100, m.ID, "LL"); err != nil {
		t.Fatal(err)
	}
	mg, _ := s.GetLease(m.ID)
	bg, _ := s.GetLease(b.ID)
	cg, _ := s.GetLease(c.ID)
	if !mg.Terminated {
		t.Fatal("master should terminate")
	}
	if bg.Terminated || bg.ParentID != 0 || bg.Start != 10 || bg.End != 190 || bg.Rent != 900 {
		t.Fatalf("B not promoted correctly: %+v", bg)
	}
	if cg.Terminated || cg.ParentID != b.ID {
		t.Fatalf("C should follow B: %+v", cg)
	}
	if err := s.Recognize(101, b.ID, "LL"); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("re-recognize promoted direct lease: got %v", err)
	}
}

func TestCascadeWithoutRecognition(t *testing.T) {
	s := newTestService(t)
	m, _ := s.CreateMaster(0, "LL", "A", 0, 200, 1000)
	mustGrantGeneral(t, s, "B", "C")
	b, _ := s.Sublease(0, m.ID, "B", 10, 190, 900)
	c, _ := s.Sublease(0, b.ID, "C", 20, 180, 800)
	if err := s.Terminate(100, m.ID, "A"); err != nil {
		t.Fatal(err)
	}
	bg, _ := s.GetLease(b.ID)
	cg, _ := s.GetLease(c.ID)
	if !bg.Terminated || bg.End != 100 {
		t.Fatalf("B should cascade-terminate at 100: %+v", bg)
	}
	if !cg.Terminated || cg.End != 100 {
		t.Fatalf("C should cascade-terminate at 100: %+v", cg)
	}
}

func TestExitRejectedAndAllowed(t *testing.T) {
	s := newTestService(t)
	m, _ := s.CreateMaster(0, "LL", "A", 0, 200, 1000)
	mustGrantGeneral(t, s, "B", "C")
	b, _ := s.Sublease(0, m.ID, "B", 10, 190, 900)
	if _, err := s.Sublease(0, b.ID, "C", 20, 180, 800); err != nil {
		t.Fatal(err)
	}
	if err := s.Exit(2, b.ID, "B"); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("exit with unrecognized child: got %v", err)
	}
	cid := activeChildOf(s, b.ID)
	if err := s.Exit(3, cid, "C"); err != nil {
		t.Fatalf("leaf exit: %v", err)
	}
	if err := s.Exit(4, b.ID, "B"); err != nil {
		t.Fatalf("B exit after child gone: %v", err)
	}
}

func activeChildOf(s *Service, parent int64) int64 {
	for _, a := range s.allLeases() {
		if a.ParentID == parent && !a.Terminated {
			return a.ID
		}
	}
	return 0
}

func TestErrorOrderAndNoTrace(t *testing.T) {
	s := newTestService(t)
	m, _ := s.CreateMaster(10, "LL", "A", 0, 200, 1000)
	leaseCount := len(s.allLeases())
	// 参数非法先于时钟回退。
	if _, err := s.Sublease(5, m.ID, "B", 80, 50, 10); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("param order: %v", err)
	}
	// 时钟回退先于其他业务错误。
	if _, err := s.Sublease(9, m.ID, "B", 10, 50, 10); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("clock order: %v", err)
	}
	// 租约不存在先于未同意。
	if _, err := s.Sublease(11, 9999, "B", 10, 50, 10); !errors.Is(err, ErrLeaseUnavailable) {
		t.Fatalf("missing lease order: %v", err)
	}
	// 对自身转租为状态错误。
	if _, err := s.Sublease(11, m.ID, "A", 10, 50, 10); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("self sublease: %v", err)
	}
	// 无同意先于期限、租金、深度错误。
	if _, err := s.Sublease(11, m.ID, "B", 500, 9999, 999999); !errors.Is(err, ErrNoConsent) {
		t.Fatalf("consent order: %v", err)
	}
	if err := s.GrantGeneral(11, "LL", "B"); err != nil {
		t.Fatal(err)
	}
	// 有同意后期限越界先于租金。
	if _, err := s.Sublease(12, m.ID, "B", 500, 9999, 999999); !errors.Is(err, ErrTermOutOfRange) {
		t.Fatalf("term order: %v", err)
	}
	// 期限合法时租金报错。
	if _, err := s.Sublease(12, m.ID, "B", 10, 100, 1001); !errors.Is(err, ErrRentTooHigh) {
		t.Fatalf("rent order: %v", err)
	}
	// 租金合法时已有有效下级之外：先建立一个下级，再试第二份。
	if _, err := s.Sublease(12, m.ID, "B", 10, 100, 1000); err != nil {
		t.Fatalf("first child: %v", err)
	}
	if err := s.GrantGeneral(12, "LL", "B2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sublease(13, m.ID, "B2", 10, 100, 1000); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("existing child: %v", err)
	}
	// 被拒操作不留痕：只有 1 主 + 1 次两份租约，lastNow 仍为 13，无额外欠费。
	if len(s.allLeases()) != leaseCount+1 {
		t.Fatalf("rejected ops left traces: %d leases", len(s.allLeases()))
	}
}

func TestClockNeverMovesBackwards(t *testing.T) {
	s := newTestService(t)
	if err := s.Advance(50); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMaster(49, "LL", "A", 0, 100, 1000); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback: %v", err)
	}
}

func TestExitKeepsArrearsAndRecourse(t *testing.T) {
	s := newTestService(t)
	m, _ := s.CreateMaster(0, "LL", "A", 5, 200, 1000)
	mustGrantGeneral(t, s, "B")
	b, _ := s.Sublease(0, m.ID, "B", 5, 200, 1000)
	due := installments(5, 200, 10, 1000)[0].due
	if err := s.Advance(due + 5); err != nil {
		t.Fatal(err)
	}
	var bid int64
	for _, a := range s.allArrears() {
		if a.LeaseID == b.ID {
			bid = a.ID
		}
	}
	if _, err := s.PayArrear(due+6, bid, m.ID, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.Exit(due+7, b.ID, "B"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetArrear(bid)
	if a.Paid != 1000 || len(s.RecourseParties(bid)) != 1 {
		t.Fatalf("liability/recourse lost after exit: %+v", a)
	}
}
