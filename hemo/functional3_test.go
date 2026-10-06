package hemo

import "testing"

// Status determination / new infection re-check flow.
func TestChangeInfectionRecheck(t *testing.T) {
	s := NewSystem(testCfg())
	mustChair(t, s, "N1", ZoneNormal, true)
	mustChair(t, s, "I1", ZoneIsolation, false)
	mustPatient(t, s, "U1", InfectionUnknown)
	plan, err := s.AddPlan(0, "U1", weekdaySet(0), 1000, 200, 0, 30000)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.GetTreatment(plan.TreatmentIDs[0])
	if first.ChairID != "N1" {
		t.Fatalf("pending on %s want N1 (observation)", first.ChairID)
	}
	if err := s.ChangeInfection(500, 500, "U1", InfectionHBV); err != nil {
		t.Fatal(err)
	}
	moved, _ := s.GetTreatment(first.ID)
	if moved.ChairID != "I1" {
		t.Fatalf("after HBV determination treatment on %s want I1", moved.ChairID)
	}
	if inf, _ := s.PatientInfection("U1"); inf != InfectionHBV {
		t.Fatalf("infection = %v want HBV", inf)
	}

	s2 := NewSystem(testCfg())
	mustChair(t, s2, "N1", ZoneNormal, true)
	mustChair(t, s2, "I1", ZoneIsolation, false)
	mustPatient(t, s2, "U2", InfectionUnknown)
	p2, err := s2.AddPlan(0, "U2", weekdaySet(0), 1000, 200, 0, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.ChangeInfection(1100, 1100, "U2", InfectionHBV); err != nil {
		t.Fatal(err)
	}
	keep, _ := s2.GetTreatment(p2.TreatmentIDs[0])
	if keep.ChairID != "N1" {
		t.Fatalf("started treatment must remain on N1, got %s", keep.ChairID)
	}
	next, _ := s2.GetTreatment(p2.TreatmentIDs[1])
	if next.ChairID != "I1" {
		t.Fatalf("future treatment should move to I1, got %s", next.ChairID)
	}

	s3 := NewSystem(testCfg())
	mustChair(t, s3, "N1", ZoneNormal, true)
	mustPatient(t, s3, "U3", InfectionUnknown)
	p3, err := s3.AddPlan(0, "U3", weekdaySet(0), 1000, 200, 0, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s3.ChangeInfection(500, 500, "U3", InfectionHBV); err == nil {
		t.Fatal("HBV determination without isolation chair must fail")
	}
	if inf, _ := s3.PatientInfection("U3"); inf != InfectionUnknown {
		t.Fatalf("failed change must restore infection, got %v", inf)
	}
	tv, _ := s3.GetTreatment(p3.TreatmentIDs[0])
	if tv.ChairID != "N1" {
		t.Fatalf("failed change must restore chair, got %s", tv.ChairID)
	}

	if err := s3.ChangeInfection(600, 600, "U3", InfectionUnknown); err == nil ||
		codeOf(err) != ErrStateConflict {
		t.Fatalf("unknown->unknown must be state conflict, got %v", err)
	}

	// negative -> HBV new infection handled the same way.
	s4 := NewSystem(testCfg())
	mustChair(t, s4, "N1", ZoneNormal, false)
	mustChair(t, s4, "I1", ZoneIsolation, false)
	mustPatient(t, s4, "P4", InfectionNegative)
	p4, err := s4.AddPlan(0, "P4", weekdaySet(0), 1000, 200, 0, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s4.ChangeInfection(500, 500, "P4", InfectionHBV); err != nil {
		t.Fatal(err)
	}
	mv, _ := s4.GetTreatment(p4.TreatmentIDs[0])
	if mv.ChairID != "I1" {
		t.Fatalf("newly infected patient must be reassigned to I1, got %s", mv.ChairID)
	}
}

// Cancelling one treatment releases the chair incl. disinfection tail.
func TestCancellationReleasesChair(t *testing.T) {
	s := NewSystem(testCfg())
	mustChair(t, s, "N1", ZoneNormal, false)
	mustPatient(t, s, "PA", InfectionNegative)
	mustPatient(t, s, "PB", InfectionNegative)
	pa, err := s.AddPlan(0, "PA", weekdaySet(0), 300, 240, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlan(0, "PB", weekdaySet(0), 569, 60, 0, 1000); err == nil {
		t.Fatal("PB must be blocked by the disinfection tail until minute 270")
	}
	if err := s.CancelTreatment(100, pa.TreatmentIDs[0]); err != nil {
		t.Fatal(err)
	}
	pb, err := s.AddPlan(200, "PB", weekdaySet(0), 569, 60, 0, 1000)
	if err != nil {
		t.Fatalf("chair must be free after cancellation: %v", err)
	}
	if err := s.CancelTreatment(600, pb.TreatmentIDs[0]); err == nil ||
		codeOf(err) != ErrStateConflict {
		t.Fatalf("cancelling a started treatment want state conflict, got %v", err)
	}
}

// CancelPlan removes everything before start; rejects if any has started.
func TestCancelPlan(t *testing.T) {
	s := NewSystem(testCfg())
	mustChair(t, s, "N1", ZoneNormal, true)
	mustPatient(t, s, "P1", InfectionNegative)
	plan, err := s.AddPlan(0, "P1", weekdaySet(0), 1000, 200, 0, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPlan(500, plan.ID); err != nil {
		t.Fatal(err)
	}
	ids, _ := s.ChairTreatments("N1")
	if len(ids) != 0 {
		t.Fatalf("all occurrences must be removed, got %d", len(ids))
	}
	if _, err := s.GetTreatment(plan.TreatmentIDs[0]); codeOf(err) != ErrNotFound {
		t.Fatalf("cancelled treatment lookup want not found, got %v", err)
	}

	s2 := NewSystem(testCfg())
	mustChair(t, s2, "N1", ZoneNormal, true)
	mustPatient(t, s2, "P1", InfectionNegative)
	p2, err := s2.AddPlan(0, "P1", weekdaySet(0), 1000, 200, 0, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.CancelPlan(1100, p2.ID); err == nil ||
		codeOf(err) != ErrStateConflict {
		t.Fatalf("cancel after first start want state conflict, got %v", err)
	}
	keep, _ := s2.GetTreatment(p2.TreatmentIDs[0])
	if keep.ID == "" {
		t.Fatal("rejected plan cancellation must keep treatments")
	}
}

// Clock rollback and error-priority checks.
func TestClockAndErrorPriority(t *testing.T) {
	s := NewSystem(testCfg())
	mustChair(t, s, "N1", ZoneNormal, false)
	mustPatient(t, s, "P1", InfectionNegative)

	if err := s.RegisterChair(100, "N9", ZoneNormal, false); err != nil {
		t.Fatal(err)
	}
	// Clock rollback outranks object-not-found.
	err := s.FaultChair(50, 50, 100, "missing")
	if codeOf(err) != ErrClockRollback {
		t.Fatalf("got %v want clock rollback", err)
	}
	// Invalid argument outranks clock rollback.
	err = s.FaultChair(50, 50, 40, "")
	if codeOf(err) != ErrInvalidArgument {
		t.Fatalf("got %v want invalid argument", err)
	}
	// Not found after clock is fine.
	err = s.FaultChair(200, 200, 300, "missing")
	if codeOf(err) != ErrNotFound {
		t.Fatalf("got %v want not found", err)
	}
	// Rejected operation does not move the clock: 50 still rolls back.
	err = s.RegisterChair(50, "NX", ZoneNormal, false)
	if codeOf(err) != ErrClockRollback {
		t.Fatalf("rejected op must not update clock, got %v", err)
	}

	// Bad duration and out-of-range timestamps.
	if err := s.RegisterPatient(100, "", InfectionNegative); codeOf(err) != ErrInvalidArgument {
		t.Fatalf("empty id got %v", err)
	}
	var wk [7]bool
	wk[0] = true
	if _, err := s.AddPlan(300, "P1", wk, 0, 0, 0, 100); codeOf(err) != ErrInvalidArgument {
		t.Fatalf("zero duration got %v", err)
	}
	if _, err := s.AddPlan(300, "P1", wk, 0, 100, 0, MaxTime+1); codeOf(err) != ErrInvalidArgument {
		t.Fatalf("out of range got %v", err)
	}
}

// Patient recovery interval conflict.
func TestPatientRecovery(t *testing.T) {
	s := NewSystem(testCfg())
	mustChair(t, s, "C1", ZoneNormal, true)
	mustChair(t, s, "C2", ZoneNormal, true)
	mustPatient(t, s, "P1", InfectionNegative)
	// Two plans producing treatments closer than MinRecovery (60) on the
	// same day: first 0..200, second at 240 (gap end 260 required).
	if _, err := s.AddPlan(0, "P1", weekdaySet(0), 0, 200, 0, 1000); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddPlan(0, "P1", weekdaySet(0), 240, 60, 0, 1000)
	if codeOf(err) != ErrPatientConflict {
		t.Fatalf("got %v want patient conflict", err)
	}
	// At exactly 260 (200 + recovery 60) it is feasible, though chair C1
	// disinfection (30) also satisfied; same chair.
	if _, err := s.AddPlan(0, "P1", weekdaySet(0), 260, 60, 0, 1000); err != nil {
		t.Fatalf("exactly-at recovery boundary must be allowed: %v", err)
	}
}
