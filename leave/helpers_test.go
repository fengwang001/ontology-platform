package leave_test

import (
	"testing"

	"ontology/leave"
)

// baseConfig：工龄档边界 [1,10]，额度 [5,10,20]，结转上限 4，结转截止日 doy 89。
func baseConfig() leave.Config {
	return leave.Config{
		TenureBounds:  []int{1, 10},
		AnnualQuotas:  []int{5, 10, 20},
		CarryCap:      4,
		CarryDeadline: 89,
	}
}

func newService(t *testing.T, cfg leave.Config) *leave.Service {
	t.Helper()
	s, err := leave.NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func mustRegister(t *testing.T, s *leave.Service, now int, id string, hireDay int) {
	t.Helper()
	if err := s.Register(now, id, hireDay); err != nil {
		t.Fatalf("Register(%d,%q,%d): %v", now, id, hireDay, err)
	}
}

func mustRequest(t *testing.T, s *leave.Service, now int, id string, from, to int) (int64, []leave.Charge) {
	t.Helper()
	lid, charges, err := s.RequestLeave(now, id, from, to)
	if err != nil {
		t.Fatalf("RequestLeave(%d,%q,[%d,%d]): %v", now, id, from, to, err)
	}
	return lid, charges
}

func mustApprove(t *testing.T, s *leave.Service, now int, id string, lid int64) {
	t.Helper()
	if err := s.Approve(now, id, lid); err != nil {
		t.Fatalf("Approve(%d,%q,%d): %v", now, id, lid, err)
	}
}

func mustBalance(t *testing.T, s *leave.Service, now int, id string) leave.Balance {
	t.Helper()
	b, err := s.Balance(now, id)
	if err != nil {
		t.Fatalf("Balance(%d,%q): %v", now, id, err)
	}
	return b
}

func expectErr(t *testing.T, err error, cat leave.Category, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error %s, got nil", what, cat)
	}
	got, ok := leave.CategoryOf(err)
	if !ok {
		t.Fatalf("%s: expected *leave.Error, got %T (%v)", what, err, err)
	}
	if got != cat {
		t.Fatalf("%s: expected category %s, got %s (%v)", what, cat, got, err)
	}
}

// expectCharges 校验逐日扣减明细（判定依据）。
func expectCharges(t *testing.T, got []leave.Charge, want []leave.Charge) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("charges length = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("charge[%d] = %+v, want %+v\ngot:  %v\nwant: %v", i, got[i], want[i], got, want)
		}
	}
}

func ch(day, year int, src leave.Source) leave.Charge {
	return leave.Charge{Day: day, Year: year, Source: src}
}
