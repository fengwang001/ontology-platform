package hemo

import (
	"errors"
	"testing"
)

func codeOf(err error) ErrCode {
	var oe *OpError
	if errors.As(err, &oe) {
		return oe.Code
	}
	return 0
}

func mustChair(t *testing.T, s *System, id string, zone Zone, obs bool) {
	t.Helper()
	if err := s.RegisterChair(0, id, zone, obs); err != nil {
		t.Fatalf("register chair %s: %v", id, err)
	}
}

func mustPatient(t *testing.T, s *System, id string, inf Infection) {
	t.Helper()
	if err := s.RegisterPatient(0, id, inf); err != nil {
		t.Fatalf("register patient %s: %v", id, err)
	}
}

func weekdaySet(days ...int) [7]bool {
	var w [7]bool
	for _, d := range days {
		w[d] = true
	}
	return w
}

// Disinfection boundary: end+gap == next start allowed; one less rejected.
func TestDisinfectionBoundary(t *testing.T) {
	newSys := func() *System {
		s := NewSystem(testCfg())
		mustChair(t, s, "N1", ZoneNormal, false)
		mustPatient(t, s, "PA", InfectionNegative)
		mustPatient(t, s, "PB", InfectionNegative)
		return s
	}

	s := newSys()
	if _, err := s.AddPlan(0, "PA", weekdaySet(0), 0, 240, 0, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlan(0, "PB", weekdaySet(0), 270, 100, 0, 1000); err != nil {
		t.Fatalf("end+gap == start must be allowed: %v", err)
	}

	s2 := newSys()
	if _, err := s2.AddPlan(0, "PA", weekdaySet(0), 0, 240, 0, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.AddPlan(0, "PB", weekdaySet(0), 269, 100, 0, 1000); err == nil {
		t.Fatal("one minute before gap end must be rejected")
	} else if codeOf(err) != ErrNoFeasibleChair {
		t.Fatalf("got %v want no feasible chair", err)
	}
}

// HBV adjacent to HCV on an isolation chair requires deep disinfection.
func TestDeepDisinfectionHBVHCV(t *testing.T) {
	s := NewSystem(testCfg())
	mustChair(t, s, "I1", ZoneIsolation, false)
	mustPatient(t, s, "H1", InfectionHBV)
	mustPatient(t, s, "C1", InfectionHCV)

	if _, err := s.AddPlan(0, "H1", weekdaySet(0), 0, 240, 0, 2000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlan(0, "C1", weekdaySet(0), 280, 100, 0, 2000); err == nil {
		t.Fatal("HBV->HCV within deep gap must be rejected")
	}
	if _, err := s.AddPlan(0, "C1", weekdaySet(0), 360, 100, 0, 2000); err != nil {
		t.Fatalf("HBV->HCV exactly at deep gap end must be allowed: %v", err)
	}

	s2 := NewSystem(testCfg())
	mustChair(t, s2, "I2", ZoneIsolation, false)
	mustPatient(t, s2, "H2", InfectionHBV)
	mustPatient(t, s2, "H3", InfectionHBV)
	if _, err := s2.AddPlan(0, "H2", weekdaySet(0), 0, 240, 0, 2000); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.AddPlan(0, "H3", weekdaySet(0), 280, 100, 0, 2000); err != nil {
		t.Fatalf("same HBV type with regular gap must be allowed: %v", err)
	}
}

// Pending patients may only use observation chairs in the normal zone.
func TestPendingObservationOnly(t *testing.T) {
	s := NewSystem(testCfg())
	mustChair(t, s, "N1", ZoneNormal, false)
	mustChair(t, s, "N2", ZoneNormal, true)
	mustChair(t, s, "I1", ZoneIsolation, false)
	mustPatient(t, s, "U1", InfectionUnknown)

	plan, err := s.AddPlan(0, "U1", weekdaySet(0), 100, 120, 0, 2000)
	if err != nil {
		t.Fatalf("pending patient must be placed on observation chair: %v", err)
	}
	tv, _ := s.GetTreatment(plan.TreatmentIDs[0])
	if tv.ChairID != "N2" {
		t.Fatalf("got chair %s want N2", tv.ChairID)
	}

	s2 := NewSystem(testCfg())
	mustChair(t, s2, "N1", ZoneNormal, false)
	mustChair(t, s2, "I1", ZoneIsolation, false)
	mustPatient(t, s2, "U2", InfectionUnknown)
	_, err = s2.AddPlan(0, "U2", weekdaySet(0), 100, 120, 0, 2000)
	if codeOf(err) != ErrIsolationConflict {
		t.Fatalf("got %v want isolation conflict", err)
	}
}

// Zone restrictions: negative -> normal; positive -> isolation.
func TestZoneRestrictions(t *testing.T) {
	s := NewSystem(testCfg())
	mustChair(t, s, "N1", ZoneNormal, true)
	mustChair(t, s, "I1", ZoneIsolation, false)
	mustPatient(t, s, "HB", InfectionHBV)
	mustPatient(t, s, "NG", InfectionNegative)

	np, err := s.AddPlan(0, "NG", weekdaySet(0), 100, 100, 0, 2000)
	if err != nil {
		t.Fatal(err)
	}
	nt, _ := s.GetTreatment(np.TreatmentIDs[0])
	if nt.ChairID != "N1" {
		t.Fatalf("negative patient on %s want N1", nt.ChairID)
	}
	hp, err := s.AddPlan(0, "HB", weekdaySet(0), 100, 100, 0, 2000)
	if err != nil {
		t.Fatal(err)
	}
	ht, _ := s.GetTreatment(hp.TreatmentIDs[0])
	if ht.ChairID != "I1" {
		t.Fatalf("HBV patient on %s want I1", ht.ChairID)
	}
}
