package cedu

import "testing"

func testCfg() Config {
	return Config{
		CycleLength: 10, TotalRequired: 30, RequiredMin: 10,
		RequiredCap: 20, ElectiveCap: 20, GraceDays: 3,
		CorrectDays: 5, CarryoverCap: 8,
	}
}

func newTestSvc(t *testing.T) *Service {
	t.Helper()
	s, err := NewService(testCfg())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func reg(t *testing.T, s *Service, in CreditInput) string {
	t.Helper()
	id, err := s.RegisterCredit(in)
	if err != nil {
		t.Fatalf("RegisterCredit %+v: %v", in, err)
	}
	return id
}

func mustErr(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %s, got nil", code)
	}
	e, ok := err.(*Error)
	if !ok || e.Code != code {
		t.Fatalf("want error %s, got %v", code, err)
	}
}

func (st HolderStatus) met() bool { return st.CurrentCycle.Met }

func TestCycleBoundaryLastDayAndFirstDay(t *testing.T) {
	s := newTestSvc(t)
	if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, CreditInput{HolderID: "h", Category: Required, Credits: 10, EarnedOn: 9, Org: "o", Now: 9})
	reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: 20, EarnedOn: 9, Org: "o2", Now: 9})
	st, err := s.Status("h", 9)
	if err != nil {
		t.Fatal(err)
	}
	if st.CurrentCycle.Index != 0 || !st.met() {
		t.Fatalf("cycle0 at day9: %+v", st)
	}
	st, err = s.Status("h", 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.CurrentCycle.Index != 1 || st.CurrentCycle.TotalIn != 0 {
		t.Fatalf("cycle1 at day10 should be fresh: %+v", st.CurrentCycle)
	}
}

func TestCapsAndOnlineJointConstraint(t *testing.T) {
	r := countCredit(rawCredit{requiredRaw: 50, electiveRaw: 50, onlineRaw: 999}, testCfg())
	if r.requiredIn != 20 || r.electiveIn != 20 || r.onlineIn != 40 || r.totalIn != 80 {
		t.Fatalf("counted = %+v, want 20/20/40/80", r)
	}
	r = countCredit(rawCredit{requiredRaw: 5, electiveRaw: 5, onlineRaw: 12}, testCfg())
	if r.requiredIn != 5 || r.electiveIn != 5 || r.onlineIn != 10 || r.totalIn != 20 {
		t.Fatalf("counted = %+v, want 5/5/10/20 (online capped by joint bound)", r)
	}
}

func TestRequiredShortfallButTotalEnough(t *testing.T) {
	s := newTestSvc(t)
	if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: 20, EarnedOn: 1, Org: "o1", Now: 1})
	reg(t, s, CreditInput{HolderID: "h", Category: Online, Credits: 20, EarnedOn: 1, Org: "o2", Now: 1})
	st, _ := s.Status("h", 1)
	if st.CurrentCycle.TotalIn != 40 || st.met() {
		t.Fatalf("should fail on required min: %+v", st.CurrentCycle)
	}
	_, err := s.Status("h", 13)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RegisterCredit(CreditInput{HolderID: "h", Category: Required, Credits: 10, EarnedOn: 13, Org: "o3", Now: 13})
	mustErr(t, err, ErrCertificateExpired)
}

func TestGraceLastDayAndOneDayLate(t *testing.T) {
	mk := func() *Service {
		s := newTestSvc(t)
		if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
			t.Fatal(err)
		}
		reg(t, s, CreditInput{HolderID: "h", Category: Required, Credits: 10, EarnedOn: 0, Org: "o", Now: 0})
		reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: 10, EarnedOn: 0, Org: "o2", Now: 0})
		return s
	}
	s := mk()
	reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: 10, EarnedOn: 12, Org: "o3", Now: 12})
	st, _ := s.Status("h", 12)
	if st.CurrentCycle.Index != 0 || !st.met() || st.CurrentCycle.CarryOut != 0 {
		t.Fatalf("grace last day should meet with no carry: %+v", st.CurrentCycle)
	}

	s2 := newTestSvc(t)
	if err := s2.RegisterHolder(RegisterInput{HolderID: "g", IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	reg(t, s2, CreditInput{HolderID: "g", Category: Required, Credits: 10, EarnedOn: 0, Org: "o", Now: 0})
	reg(t, s2, CreditInput{HolderID: "g", Category: Elective, Credits: 10, EarnedOn: 0, Org: "o2", Now: 0})
	_, err := s2.RegisterCredit(CreditInput{HolderID: "g", Category: Elective, Credits: 10, EarnedOn: 12, Org: "o3", Now: 13})
	mustErr(t, err, ErrCertificateExpired)
}

func TestCorrectionWindowExactBoundary(t *testing.T) {
	s := newTestSvc(t)
	if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	id := reg(t, s, CreditInput{HolderID: "h", Category: Required, Credits: 5, EarnedOn: 0, Org: "o", Now: 0})
	if err := s.CorrectCredit("h", id, "o", 8, 5); err != nil {
		t.Fatalf("correction at exact deadline: %v", err)
	}
	mustErr(t, s.CorrectCredit("h", id, "o", 9, 6), ErrCorrectionExpired)
	mustErr(t, s.RevokeCredit("h", id, "o", 6), ErrCorrectionExpired)
	mustErr(t, s.CorrectCredit("h", id, "other", 9, 5), ErrNotFound)
}
