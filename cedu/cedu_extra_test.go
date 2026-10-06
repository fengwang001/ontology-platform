package cedu

import "testing"

func TestCorrectionEquivalentToDirectValue(t *testing.T) {
	build := func(correct bool) []CycleStatus {
		s := newTestSvc(t)
		if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
			t.Fatal(err)
		}
		v := 25
		if correct {
			v = 10
		}
		id := reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: v, EarnedOn: 0, Org: "o", Now: 0})
		reg(t, s, CreditInput{HolderID: "h", Category: Required, Credits: 10, EarnedOn: 0, Org: "o2", Now: 0})
		if correct {
			if err := s.CorrectCredit("h", id, "o", 25, 4); err != nil {
				t.Fatal(err)
			}
		}
		var snaps []CycleStatus
		for _, day := range []int{4, 9, 10, 12, 13, 20} {
			st, err := s.Status("h", day)
			if err != nil {
				t.Fatal(err)
			}
			snaps = append(snaps, st.CurrentCycle)
		}
		return snaps
	}
	a, b := build(true), build(false)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("snapshot %d differs:\ncorrected=%+v\ndirect   =%+v", i, a[i], b[i])
		}
	}
}

func TestCarryoverCompositionAndCap(t *testing.T) {
	s := newTestSvc(t)
	if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, CreditInput{HolderID: "h", Category: Required, Credits: 10, EarnedOn: 0, Org: "r", Now: 0})
	reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: 20, EarnedOn: 0, Org: "e", Now: 0})
	reg(t, s, CreditInput{HolderID: "h", Category: Online, Credits: 20, EarnedOn: 0, Org: "n", Now: 0})
	c0, err := s.Cycle("h", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if c0.Index != 0 || !c0.Met || c0.CarryOut != 8 {
		t.Fatalf("cycle0 carry want 8: %+v", c0)
	}
	st, _ := s.Status("h", 15)
	if st.CurrentCycle.Index != 1 || st.CurrentCycle.CarryIn != 8 ||
		st.CurrentCycle.ElectiveIn != 8 || st.met() {
		t.Fatalf("cycle1 carry-only: %+v", st.CurrentCycle)
	}
	reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: 20, EarnedOn: 11, Org: "e2", Now: 16})
	reg(t, s, CreditInput{HolderID: "h", Category: Required, Credits: 10, EarnedOn: 11, Org: "r2", Now: 16})
	st, _ = s.Status("h", 16)
	if st.CurrentCycle.ElectiveIn != 20 || st.CurrentCycle.TotalIn != 30 || !st.met() {
		t.Fatalf("cycle1 elective capped incl carry: %+v", st.CurrentCycle)
	}
}

func TestRequiredExcessNotCarried(t *testing.T) {
	s := newTestSvc(t)
	if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, CreditInput{HolderID: "h", Category: Required, Credits: 20, EarnedOn: 0, Org: "r", Now: 0})
	reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: 10, EarnedOn: 0, Org: "e", Now: 0})
	c0, err := s.Cycle("h", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if c0.TotalIn != 30 || c0.CarryOut != 0 {
		t.Fatalf("required excess must not carry: %+v", c0)
	}
}

func TestExpireThenReregister(t *testing.T) {
	s := newTestSvc(t)
	if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	reg(t, s, CreditInput{HolderID: "h", Category: Required, Credits: 5, EarnedOn: 0, Org: "o", Now: 0})
	// 被接受的查询前先有一个变更把时钟推进（这里用一次同周期更正）。
	// 宽限末日当天仍允许补修登记，下一日同类登记被拒且不改变时钟。
	reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: 1, EarnedOn: 12, Org: "g", Now: 12})
	// now 回退到 11 → 时钟回退，优先于状态/失效检查。
	mustErr(t, s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 11}), ErrClockRollback)
	// 宽限满日（13）之后：查询已可见失效结论；随后任何变更都被拒。
	st, err := s.Status("h", 14)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Expired {
		t.Fatalf("holder should be expired after grace end: %+v", st)
	}
	if _, err := s.RegisterCredit(CreditInput{HolderID: "h", Category: Online, Credits: 1, EarnedOn: 12, Org: "late", Now: 14}); err != nil {
		mustErr(t, err, ErrCertificateExpired)
	} else {
		t.Fatalf("credit registered after grace end must be rejected")
	}
	if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 100, Now: 100}); err != nil {
		t.Fatalf("re-register after expiry: %v", err)
	}
	st, _ = s.Status("h", 100)
	if !st.Active || st.CurrentCycle.Index != 0 || st.IssueDate != 100 || st.CurrentCycle.TotalIn != 0 {
		t.Fatalf("fresh lifecycle expected: %+v", st)
	}
	// 旧同键记录随生命周期丢弃，可重新登记。
	id, err := s.RegisterCredit(CreditInput{HolderID: "h", Category: Required, Credits: 1, EarnedOn: 100, Org: "o", Now: 100})
	if err != nil {
		t.Fatalf("old records must not be inherited: %v", err)
	}
	if id != "h-R1" {
		t.Fatalf("record sequence should restart, got %s", id)
	}
}

func TestDuplicateRegistration(t *testing.T) {
	s := newTestSvc(t)
	if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	in := CreditInput{HolderID: "h", Category: Required, Credits: 5, EarnedOn: 2, Org: "o", Now: 2}
	reg(t, s, in)
	// 同学分、不同机构 → 非重复。
	reg(t, s, CreditInput{HolderID: "h", Category: Required, Credits: 5, EarnedOn: 2, Org: "o2", Now: 2})
	if _, err := s.RegisterCredit(in); err == nil {
		t.Fatalf("duplicate should be rejected")
	} else {
		mustErr(t, err, ErrDuplicate)
	}
	// 不同类别 → 非重复。
	reg(t, s, CreditInput{HolderID: "h", Category: Elective, Credits: 5, EarnedOn: 2, Org: "o", Now: 2})
	// 撤销后同键可再次登记。
	id := "h-R1"
	if err := s.RevokeCredit("h", id, "o", 3); err != nil {
		t.Fatal(err)
	}
	in.Now = 4
	if _, err := s.RegisterCredit(in); err != nil {
		t.Fatalf("re-register after revoke: %v", err)
	}
}

func TestRejectionPriority(t *testing.T) {
	s := newTestSvc(t)
	if err := s.RegisterHolder(RegisterInput{HolderID: "h", IssueDate: 0, Now: 10}); err != nil {
		t.Fatal(err)
	}
	// 参数非法优先于一切。
	_, err := s.RegisterCredit(CreditInput{HolderID: "missing", Org: "", Credits: 0, EarnedOn: 1, Now: 0})
	mustErr(t, err, ErrInvalidParam)
	// 时钟回退优先于不存在。
	_, err = s.RegisterCredit(CreditInput{HolderID: "missing", Org: "o", Credits: 1, EarnedOn: 1, Now: 5})
	mustErr(t, err, ErrClockRollback)
	// 时钟正常、持证人不存在 → NOT_FOUND。
	_, err = s.RegisterCredit(CreditInput{HolderID: "missing", Org: "o", Credits: 1, EarnedOn: 10, Now: 10})
	mustErr(t, err, ErrNotFound)
	// 失效检查优先于重复。
	if _, err := s.Status("h", 20); err != nil {
		t.Fatal(err)
	}
	in := CreditInput{HolderID: "h", Category: Required, Credits: 1, EarnedOn: 20, Org: "o", Now: 20}
	if _, err := s.RegisterCredit(CreditInput{HolderID: "h", Category: Required, Credits: 1, EarnedOn: 20, Org: "o", Now: 20}); err == nil {
		t.Fatalf("expired holder registration should fail")
	}
	_ = in
	// h 此时已失效（无任何达标学分且越过宽限）；即便同键重复也先报失效。
	_, err = s.RegisterCredit(in)
	mustErr(t, err, ErrCertificateExpired)
}
