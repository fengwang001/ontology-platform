package hemo

import "testing"

func testCfg() Config {
	return Config{
		RegularNegative: 30,
		RegularHBV:      40,
		RegularHCV:      40,
		RegularUnknown:  20,
		DeepDisinfect:   120,
		MinRecovery:     60,
	}
}

func TestSmokeBasicPlan(t *testing.T) {
	s := NewSystem(testCfg())
	if err := s.RegisterChair(0, "C1", ZoneNormal, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterChair(0, "C2", ZoneNormal, true); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterPatient(0, "P1", InfectionNegative); err != nil {
		t.Fatal(err)
	}
	var wk [7]bool
	wk[0] = true
	plan, err := s.AddPlan(0, "P1", wk, 600, 240, 0, 20000)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.TreatmentIDs) == 0 {
		t.Fatal("expected occurrences")
	}
	tv, _ := s.GetTreatment(plan.TreatmentIDs[0])
	if tv.ChairID != "C1" {
		t.Fatalf("got chair %q want C1", tv.ChairID)
	}
	if tv.Start != 600 || tv.End != 840 {
		t.Fatalf("got [%d,%d)", tv.Start, tv.End)
	}
}

func TestTimelinePredecessorSuccessor(t *testing.T) {
	var tl Timeline
	mk := func(id string, start, end int) *Treatment {
		return &Treatment{ID: id, Start: start, End: end}
	}
	tl.Add(mk("a", 0, 100))
	tl.Add(mk("b", 1000, 1100))
	tl.Add(mk("c", 10000, 10200))
	if p := tl.Predecessor(500, nil); p == nil || p.ID != "a" {
		t.Fatalf("pred 500 = %v", p)
	}
	if p := tl.Predecessor(1000, nil); p == nil || p.ID != "b" {
		t.Fatalf("pred 1000 = %v", p)
	}
	if p := tl.Successor(1, nil); p == nil || p.ID != "b" {
		t.Fatalf("succ 1 = %v", p)
	}
	if p := tl.Successor(1000, nil); p == nil || p.ID != "b" {
		t.Fatalf("succ 1000 = %v", p)
	}
	if p := tl.Successor(1001, nil); p == nil || p.ID != "c" {
		t.Fatalf("succ 1001 = %v", p)
	}
	if p := tl.Predecessor(9_999_999, nil); p == nil || p.ID != "c" {
		t.Fatalf("pred max = %v", p)
	}
	if p := tl.Successor(9_999_999, nil); p != nil {
		t.Fatalf("succ past max = %v", p)
	}
	tl.Remove(tl.Predecessor(1000, nil))
	if p := tl.Successor(1, nil); p == nil || p.ID != "c" {
		t.Fatalf("after remove b succ = %v", p)
	}
}
