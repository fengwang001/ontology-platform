package hemo

import "testing"

// Helpers build deterministic chair occupancy on two weekdays (day 1 and
// day 3, both at minute 600..800). They use a chain of blocker patients so
// that every target chair ends up occupied on the intended day.

// blockOneChair places blocker on the smallest chair that is busy-free for
// the single occurrence (weekday day, at minute 600), forcing occupancy.
// Callers add blockers in an order that deterministically fills the chairs
// they need.
func addDayBlocker(t *testing.T, s *System, pid string, day int) TreatmentView {
	t.Helper()
	wk := weekdaySet(day)
	from := day * MinutesPerDay
	plan, err := s.AddPlan(0, pid, wk, 600, 200, from, from+MinutesPerDay)
	if err != nil {
		t.Fatalf("blocker %s day %d: %v", pid, day, err)
	}
	tv, _ := s.GetTreatment(plan.TreatmentIDs[0])
	return tv
}

// All-or-nothing expansion plus same-chair preference.
func TestPlanAllOrNothingSameChair(t *testing.T) {
	// Both occurrences fit one chair -> smallest chair is used.
	s := NewSystem(testCfg())
	mustChair(t, s, "C1", ZoneNormal, true)
	mustChair(t, s, "C2", ZoneNormal, true)
	mustPatient(t, s, "P1", InfectionNegative)
	plan, err := s.AddPlan(0, "P1", weekdaySet(0), 600, 200, 0, 20000)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.TreatmentIDs) != 2 {
		t.Fatalf("got %d occurrences want 2", len(plan.TreatmentIDs))
	}
	for _, id := range plan.TreatmentIDs {
		tv, _ := s.GetTreatment(id)
		if tv.ChairID != "C1" {
			t.Fatalf("occurrence on %s want C1", tv.ChairID)
		}
	}

	// Cross pattern: C1 busy on day1 only, C2 busy on day3 only. Build it by
	// filling chairs in order: day1 blockers go to C1 then C2; day3 blockers
	// likewise. To leave C2 free on day1, only one blocker is placed day1.
	// To leave C1 free on day3 while C2 is busy day3, place two blockers on
	// day3 (C1 then C2) -- that would also block C1 day3. Instead use the
	// reverse registration order for chair IDs by naming chairs so the
	// "spare" chair is deterministic: use three chairs Z1,Z2 and block
	// Z1-day1 and Z2-day3 while both chairs host one of the two P1 visits.
	s2 := NewSystem(testCfg())
	mustChair(t, s2, "Z1", ZoneNormal, true)
	mustChair(t, s2, "Z2", ZoneNormal, true)
	mustPatient(t, s2, "P1", InfectionNegative)
	mustPatient(t, s2, "B1", InfectionNegative) // day1 -> Z1
	mustPatient(t, s2, "B2", InfectionNegative) // day3 -> Z1 (free then)
	mustPatient(t, s2, "B3", InfectionNegative) // day3 -> Z2
	d1 := addDayBlocker(t, s2, "B1", 1)
	if d1.ChairID != "Z1" {
		t.Fatalf("B1 on %s want Z1", d1.ChairID)
	}
	d2 := addDayBlocker(t, s2, "B2", 3)
	if d2.ChairID != "Z1" {
		t.Fatalf("B2 on %s want Z1", d2.ChairID)
	}
	// The above blocks Z1 on both days; that does not create the cross
	// pattern. Adjust: instead verify the documented split via the pattern
	// in TestPlanGreedySplit, and here assert that P1 uses Z2 for both
	// (same-chair preference still wins when one chair hosts all).
	plan2, err := s2.AddPlan(0, "P1", weekdaySet(1, 3), 600, 200,
		0, 5*MinutesPerDay)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range plan2.TreatmentIDs {
		tv, _ := s2.GetTreatment(id)
		if tv.ChairID != "Z2" {
			t.Fatalf("same-chair preference must choose Z2 for all, got %s", tv.ChairID)
		}
	}

	// B3 cannot be added (Z1 and Z2 busy day3), exercise rejection:
	if _, err := s2.AddPlan(0, "B3", weekdaySet(3), 600, 200,
		3*MinutesPerDay, 4*MinutesPerDay); err == nil {
		t.Fatal("day3 fully blocked: extra plan must be rejected")
	}

	// All-or-nothing with true split is exercised separately below.
	s3 := NewSystem(testCfg())
	mustChair(t, s3, "Z1", ZoneNormal, true)
	mustChair(t, s3, "Z2", ZoneNormal, true)
	mustPatient(t, s3, "P1", InfectionNegative)
	mustPatient(t, s3, "X1", InfectionNegative) // day1 -> Z1
	mustPatient(t, s3, "X2", InfectionNegative) // day3 -> Z1
	mustPatient(t, s3, "X3", InfectionNegative) // day3 -> Z2
	addDayBlocker(t, s3, "X1", 1)               // Z1 day1
	addDayBlocker(t, s3, "X2", 3)               // Z1 day3
	addDayBlocker(t, s3, "X3", 3)               // Z2 day3
	// Now day1: Z1 busy, Z2 free; day3: both busy. P1 day1/day3 must fail
	// the day3 occurrence, so the whole plan is rejected and nothing lands.
	if _, err := s3.AddPlan(0, "P1", weekdaySet(1, 3), 600, 200,
		0, 5*MinutesPerDay); err == nil {
		t.Fatal("plan with the day3 occurrence unplaceable must be rejected")
	}
	for _, cid := range []string{"Z1", "Z2"} {
		ids, _ := s3.ChairTreatments(cid)
		for _, id := range ids {
			tv, _ := s3.GetTreatment(id)
			if tv.PatientID == "P1" {
				t.Fatal("rejected plan must leave no treatments behind")
			}
		}
	}
}

// Deterministic greedy split: with three chairs and an occupancy pattern
// where no single chair hosts all three occurrences, each occurrence takes
// the smallest feasible chair in ascending start order.
func TestPlanGreedySplit(t *testing.T) {
	// Mask chairs with disjoint per-day fault windows so that no single
	// chair hosts all occurrences but every day leaves one feasible chair:
	//   day1: only Z3, day2: only Z2, day3: only Z1.
	s := splitWithFaults(t)
	plan, err := s.AddPlan(0, "P1", weekdaySet(1, 2, 3), 600, 200,
		0, 5*MinutesPerDay)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Z3", "Z2", "Z1"}
	for i, id := range plan.TreatmentIDs {
		tv, _ := s.GetTreatment(id)
		if tv.ChairID != want[i] {
			t.Fatalf("occurrence %d on %s want %s", i, tv.ChairID, want[i])
		}
	}
}

// splitWithFaults builds three chairs and masks them per day via disjoint
// fault windows: day1 only Z3 available, day2 only Z2, day3 only Z1.
func splitWithFaults(t *testing.T) *System {
	t.Helper()
	s := NewSystem(testCfg())
	mustChair(t, s, "Z1", ZoneNormal, true)
	mustChair(t, s, "Z2", ZoneNormal, true)
	mustChair(t, s, "Z3", ZoneNormal, true)
	mustPatient(t, s, "P1", InfectionNegative)
	// Register disjoint future fault windows at now=0.
	type fw struct {
		chair string
		at    int
		to    int
	}
	windows := []fw{
		{"Z1", 1440, 2880}, {"Z2", 1440, 2880}, // day1: only Z3
		{"Z1", 2880, 4320}, {"Z3", 2880, 4320}, // day2: only Z2
		{"Z2", 4320, 5760}, {"Z3", 4320, 5760}, // day3: only Z1
	}
	for i, w := range windows {
		if err := s.FaultChair(0, w.at, w.to, w.chair); err != nil {
			t.Fatalf("fault %d on %s: %v", i, w.chair, err)
		}
	}
	return s
}

// Fault: reassignment success, whole rejection, in-progress untouched, and
// recovery never pulls reassigned treatments back.
func TestFaultScenarios(t *testing.T) {
	s := NewSystem(testCfg())
	mustChair(t, s, "C1", ZoneNormal, true)
	mustChair(t, s, "C2", ZoneNormal, true)
	mustPatient(t, s, "P1", InfectionNegative)
	plan, err := s.AddPlan(0, "P1", weekdaySet(0), 1000, 200, 0, 30000)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.GetTreatment(plan.TreatmentIDs[0]) // 1000..1200
	if err := s.FaultChair(1200, 1200, 50000, "C1"); err != nil {
		t.Fatal(err)
	}
	f1, _ := s.GetTreatment(first.ID)
	if f1.ChairID != "C1" {
		t.Fatalf("finished treatment moved to %s", f1.ChairID)
	}
	second, _ := s.GetTreatment(plan.TreatmentIDs[1])
	if second.ChairID != "C2" {
		t.Fatalf("future treatment on %s want C2", second.ChairID)
	}

	s2 := NewSystem(testCfg())
	mustChair(t, s2, "C1", ZoneNormal, true)
	mustChair(t, s2, "C2", ZoneNormal, true)
	mustPatient(t, s2, "P1", InfectionNegative)
	p2, err := s2.AddPlan(0, "P1", weekdaySet(0), 1000, 300, 0, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.FaultChair(1100, 1100, 50000, "C1"); err != nil {
		t.Fatal(err)
	}
	tv, _ := s2.GetTreatment(p2.TreatmentIDs[0])
	if tv.ChairID != "C1" {
		t.Fatalf("in-progress treatment moved to %s", tv.ChairID)
	}

	s3 := NewSystem(testCfg())
	mustChair(t, s3, "I1", ZoneIsolation, false)
	mustChair(t, s3, "N1", ZoneNormal, false)
	mustPatient(t, s3, "H1", InfectionHBV)
	p3, err := s3.AddPlan(0, "H1", weekdaySet(0), 1000, 200, 0, 30000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s3.FaultChair(500, 500, 50000, "I1"); err == nil {
		t.Fatal("fault with nowhere to reassign must be rejected")
	} else if codeOf(err) != ErrIsolationConflict {
		t.Fatalf("got %v want isolation conflict", err)
	}
	tv3, _ := s3.GetTreatment(p3.TreatmentIDs[0])
	if tv3.ChairID != "I1" {
		t.Fatalf("rejected fault must keep treatment on I1, got %s", tv3.ChairID)
	}
	if c := s3.chairs["I1"]; !c.AvailableAt(501) {
		t.Fatal("rejected fault must not mark the chair unavailable")
	}

	if err := s.RecoverChair(40000, "C1"); err != nil {
		t.Fatal(err)
	}
	still, _ := s.GetTreatment(plan.TreatmentIDs[1])
	if still.ChairID != "C2" {
		t.Fatalf("recovered chair must not pull treatment back, got %s", still.ChairID)
	}
}
