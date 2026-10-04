package review

import (
	"bytes"
	"errors"
	"testing"

	"ontology/formulary"
	"ontology/interact"
)

func submitErr(e *Engine, now int, doc, pat string, items ...Item) *SubmitError {
	_, _, _, err := e.Submit(now, doc, pat, items)
	return seErr(err)
}

// Pending prescriptions occupy capacity: two pending approvals would
// otherwise overshoot after the fact.
func TestPendingOccupiesCapacity(t *testing.T) {
	e := NewEngine(10)
	mustDrug(t, e, "D", []byte("X"), 1000, 1)
	mustMax(t, e, []byte("X"), 3000)
	seedCommon(t, e)
	// 3000 pending.
	if se := submitErr(e, 1, "d1", "p", Item{Drug: "D", Per: 3, PerDay: 1, Start: 1, End: 10}); se != nil {
		t.Fatal(se)
	}
	// Another 100 on the same day is rejected while the first is pending.
	if se := submitErr(e, 1, "d1", "p", Item{Drug: "D", Per: 1, PerDay: 1, Start: 2, End: 5}); se == nil || se.Reason != ErrOverMax {
		t.Fatalf("pending occupies quota, got %+v", se)
	}
}

func TestRejectPrecedence(t *testing.T) {
	// ban is checked before overmax; allergy before ban.
	e := NewEngine(10)
	mustDrug(t, e, "A", []byte("W"), 500, 1) // 5/day = 2500
	mustDrug(t, e, "B", []byte("F"), 1000, 1)
	mustPair(t, e, []byte("W"), []byte("F"), 3)
	mustMax(t, e, []byte("W"), 3000)
	seedCommon(t, e)
	// Existing W prescription.
	if se := submitErr(e, 1, "d1", "p", Item{Drug: "A", Per: 5, PerDay: 1, Start: 1, End: 30}); se != nil {
		t.Fatal(se)
	}
	se := submitErr(e, 1, "d1", "p",
		Item{Drug: "B", Per: 1, PerDay: 1, Start: 5, End: 10}, // F, banned vs W
		Item{Drug: "A", Per: 2, PerDay: 1, Start: 5, End: 10}, // W: 2500+1000=3500 over cap
	)
	if se == nil || se.Reason != ErrBan || se.Index != 0 {
		t.Fatalf("ban before overmax, got %+v", se)
	}
	// Same submit with F removed would be rejected by the cap.
	if se := submitErr(e, 1, "d1", "p", Item{Drug: "A", Per: 2, PerDay: 1, Start: 5, End: 10}); se == nil || se.Reason != ErrOverMax {
		t.Fatalf("overmax without ban, got %+v", se)
	}

	// Allergy before ban: allergy must be configured before the first Submit.
	eA := NewEngine(10)
	mustDrug(t, eA, "A", []byte("W"), 500, 1)
	mustDrug(t, eA, "B", []byte("F"), 1000, 1)
	mustPair(t, eA, []byte("W"), []byte("F"), 3)
	mustMax(t, eA, []byte("W"), 3000)
	mustAllergy(t, eA, "p", []byte("F"))
	seedCommon(t, eA)
	if se := submitErr(eA, 1, "d1", "p", Item{Drug: "A", Per: 5, PerDay: 1, Start: 1, End: 30}); se != nil {
		t.Fatal(se)
	}
	se = submitErr(eA, 1, "d1", "p",
		Item{Drug: "B", Per: 1, PerDay: 1, Start: 5, End: 10},
		Item{Drug: "A", Per: 2, PerDay: 1, Start: 5, End: 10},
	)
	if se == nil || se.Reason != ErrAllergy || se.Index != 0 {
		t.Fatalf("allergy before ban, got %+v", se)
	}

	// Privilege reports smallest offending index.
	e2 := NewEngine(10)
	mustDrug(t, e2, "L1", []byte("Q"), 1, 1)
	mustDrug(t, e2, "L3", []byte("R"), 1, 3)
	mustDrug(t, e2, "L2", []byte("T"), 1, 2)
	seedCommon(t, e2)
	se = submitErr(e2, 1, "d1", "p",
		Item{Drug: "L1", Per: 1, PerDay: 1, Start: 1, End: 5},
		Item{Drug: "L3", Per: 1, PerDay: 1, Start: 1, End: 5},
		Item{Drug: "L2", Per: 1, PerDay: 1, Start: 1, End: 5},
	)
	if se == nil || se.Reason != ErrNoPrivilege || se.Index != 1 {
		t.Fatalf("privilege smallest index, got %+v", se)
	}
}

func TestHintOrdering(t *testing.T) {
	e := NewEngine(10)
	mustDrug(t, e, "W", []byte("W"), 1, 1)
	mustDrug(t, e, "S", []byte("S"), 1, 1)
	mustDrug(t, e, "F", []byte("F"), 1, 1)
	mustDrug(t, e, "Z", []byte("Z"), 1, 1)
	mustDrug(t, e, "A", []byte("A"), 1, 1)
	mustPair(t, e, []byte("W"), []byte("S"), 2)
	mustPair(t, e, []byte("S"), []byte("F"), 1)
	mustPair(t, e, []byte("A"), []byte("Z"), 2)
	seedCommon(t, e)
	if se := submitErr(e, 1, "d1", "p", Item{Drug: "W", Per: 1, PerDay: 1, Start: 1, End: 20}); se != nil {
		t.Fatal(se)
	}
	_, _, hints, err := e.Submit(1, "d1", "p", []Item{
		{Drug: "S", Per: 1, PerDay: 1, Start: 1, End: 10},
		{Drug: "F", Per: 1, PerDay: 1, Start: 1, End: 10},
		{Drug: "A", Per: 1, PerDay: 1, Start: 1, End: 10},
		{Drug: "Z", Per: 1, PerDay: 1, Start: 1, End: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Hint{
		{IngA: []byte("A"), IngB: []byte("Z"), Grade: 2},
		{IngA: []byte("S"), IngB: []byte("W"), Grade: 2},
		{IngA: []byte("F"), IngB: []byte("S"), Grade: 1},
	}
	if len(hints) != len(want) {
		t.Fatalf("hints %+v", hints)
	}
	for i := range want {
		if hints[i].Grade != want[i].Grade ||
			!bytes.Equal(hints[i].IngA, want[i].IngA) ||
			!bytes.Equal(hints[i].IngB, want[i].IngB) {
			t.Fatalf("hint %d = %+v, want %+v", i, hints[i], want[i])
		}
	}
}

func TestDenyReleasesQuota(t *testing.T) {
	e := NewEngine(10)
	mustDrug(t, e, "DX", []byte("X"), 1000, 1)
	mustDrug(t, e, "DY", []byte("Y"), 1, 1)
	mustDrug(t, e, "DW", []byte("W"), 1, 1)
	mustMax(t, e, []byte("X"), 3000)
	mustPair(t, e, []byte("Y"), []byte("W"), 2)
	seedCommon(t, e)
	// Existing W makes the Y item trigger caution, so the combined
	// prescription is pending while its X item already occupies 3000/day.
	if se := submitErr(e, 1, "d1", "p", Item{Drug: "DW", Per: 1, PerDay: 1, Start: 1, End: 30}); se != nil {
		t.Fatal(se)
	}
	pid, st, _, err := e.Submit(1, "d1", "p", []Item{
		{Drug: "DX", Per: 3, PerDay: 1, Start: 1, End: 10},
		{Drug: "DY", Per: 1, PerDay: 1, Start: 1, End: 10},
	})
	if err != nil || st != StatusPending {
		t.Fatalf("pending: st=%d err=%v", st, err)
	}
	if se := submitErr(e, 1, "d1", "p", Item{Drug: "DX", Per: 1, PerDay: 1, Start: 2, End: 5}); se == nil || se.Reason != ErrOverMax {
		t.Fatalf("pending X occupies quota, got %+v", se)
	}
	if err := e.Deny(2, "ph", pid); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.RxRecord(pid); r.Status != StatusVoided {
		t.Fatalf("status %d", r.Status)
	}
	if se := submitErr(e, 2, "d1", "p", Item{Drug: "DX", Per: 1, PerDay: 1, Start: 2, End: 5}); se != nil {
		t.Fatalf("after deny quota released: %+v", se)
	}
}

func TestFreezeAndClockAndState(t *testing.T) {
	e := NewEngine(5)
	mustDrug(t, e, "D", []byte("X"), 100, 1)
	mustMax(t, e, []byte("X"), 100000)
	seedCommon(t, e)
	id, st, _, err := e.Submit(10, "d1", "p", []Item{{Drug: "D", Per: 1, PerDay: 1, Start: 10, End: 20}})
	if err != nil || st != StatusActive {
		t.Fatal(err)
	}
	// Configuration is frozen after the first accepted Submit.
	if err := e.Formulary().AddDrug("D2", []byte("Y"), 1, 1); !errors.Is(err, formulary.ErrFrozen) {
		t.Fatalf("frozen AddDrug, got %v", err)
	}
	if err := e.Formulary().SetMax([]byte("X"), 1); !errors.Is(err, formulary.ErrFrozen) {
		t.Fatalf("frozen SetMax, got %v", err)
	}
	if err := e.Table().SetPair([]byte("X"), []byte("Y"), 1); !errors.Is(err, interact.ErrFrozen) {
		t.Fatalf("frozen SetPair, got %v", err)
	}
	if err := e.Table().SetAllergy("p", []byte("X")); !errors.Is(err, interact.ErrFrozen) {
		t.Fatalf("frozen SetAllergy, got %v", err)
	}
	// Personnel registration still works.
	if err := e.AddDoctor("d9", 2); err != nil {
		t.Fatal(err)
	}

	// Invalid parameter beats clock rollback.
	if se := submitErr(e, 5, "", "p", Item{Drug: "D", Per: 1, PerDay: 1, Start: 5, End: 6}); se == nil || se.Reason != ErrInvalid {
		t.Fatalf("invalid first, got %+v", se)
	}
	// Clock rollback with otherwise-valid parameters; rejected call does not
	// land expiries nor advance the clock.
	if se := submitErr(e, 5, "d1", "p", Item{Drug: "D", Per: 1, PerDay: 1, Start: 5, End: 6}); se == nil || se.Reason != ErrClock {
		t.Fatalf("clock rollback, got %+v", se)
	}
	// Missing entity beats privilege: unknown doctor, level-3 drug.
	if se := submitErr(e, 10, "ghost", "p", Item{Drug: "D", Per: 1, PerDay: 1, Start: 10, End: 11}); se == nil || se.Reason != ErrNoEntity {
		t.Fatalf("no doctor, got %+v", se)
	}

	// Approve/Deny/Stop order: invalid > clock > missing > auth > state.
	if err := e.Approve(5, "ph", id); err != ErrClock {
		t.Fatalf("approve clock, got %v", err)
	}
	if err := e.Approve(11, "ghost", id); err != ErrNoRx {
		t.Fatalf("unknown pharmacist, got %v", err)
	}
	if err := e.Stop(11, "ghost", id); err != ErrNoRx {
		t.Fatalf("unknown doctor, got %v", err)
	}
	if err := e.Stop(11, "d2", id); err != ErrNoAuth {
		t.Fatalf("other level-2 doctor cannot stop, got %v", err)
	}
	// Original doctor and level-3 doctor may stop.
	if err := e.Stop(12, "d1", id); err != nil {
		t.Fatalf("owner stop: %v", err)
	}
	if err := e.Approve(13, "ph", id); err != ErrState {
		t.Fatalf("approve active: %v", err)
	}
	if err := e.Deny(13, "ph", id); err != ErrState {
		t.Fatalf("deny active: %v", err)
	}
	if err := e.Stop(14, "d3", id); err != nil {
		t.Fatalf("level-3 stop: %v", err)
	}
	// Stopping a voided prescription fails with state mismatch.
	if err := e.Deny(15, "ph", id); err != ErrState {
		t.Fatalf("deny again: %v", err)
	}
}

func TestInvalidParameters(t *testing.T) {
	e := NewEngine(5)
	mustDrug(t, e, "D", []byte("X"), 100, 1)
	seedCommon(t, e)
	cases := []Item{
		{Drug: "", Per: 1, PerDay: 1, Start: 1, End: 2},
		{Drug: "D", Per: 0, PerDay: 1, Start: 1, End: 2},
		{Drug: "D", Per: 21, PerDay: 1, Start: 1, End: 2},
		{Drug: "D", Per: 1, PerDay: 0, Start: 1, End: 2},
		{Drug: "D", Per: 1, PerDay: 13, Start: 1, End: 2},
		{Drug: "D", Per: 1, PerDay: 1, Start: 0, End: 2}, // now=1 > start
		{Drug: "D", Per: 1, PerDay: 1, Start: 2, End: 2}, // empty
		{Drug: "D", Per: 1, PerDay: 1, Start: 3, End: 2},
	}
	for i, it := range cases {
		if se := submitErr(e, 1, "d1", "p", it); se == nil || se.Reason != ErrInvalid {
			t.Fatalf("case %d: got %+v", i, se)
		}
	}
	// Duplicate drug within one prescription.
	if se := submitErr(e, 1, "d1", "p",
		Item{Drug: "D", Per: 1, PerDay: 1, Start: 1, End: 3},
		Item{Drug: "D", Per: 1, PerDay: 1, Start: 1, End: 3},
	); se == nil || se.Reason != ErrInvalid {
		t.Fatalf("duplicate drug, got %+v", se)
	}
	// Empty item list.
	if se := submitErr(e, 1, "d1", "p"); se == nil || se.Reason != ErrInvalid {
		t.Fatalf("empty items, got %+v", se)
	}
}

func TestAdjacentIntervalsDoNotInteract(t *testing.T) {
	e := NewEngine(10)
	mustDrug(t, e, "A", []byte("W"), 500, 1)
	mustDrug(t, e, "B", []byte("F"), 100, 1)
	mustPair(t, e, []byte("W"), []byte("F"), 3)
	mustMax(t, e, []byte("W"), 1000)
	seedCommon(t, e)
	if se := submitErr(e, 1, "d1", "p", Item{Drug: "A", Per: 2, PerDay: 1, Start: 1, End: 5}); se != nil {
		t.Fatal(se)
	}
	// [5,8) abuts [1,5): no shared day, so neither ban nor cap applies.
	id, st, hints, err := e.Submit(5, "d1", "p", []Item{{Drug: "B", Per: 1, PerDay: 1, Start: 5, End: 8}})
	if err != nil || st != StatusActive || len(hints) != 0 {
		t.Fatalf("adjacent: id=%d st=%d hints=%+v err=%v", id, st, hints, err)
	}
}

func TestStopEmptiesItems(t *testing.T) {
	e := NewEngine(10)
	mustDrug(t, e, "D", []byte("X"), 500, 1)
	mustMax(t, e, []byte("X"), 1000)
	seedCommon(t, e)
	id, _, _, err := e.Submit(1, "d1", "p", []Item{{Drug: "D", Per: 2, PerDay: 1, Start: 1, End: 10}})
	if err != nil {
		t.Fatal(err)
	}
	// Stop at start: interval becomes empty, quota fully released.
	if err := e.Stop(1, "d1", id); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.RxRecord(id); r.Items[0].End != 1 {
		t.Fatalf("end=%d", r.Items[0].End)
	}
	if se := submitErr(e, 2, "d1", "p", Item{Drug: "D", Per: 2, PerDay: 1, Start: 2, End: 5}); se != nil {
		t.Fatalf("quota released after emptying stop: %+v", se)
	}
}
