package pharmacy

import (
	"errors"
	"testing"
)

func acceptSimple(t *testing.T, s *System, id, drugID string, qty, issued, now int) {
	t.Helper()
	input := PrescriptionInput{
		PatientID: "patient-" + id,
		IssuedAt:  issued,
		Lines:     []LineInput{{DrugID: drugID, Quantity: qty}},
	}
	if err := s.AcceptPrescription(id, input, now); err != nil {
		t.Fatalf("accept %s: %v", id, err)
	}
}

func TestArrivalFairness(t *testing.T) {
	s := NewSystem(Options{ReservationWindow: 10000})
	mustRegister(t, s, "d", 1, true)
	acceptSimple(t, s, "first", "d", 2, 0, 0)
	acceptSimple(t, s, "second", "d", 2, 0, 0)
	if err := s.ReceiveDrug("d", 1, 3); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Prescription("first", 1)
	second, _ := s.Prescription("second", 1)
	if first.Lines[0].Reserved != 2 || first.Lines[0].Owed != 0 {
		t.Fatalf("first=%+v", first.Lines[0])
	}
	if second.Lines[0].Reserved != 1 || second.Lines[0].Owed != 1 {
		t.Fatalf("second=%+v", second.Lines[0])
	}
}

func TestCascadeExpiryReallocation(t *testing.T) {
	s := NewSystem(Options{ReservationWindow: 5})
	mustRegister(t, s, "d", 1, true)
	if err := s.ReceiveDrug("d", 0, 2); err != nil {
		t.Fatal(err)
	}
	acceptSimple(t, s, "holder", "d", 2, 0, 0)
	acceptSimple(t, s, "waiting", "d", 1, 0, 0)
	if _, err := s.Prescription("waiting", 6); err != nil {
		t.Fatal(err)
	}
	waiting, _ := s.Prescription("waiting", 6)
	holder, _ := s.Prescription("holder", 6)
	if waiting.Lines[0].Reserved != 1 || holder.Lines[0].Reserved != 1 || holder.Lines[0].Owed != 1 {
		t.Fatalf("waiting=%+v holder=%+v", waiting.Lines[0], holder.Lines[0])
	}
	if _, err := s.Prescription("waiting", 12); err != nil {
		t.Fatal(err)
	}
	waiting, _ = s.Prescription("waiting", 12)
	holder, _ = s.Prescription("holder", 12)
	if holder.Lines[0].Reserved != 2 || waiting.Lines[0].Reserved != 0 || waiting.Lines[0].Owed != 1 {
		t.Fatalf("holder=%+v waiting=%+v", holder.Lines[0], waiting.Lines[0])
	}
}

func TestExpiryReleasesAndAllocates(t *testing.T) {
	s := NewSystem(Options{ReservationWindow: 10000})
	mustRegister(t, s, "d", 1, true)
	if err := s.ReceiveDrug("d", 0, 1); err != nil {
		t.Fatal(err)
	}
	acceptSimple(t, s, "old", "d", 1, 0, 0)
	acceptSimple(t, s, "young", "d", 1, 100, 100)
	young, _ := s.Prescription("young", 4321)
	old, _ := s.Prescription("old", 4321)
	if old.Status != StatusExpired || young.Status != StatusActive || young.Lines[0].Reserved != 1 {
		t.Fatalf("old=%+v young=%+v", old, young)
	}
}

func TestCancelReleasesAndAllocates(t *testing.T) {
	s := NewSystem(Options{ReservationWindow: 10000})
	mustRegister(t, s, "d", 1, true)
	if err := s.ReceiveDrug("d", 0, 1); err != nil {
		t.Fatal(err)
	}
	acceptSimple(t, s, "first", "d", 1, 0, 0)
	acceptSimple(t, s, "second", "d", 1, 0, 0)
	if err := s.CancelPrescription("first", 1); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Prescription("first", 1)
	second, _ := s.Prescription("second", 1)
	if first.Status != StatusCancelled || second.Lines[0].Reserved != 1 || second.Lines[0].Owed != 0 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if err := s.CancelPrescription("first", 2); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("repeat cancel=%v", err)
	}
}

func TestDispenseKeepsFutureDebt(t *testing.T) {
	s := NewSystem(Options{ReservationWindow: 10000})
	mustRegister(t, s, "d", 1, true)
	if err := s.ReceiveDrug("d", 0, 2); err != nil {
		t.Fatal(err)
	}
	acceptSimple(t, s, "rx", "d", 5, 0, 0)
	report, err := s.Dispense("rx", 1)
	if err != nil {
		t.Fatal(err)
	}
	if report.Lines[0].Dispensed != 2 || report.Lines[0].Reserved != 0 || report.Lines[0].Owed != 3 || report.Status != StatusActive {
		t.Fatalf("report=%+v", report)
	}
	if err := s.ReceiveDrug("d", 2, 3); err != nil {
		t.Fatal(err)
	}
	report, err = s.Dispense("rx", 2)
	if err != nil || report.Lines[0].Dispensed != 5 || report.Lines[0].Owed != 0 || report.Status != StatusCompleted {
		t.Fatalf("final=%+v err=%v", report, err)
	}
}

func TestErrorPriority(t *testing.T) {
	s := NewSystem(Options{ReservationWindow: 10000})
	if err := s.RegisterDrug("", DrugConfig{PackSize: 1, Splittable: true}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid=%v", err)
	}
	mustRegister(t, s, "missing", 1, true)
	mustRegister(t, s, "empty", 1, true)
	if err := s.ReceiveDrug("missing", 5, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.ReceiveDrug("missing", 4, 1); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback=%v", err)
	}
	if _, err := s.Dispense("unknown", 6); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found=%v", err)
	}
	acceptSimple(t, s, "rx", "empty", 1, 0, 6)
	if _, err := s.Dispense("rx", 10); !errors.Is(err, ErrNoActiveReservation) {
		t.Fatalf("no reservation=%v", err)
	}
	if _, err := s.Dispense("rx", 4327); !errors.Is(err, ErrPrescriptionExpired) {
		t.Fatalf("expired=%v", err)
	}
}
