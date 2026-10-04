package review

import (
	"errors"
	"testing"
)

func mustDrug(t *testing.T, e *Engine, id string, ing []byte, mg, level int) {
	t.Helper()
	if err := e.Formulary().AddDrug(id, ing, mg, level); err != nil {
		t.Fatalf("AddDrug %s: %v", id, err)
	}
}

func mustMax(t *testing.T, e *Engine, ing []byte, m int) {
	t.Helper()
	if err := e.Formulary().SetMax(ing, m); err != nil {
		t.Fatalf("SetMax %s: %v", ing, err)
	}
}

func mustPair(t *testing.T, e *Engine, a, b []byte, g int) {
	t.Helper()
	if err := e.Table().SetPair(a, b, g); err != nil {
		t.Fatalf("SetPair: %v", err)
	}
}

func mustAllergy(t *testing.T, e *Engine, p string, ing []byte) {
	t.Helper()
	if err := e.Table().SetAllergy(p, ing); err != nil {
		t.Fatalf("SetAllergy: %v", err)
	}
}

func seedCommon(t *testing.T, e *Engine) {
	t.Helper()
	for _, err := range []error{
		e.AddDoctor("d1", 1),
		e.AddDoctor("d2", 2),
		e.AddDoctor("d3", 3),
		e.AddPharmacist("ph"),
		e.AddPatient("p"),
		e.AddPatient("p2"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func seErr(err error) *SubmitError {
	var se *SubmitError
	errors.As(err, &se)
	return se
}

// The worked daily-cap examples from the specification.
func TestSpecExampleMaxDay(t *testing.T) {
	e := NewEngine(3)
	mustDrug(t, e, "P1", []byte("X"), 500, 1)
	mustDrug(t, e, "P2", []byte("X"), 650, 1)
	mustDrug(t, e, "P3", []byte("X"), 250, 1)
	mustMax(t, e, []byte("X"), 4000)
	seedCommon(t, e)

	submit := func(drug string, per, pd, s, ed int) error {
		_, _, _, err := e.Submit(1, "d1", "p", []Item{{Drug: drug, Per: per, PerDay: pd, Start: s, End: ed}})
		return err
	}

	// P1: 2 tablets x 3/day = 3000 on [1,8).
	if err := submit("P1", 2, 3, 1, 8); err != nil {
		t.Fatalf("P1: %v", err)
	}
	// P2 1x2 = 1300 on [7,10): day 7 sums 4300 -> over max.
	if se := seErr(submit("P2", 1, 2, 7, 10)); se == nil || se.Reason != ErrOverMax || se.Index != 0 {
		t.Fatalf("overlap day7: got %v", se)
	}
	// [8,10) shares no day with [1,8): passes.
	if err := submit("P2", 1, 2, 8, 10); err != nil {
		t.Fatalf("adjacent intervals: %v", err)
	}

	// Fresh engine: 1/day = 650 on [7,10): 3650 passes.
	e2 := NewEngine(3)
	mustDrug(t, e2, "P1", []byte("X"), 500, 1)
	mustDrug(t, e2, "P2", []byte("X"), 650, 1)
	mustMax(t, e2, []byte("X"), 4000)
	seedCommon(t, e2)
	if _, _, _, err := e2.Submit(1, "d1", "p", []Item{{Drug: "P1", Per: 2, PerDay: 3, Start: 1, End: 8}}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e2.Submit(1, "d1", "p", []Item{{Drug: "P2", Per: 1, PerDay: 1, Start: 7, End: 10}}); err != nil {
		t.Fatalf("3650 pass: %v", err)
	}

	// P3 2x2 = 1000 on [7,10): exactly 4000 passes.
	e3 := NewEngine(3)
	mustDrug(t, e3, "P1", []byte("X"), 500, 1)
	mustDrug(t, e3, "P3", []byte("X"), 250, 1)
	mustMax(t, e3, []byte("X"), 4000)
	seedCommon(t, e3)
	if _, _, _, err := e3.Submit(1, "d1", "p", []Item{{Drug: "P1", Per: 2, PerDay: 3, Start: 1, End: 8}}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e3.Submit(1, "d1", "p", []Item{{Drug: "P3", Per: 2, PerDay: 2, Start: 7, End: 10}}); err != nil {
		t.Fatalf("exactly 4000: %v", err)
	}

	// One milligram over the cap is rejected.
	e4 := NewEngine(3)
	mustDrug(t, e4, "P1", []byte("X"), 500, 1)
	mustDrug(t, e4, "P3", []byte("X"), 250, 1)
	mustMax(t, e4, []byte("X"), 3999)
	seedCommon(t, e4)
	if _, _, _, err := e4.Submit(1, "d1", "p", []Item{{Drug: "P1", Per: 2, PerDay: 3, Start: 1, End: 8}}); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := e4.Submit(1, "d1", "p", []Item{{Drug: "P3", Per: 2, PerDay: 2, Start: 7, End: 10}})
	if se := seErr(err); se == nil || se.Reason != ErrOverMax {
		t.Fatalf("4000 > 3999 must reject, got %v", se)
	}
}

// The worked interaction examples from the specification.
func TestSpecExampleInteractions(t *testing.T) {
	build := func(t *testing.T) *Engine {
		e := NewEngine(3)
		mustDrug(t, e, "DW", []byte("W"), 100, 1)
		mustDrug(t, e, "DS", []byte("S"), 100, 1)
		mustDrug(t, e, "DF", []byte("F"), 100, 1)
		mustPair(t, e, []byte("W"), []byte("S"), 2)
		mustPair(t, e, []byte("W"), []byte("F"), 3)
		mustPair(t, e, []byte("S"), []byte("F"), 1)
		seedCommon(t, e)
		return e
	}

	// [S item, F item] together: F contraindicated with W, index 1.
	e := build(t)
	if _, _, _, err := e.Submit(1, "d1", "p", []Item{{Drug: "DW", Per: 1, PerDay: 1, Start: 1, End: 30}}); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := e.Submit(1, "d1", "p", []Item{
		{Drug: "DS", Per: 1, PerDay: 1, Start: 5, End: 10},
		{Drug: "DF", Per: 1, PerDay: 1, Start: 5, End: 10},
	})
	if se := seErr(err); se == nil || se.Reason != ErrBan || se.Index != 1 {
		t.Fatalf("ban index 1, got %+v", se)
	}

	// S alone -> pending with hint (S,W) grade 2.
	e = build(t)
	if _, _, _, err := e.Submit(1, "d1", "p", []Item{{Drug: "DW", Per: 1, PerDay: 1, Start: 1, End: 30}}); err != nil {
		t.Fatal(err)
	}
	id, status, hints, err := e.Submit(1, "d1", "p", []Item{{Drug: "DS", Per: 1, PerDay: 1, Start: 5, End: 10}})
	if err != nil || status != StatusPending {
		t.Fatalf("pending, got status=%d err=%v", status, err)
	}
	if len(hints) != 1 || string(hints[0].IngA) != "S" || string(hints[0].IngB) != "W" || hints[0].Grade != 2 {
		t.Fatalf("hints = %+v", hints)
	}
	// F on [6,8) is banned by the pending S? no; banned by active W.
	_, _, _, err = e.Submit(1, "d1", "p", []Item{{Drug: "DF", Per: 1, PerDay: 1, Start: 6, End: 8}})
	if se := seErr(err); se == nil || se.Reason != ErrBan {
		t.Fatalf("F banned by W, got %+v", se)
	}

	// At now=3 the pending prescription can still be approved.
	if err := e.Approve(3, "ph", id); err != nil {
		t.Fatalf("approve at 3: %v", err)
	}
	if r, _ := e.RxRecord(id); r.Status != StatusActive {
		t.Fatalf("status %d", r.Status)
	}

	// At now=4 a new operation lands the (now expired) pending prescription.
	e2 := build(t)
	if _, _, _, err := e2.Submit(1, "d1", "p", []Item{{Drug: "DW", Per: 1, PerDay: 1, Start: 1, End: 30}}); err != nil {
		t.Fatal(err)
	}
	id2, st2, _, err := e2.Submit(1, "d1", "p", []Item{{Drug: "DS", Per: 1, PerDay: 1, Start: 5, End: 10}})
	if err != nil || st2 != StatusPending {
		t.Fatal(err)
	}
	if err := e2.Approve(4, "ph", id2); err != ErrState {
		t.Fatalf("expired at 4, got %v", err)
	}
	if r, _ := e2.RxRecord(id2); r.Status != StatusVoided {
		t.Fatalf("status %d", r.Status)
	}

	// Stop W on day 6 truncates to [1,6); F on [6,8) is then allowed.
	e3 := build(t)
	wid, _, _, err := e3.Submit(1, "d1", "p", []Item{{Drug: "DW", Per: 1, PerDay: 1, Start: 1, End: 30}})
	if err != nil {
		t.Fatal(err)
	}
	sid, _, _, err := e3.Submit(1, "d1", "p", []Item{{Drug: "DS", Per: 1, PerDay: 1, Start: 5, End: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e3.Approve(2, "ph", sid); err != nil {
		t.Fatal(err)
	}
	if err := e3.Stop(6, "d1", wid); err != nil {
		t.Fatal(err)
	}
	if r, _ := e3.RxRecord(wid); r.Items[0].End != 6 {
		t.Fatalf("W truncated to %d", r.Items[0].End)
	}
	_, _, hints3, err := e3.Submit(6, "d1", "p", []Item{{Drug: "DF", Per: 1, PerDay: 1, Start: 6, End: 8}})
	if err != nil {
		t.Fatalf("F allowed after stop: %v", err)
	}
	if len(hints3) != 1 || string(hints3[0].IngA) != "F" || string(hints3[0].IngB) != "S" || hints3[0].Grade != 1 {
		t.Fatalf("hint (F,S) grade1, got %+v", hints3)
	}
}
