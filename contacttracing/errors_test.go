package contacttracing

import "testing"

func TestErrorPriority(t *testing.T) {
	s := New()
	mustOK(t, s.RecordStay("p", "R", 100, 0, 50))

	// 参数非法优先于一切：空标识 + 时钟回退。
	if err := s.RecordStay("", "R", 50, 0, 10); errKind(err) != ErrInvalidParameter {
		t.Fatalf("got %v", err)
	}
	// 时钟回退优先于对象不存在。
	if err := s.RecordIsolation("nope", 50, 0); errKind(err) != ErrClockRollback {
		t.Fatalf("got %v", err)
	}
	// 对象不存在优先于状态不符。
	if err := s.RecordIsolation("nope", 200, 0); errKind(err) != ErrNotFound {
		t.Fatalf("got %v", err)
	}
	// 出住：对象不存在（无在住记录）优先于其他。
	if err := s.RecordDischarge("ghost", "R", 200, 60); errKind(err) != ErrNotFound {
		t.Fatalf("got %v", err)
	}
	// 状态查询对象不存在。
	if _, err := s.PatientStatusAt("ghost", 200); errKind(err) != ErrNotFound {
		t.Fatalf("got %v", err)
	}
	// 清单对象不存在。
	if _, err := s.ListContacts("nope", 200); errKind(err) != ErrNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestClockRollbackRejected(t *testing.T) {
	s := New()
	mustOK(t, s.RecordStay("p", "R", 100, 0, 50))
	// 被拒绝的操作不改变状态与时钟：随后 now=100 仍接受。
	err := s.RecordStay("q", "R", 90, 0, 10)
	if errKind(err) != ErrClockRollback {
		t.Fatalf("got %v", err)
	}
	mustOK(t, s.RecordStay("q", "R", 100, 0, 10))
}

func TestStayConflictAndTouch(t *testing.T) {
	s := New()
	// 左闭右开：端点相接（out==in）不冲突。
	mustOK(t, s.RecordStay("p", "R", 100, 0, 50))
	mustOK(t, s.RecordStay("p", "R", 100, 50, 100))
	// 正重叠冲突。
	if err := s.RecordStay("p", "R", 130, 90, 120); errKind(err) != ErrStayConflict {
		t.Fatalf("got %v", err)
	}

	// 入住后与既有区间冲突。
	s2 := New()
	mustOK(t, s2.RecordStay("a", "R", 100, 0, 50))
	if err := s2.RecordAdmission("a", "R", 100, 40); errKind(err) != ErrStayConflict {
		t.Fatalf("admission overlap got %v", err)
	}
	// 同一患者只能有一条在住记录。
	mustOK(t, s2.RecordAdmission("a", "R", 200, 100))
	if err := s2.RecordAdmission("a", "R2", 300, 250); errKind(err) != ErrInvalidState {
		t.Fatalf("double open got %v", err)
	}
	// 出住须晚于入住。
	if err := s2.RecordDischarge("a", "R", 300, 100); errKind(err) != ErrInvalidParameter {
		t.Fatalf("discharge eq got %v", err)
	}
	mustOK(t, s2.RecordDischarge("a", "R", 300, 180))
}

func TestCaseStateMachine(t *testing.T) {
	s := New()
	cid, err := s.RegisterCase("p", 100, 50)
	mustOK(t, err)
	// 重复病例登记。
	if _, err := s.RegisterCase("p", 100, 50); errKind(err) != ErrInvalidState {
		t.Fatalf("dup case got %v", err)
	}
	// 改正不存在的病例。
	if err := s.CorrectOnset("nope", 100, 10); errKind(err) != ErrNotFound {
		t.Fatalf("got %v", err)
	}
	// 撤销后改正/隔离均报状态不符。
	mustOK(t, s.RevokeCase(cid, 100))
	if err := s.CorrectOnset(cid, 100, 10); errKind(err) != ErrInvalidState {
		t.Fatalf("correct revoked got %v", err)
	}
	if err := s.RecordIsolation(cid, 100, 60); errKind(err) != ErrInvalidState {
		t.Fatalf("isolate revoked got %v", err)
	}
}

func TestInvalidParameters(t *testing.T) {
	s := New()
	cases := []error{
		s.RecordStay("p", "R", 100, 80, 50), // in>=out
		s.RecordStay("p", "R", 100, 0, 101), // out>now
		s.RecordAdmission("p", "R", 100, 101),
		s.RecordStay("p", "R", -1, 0, 0),                                   // now 越界
		func() error { _, e := s.RegisterCase("p", 100, 101); return e }(), // onset>now
	}
	for i, err := range cases {
		if errKind(err) != ErrInvalidParameter {
			t.Fatalf("case %d got %v", i, err)
		}
	}
}
