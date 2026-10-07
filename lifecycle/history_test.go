package lifecycle

import (
	"fmt"
	"testing"
	"time"
)

// TestStateAtMatchesActualLazySettlement: for many probe instants the
// history-derived state must equal the state obtained by a fresh lazy
// settlement at that same instant on an independently built engine.
func TestStateAtMatchesActualLazySettlement(t *testing.T) {
	eng, clock, _ := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "Doc",
		Transitions: []TransitionDef{
			{Name: "e1", From: "A", To: "B", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
			{Name: "e2", From: "B", To: "C", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
			{Name: "e3", From: "C", To: "D", Trigger: TriggerExpiry, Duration: 10 * time.Minute},
		},
	}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "A", t0))

	// Freeze ground truth at each probe minute by cloning the exact same
	// inputs into a fresh engine.
	for minute := 0; minute <= 40; minute++ {
		at := t0.Add(time.Duration(minute) * time.Minute)

		wantEng, wantClock, _ := testEngine(t)
		mustT(t, wantEng.RegisterType(eng.types["Doc"]))
		mustT(t, wantEng.RegisterInstance("d1", "Doc", "A", t0))
		wantClock.Set(at)
		want, err := wantEng.Get("d1")
		if err != nil {
			t.Fatal(err)
		}

		got, err := eng.StateAt("d1", at)
		if err != nil {
			t.Fatalf("StateAt at minute %d: %v", minute, err)
		}
		if got.State != want.State || !got.EnteredAt.Equal(want.EnteredAt) {
			t.Fatalf("minute %d: StateAt=%s@%s want %s@%s",
				minute, got.State, got.EnteredAt, want.State, want.EnteredAt)
		}
	}

	// Independently of how far the live engine has settled now, the past
	// answer must remain stable.
	clock.Set(t0.Add(40 * time.Minute))
	if snap, _ := eng.Get("d1"); snap.State != "D" {
		t.Fatalf("live state setup: %s", snap.State)
	}
	got, err := eng.StateAt("d1", t0.Add(25*time.Minute))
	if err != nil || got.State != "C" {
		t.Fatalf("past state after live catch-up: %s %v", got.State, err)
	}
}

// TestSettlementCostGrowsWithNewRings proves per-access cost scales with
// rings newly settled, not with historical ring count: repeated zero-ring
// accesses perform a constant number of due probes, and an access settling
// k new rings performs O(k) probes regardless of total history length.
func TestSettlementCostGrowsWithNewRings(t *testing.T) {
	eng, clock, logger := testEngine(t)
	trans := []TransitionDef{
		{Name: "e1", From: "S0", To: "S1", Trigger: TriggerExpiry, Duration: time.Minute},
	}
	for i := 2; i <= 60; i++ {
		trans = append(trans, TransitionDef{
			Name:     fmt.Sprintf("e%d", i),
			From:     fmt.Sprintf("S%d", i-1),
			To:       fmt.Sprintf("S%d", i),
			Trigger:  TriggerExpiry,
			Duration: time.Minute,
		})
	}
	mustT(t, eng.RegisterType(&ObjectType{Name: "Doc", Transitions: trans}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "S0", t0))

	// Settle 30 rings at once.
	clock.Advance(30 * time.Minute)
	if _, err := eng.Get("d1"); err != nil {
		t.Fatal(err)
	}

	// Steady-state reads perform zero due judgments and settle no rings,
	// independent of the 30 settled history rings.
	for i := 0; i < 5; i++ {
		_, _ = eng.Get("d1")
	}

	// At the settled instant the simulation finds nothing new to judge.
	checks, err := eng.ReplayDueChecksAt(clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if checks != 0 {
		t.Fatalf("steady-state due judgments = %d, want 0 (must not scan history)", checks)
	}

	// Cost proof independent of live history length. Build simulations at
	// instants in the future (events beyond "now" are not in history, so
	// these replays start from the 30 committed rings and lazily settle
	// only what is newly due). Settling k new rings performs exactly k due
	// judgments.
	cases := []struct {
		advance  time.Duration
		newRings int
	}{
		{31 * time.Minute, 1},
		{35 * time.Minute, 5},
		{40 * time.Minute, 10},
		{60 * time.Minute, 30},
	}
	for _, c := range cases {
		at := t0.Add(c.advance)
		got, err := eng.ReplayDueChecksAt(at)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.newRings {
			t.Fatalf("at %s due judgments=%d want %d (history already has 30 rings)",
				c.advance, got, c.newRings)
		}
	}

	// Live settlement of 10 new rings also fires exactly 10, confirming the
	// hook measures the same work the live engine performs.
	clock.Advance(10 * time.Minute)
	ringsBefore := len(logger.CallsCopy())
	_, _ = eng.Get("d1")
	var newRings int
	for _, c := range logger.CallsCopy()[ringsBefore:] {
		newRings += len(c.Rings)
	}
	if newRings != 10 {
		t.Fatalf("live settled %d new rings, want 10", newRings)
	}
}
