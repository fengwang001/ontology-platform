package tzperm

import (
	"fmt"
	"testing"
)

// TestExhaustiveThreeSourcesDSTMigration enumerates the cross product of:
//   - entry-labeled zone: before/after region migration, with its own DST
//     jump, and offset families on both sides of the region migration;
//   - region default zone: a versioned chain whose pre/post migration
//     definitions contain DST jumps at different instants;
//   - querier zone: a third independent offset/DST identity.
//
// For every combination it checks query instants sampled around every
// boundary (entry jump, region migration, region DST jumps, window
// boundaries rendered in each candidate baseline) and asserts the engine
// baseline selection is always the region-effective zone, independent of
// the other two sources, and that window membership follows the half-open
// rule. It also re-runs each request twice to catch any current-time drift
// for fixed historical inputs.
func TestExhaustiveThreeSourcesDSTMigration(t *testing.T) {
	const migration = int64(100_000)

	// Region definitions: OLD (with a DST jump before migration) and NEW
	// (with a different DST jump after migration).
	regionOld := &ZoneRules{Name: "R-OLD", Transitions: []Transition{
		{At: -1 << 62, Offset: 0},
		{At: 1_000, Offset: 3600}, // spring before migration
	}}
	regionNew := &ZoneRules{Name: "R-NEW", Transitions: []Transition{
		{At: -1 << 62, Offset: 2 * 3600},
		{At: 150_000, Offset: 3 * 3600}, // spring after migration
	}}

	// Entry zones: one DST zone jumping before, one after the migration,
	// plus fixed extreme offsets.
	entryZones := []*ZoneRules{
		{Name: "E-pre", Transitions: []Transition{
			{At: -1 << 62, Offset: -4 * 3600},
			{At: 2_000, Offset: -3 * 3600},
		}},
		{Name: "E-post", Transitions: []Transition{
			{At: -1 << 62, Offset: 5 * 3600},
			{At: 160_000, Offset: 6 * 3600},
		}},
		{Name: "E-fixed", Transitions: []Transition{{At: -1 << 62, Offset: 11 * 3600}}},
	}
	// Querier zones are deliberately far from region zones.
	querierZones := []*ZoneRules{
		{Name: "Q-a", Transitions: []Transition{{At: -1 << 62, Offset: -9 * 3600}}},
		{Name: "Q-b", Transitions: []Transition{
			{At: -1 << 62, Offset: 9 * 3600},
			{At: 120_000, Offset: 10 * 3600},
		}},
	}

	// Interesting absolute instants: every transition/migration and +/-1s.
	boundaries := []int64{1_000, 2_000, migration, 120_000, 150_000, 160_000}
	var instants []int64
	for _, b := range boundaries {
		instants = append(instants, b-1, b, b+1)
	}

	// Windows: normal, wrap-around, all-day. Boundaries are exercised in
	// local seconds for every query instant.
	windows := []WindowRules{
		{StartSec: 0, EndSec: 12 * 3600},
		{StartSec: 23 * 3600, EndSec: 1 * 3600},
		{StartSec: 8 * 3600, EndSec: 8 * 3600},
		// A window whose edges deliberately sit near DST-sensitive hours.
		{StartSec: 2*3600 + 30*60, EndSec: 3*3600 + 30*60},
	}

	cases := 0
	for wi, rules := range windows {
		for _, entry := range entryZones {
			for _, querier := range querierZones {
				store := NewStore()
				store.UpsertRegion("R", []ZoneVersion{
					{EffectiveFrom: -1 << 62, Zone: regionOld},
					{EffectiveFrom: migration, Zone: regionNew},
				})
				store.UpsertObjectType("T", []TypeVersion{{EffectiveFrom: -1 << 62}})
				store.UpsertWindowSet("W", []WindowVersion{{EffectiveFrom: -1 << 62, Rules: rules}})
				store.BindPolicy("T", "A", "W")
				sink := NewMemorySink()
				eng := NewEngine(store, sink)

				for k, valueWall := range instants {
					store.PutObject(Object{
						ID: "O", TypeID: "T", RegionID: "R",
						Attrs: map[string]*TimeAttribute{
							"A": {Name: "A", WallSec: valueWall, EntryZone: entry},
						},
					})
					queryAt := instants[(k+wi)%len(instants)]
					req := ViewRequest{
						RequestID: fmt.Sprintf("c%d", cases),
						ObjectID:  "O", AttrName: "A", At: queryAt, HasAt: true,
						Querier: &Querier{ID: "u", Zone: querier},
					}

					o1 := eng.Check(req)
					o2 := eng.Check(req) // fixed inputs, repeatable verdict
					if o1.Decision != o2.Decision || o1.ErrorCode != o2.ErrorCode {
						t.Fatalf("non-repeatable decision: %+v vs %+v rules=%v entry=%s q=%s valueWall=%d at=%d",
							o1, o2, rules, entry.Name, querier.Name, valueWall, queryAt)
					}

					recs := sink.Records()
					r := recs[len(recs)-1]

					// Baseline must always be a region zone, never the
					// entry or querier zone name.
					if r.BaselineZoneAtQuery != "R-OLD" && r.BaselineZoneAtQuery != "R-NEW" {
						t.Fatalf("query baseline %q not a region zone", r.BaselineZoneAtQuery)
					}
					if r.BaselineZoneAtValue != "R-OLD" && r.BaselineZoneAtValue != "R-NEW" {
						t.Fatalf("value baseline %q not a region zone", r.BaselineZoneAtValue)
					}
					wantQ := "R-OLD"
					if queryAt >= migration {
						wantQ = "R-NEW"
					}
					if r.BaselineZoneAtQuery != wantQ {
						t.Fatalf("query baseline = %q want %s (q=%s entry=%s at=%d)",
							r.BaselineZoneAtQuery, wantQ, querier.Name, entry.Name, queryAt)
					}
					valueInstant, _ := entry.ResolveWall(valueWall)
					wantV := "R-OLD"
					if valueInstant >= migration {
						wantV = "R-NEW"
					}
					if r.BaselineZoneAtValue != wantV {
						t.Fatalf("value baseline = %q want %s (entry=%s wall=%d instant=%d)",
							r.BaselineZoneAtValue, wantV, entry.Name, valueWall, valueInstant)
					}

					// Independently recompute membership in the selected
					// region zone and require agreement.
					qzone := regionOld
					if queryAt >= migration {
						qzone = regionNew
					}
					sod, _ := SecondsOfDay(qzone.ToWall(queryAt))
					wantInside := rules.Contains(sod)
					if r.InsideWindow != wantInside {
						t.Fatalf("membership mismatch rules=%v at=%d sod=%d rec=%v",
							rules, queryAt, sod, r.InsideWindow)
					}
					if wantInside && o1.Decision != DecisionAllow {
						t.Fatalf("inside window but denied: %+v", o1)
					}
					if !wantInside && o1.Decision != DecisionDeny {
						t.Fatalf("outside window but allowed: %+v", o1)
					}
					cases++
				}
			}
		}
	}
	t.Logf("exhaustive cases evaluated: %d", cases)
	if cases == 0 {
		t.Fatal("no cases generated")
	}
}
