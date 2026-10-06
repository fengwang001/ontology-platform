package pharmacy

import (
	"errors"
	"testing"
)

func (s *System) tickForTest(now int) {
	s.processThrough(now)
	s.now = now
}

func mustRegister(t *testing.T, s *System, id string, pack int, split bool) {
	t.Helper()
	if err := s.RegisterDrug(id, DrugConfig{PackSize: pack, Splittable: split}); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

func TestReservationWindowBoundary(t *testing.T) {
	s := NewSystem(Options{ReservationWindow: 10})
	mustRegister(t, s, "d", 1, true)
	if err := s.ReceiveDrug("d", 0, 1); err != nil {
		t.Fatal(err)
	}
	input := PrescriptionInput{PatientID: "p", IssuedAt: 0, Lines: []LineInput{{DrugID: "d", Quantity: 1}}}
	if err := s.AcceptPrescription("rx", input, 0); err != nil {
		t.Fatal(err)
	}
	waiting := PrescriptionInput{PatientID: "q", IssuedAt: 0, Lines: []LineInput{{DrugID: "d", Quantity: 1}}}
	if err := s.AcceptPrescription("wait", waiting, 0); err != nil {
		t.Fatal(err)
	}
	report, err := s.Prescription("rx", 10)
	if err != nil || report.Lines[0].Reserved != 1 {
		t.Fatalf("at R: report=%+v err=%v", report, err)
	}
	report, err = s.Prescription("rx", 11)
	if err != nil || report.Lines[0].Reserved != 0 || report.Lines[0].Owed != 1 {
		t.Fatalf("after R report=%+v err=%v", report, err)
	}
	active := s.activeByLine[s.prescriptions["rx"].lines[0]]
	if len(active) != 0 {
		t.Fatalf("old reservation remained: %+v", active)
	}
}

func TestPrescriptionValidityBoundary(t *testing.T) {
	s := NewSystem(Options{ReservationWindow: 10000})
	mustRegister(t, s, "d", 1, true)
	input := PrescriptionInput{PatientID: "p", IssuedAt: 0, Lines: []LineInput{{DrugID: "d", Quantity: 1}}}
	if err := s.AcceptPrescription("rx", input, 4320); err != nil {
		t.Fatalf("exactly valid: %v", err)
	}
	if err := s.AcceptPrescription("late", input, 4321); !errors.Is(err, ErrPrescriptionExpired) {
		t.Fatalf("expired error=%v", err)
	}
}

func TestNonSplittableRoundingAndDebt(t *testing.T) {
	s := NewSystem(Options{ReservationWindow: 10000})
	mustRegister(t, s, "d", 10, false)
	if err := s.ReceiveDrug("d", 0, 12); err != nil {
		t.Fatal(err)
	}
	input := PrescriptionInput{
		PatientID: "p",
		IssuedAt:  0,
		Lines:     []LineInput{{DrugID: "d", Quantity: 5}},
	}
	if err := s.AcceptPrescription("up", input, 0); err != nil {
		t.Fatal(err)
	}
	report, _ := s.Prescription("up", 0)
	if report.Lines[0].Reserved != 10 || report.Lines[0].Owed != 0 {
		t.Fatalf("round up report=%+v", report.Lines[0])
	}

	down := PrescriptionInput{PatientID: "q", IssuedAt: 1, Lines: []LineInput{{DrugID: "d", Quantity: 5}}}
	if err := s.AcceptPrescription("down", down, 1); err != nil {
		t.Fatal(err)
	}
	report, _ = s.Prescription("down", 1)
	if report.Lines[0].Reserved != 0 || report.Lines[0].Owed != 5 {
		t.Fatalf("round down report=%+v", report.Lines[0])
	}
}

func TestAllAtOnceRejectionHasNoSideEffects(t *testing.T) {
	s := NewSystem(Options{ReservationWindow: 10000})
	mustRegister(t, s, "a", 1, true)
	mustRegister(t, s, "b", 1, true)
	if err := s.ReceiveDrug("a", 0, 1); err != nil {
		t.Fatal("setup")
	}
	input := PrescriptionInput{
		PatientID: "p",
		IssuedAt:  0,
		AllAtOnce: true,
		Lines: []LineInput{
			{DrugID: "a", Quantity: 1},
			{DrugID: "b", Quantity: 1},
		},
	}
	if err := s.AcceptPrescription("rx", input, 0); !errors.Is(err, ErrOutOfStock) {
		t.Fatalf("error=%v", err)
	}
	if _, err := s.Prescription("rx", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("prescription existed: %v", err)
	}
	drugReport, _ := s.Drug("a", 0)
	if drugReport.OnHand != 1 || drugReport.ActiveReservation != 0 || drugReport.OutstandingDebt != 0 {
		t.Fatalf("side effects: %+v", drugReport)
	}
}
