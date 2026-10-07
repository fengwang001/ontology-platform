package tzperm

import "testing"

// fixture builds a small world with two zone identities:
//
//	oldZone: fixed offset +0 for all t
//	newZone: fixed offset +1 for all t
//
// plus one DST zone dstZone whose offset jumps +1 at dstJump.
func fixture(t *testing.T) (*Store, *Engine, *MemorySink, map[string]*ZoneRules) {
	t.Helper()
	old := &ZoneRules{Name: "OLD", Transitions: []Transition{{At: -1 << 62, Offset: 0}}}
	neu := &ZoneRules{Name: "NEW", Transitions: []Transition{{At: -1 << 62, Offset: 3600}}}
	qz := &ZoneRules{Name: "QZ", Transitions: []Transition{{At: -1 << 62, Offset: 9 * 3600}}}

	const migrate = int64(100_000)
	store := NewStore()
	store.UpsertRegion("R1", []ZoneVersion{
		{EffectiveFrom: -1 << 62, Zone: old},
		{EffectiveFrom: migrate, Zone: neu},
	})
	store.UpsertObjectType("T1", []TypeVersion{
		{EffectiveFrom: -1 << 62},
	})
	sink := NewMemorySink()
	eng := NewEngine(store, sink)
	return store, eng, sink, map[string]*ZoneRules{
		"old": old, "new": neu, "qz": qz, "migrate": nil,
	}
}

const migrateInstant = int64(100_000)

func putSimpleObject(store *Store, zones map[string]*ZoneRules, wall int64, entry *ZoneRules) {
	store.PutObject(Object{
		ID:       "O1",
		TypeID:   "T1",
		RegionID: "R1",
		Attrs: map[string]*TimeAttribute{
			"A1": {Name: "A1", WallSec: wall, EntryZone: entry},
		},
	})
}

func TestBaselineIsRegionZoneNotQuerierOrEntry(t *testing.T) {
	store, eng, sink, zones := fixture(t)
	// Value entered while OLD was effective, using NEW as the entry-labeled
	// zone. Querier sits in QZ (+9). The value instant is 50_000-3600,
	// which is before the migration, so the value baseline must be OLD.
	putSimpleObject(store, zones, 50_000, zones["new"])
	store.BindPolicy("T1", "A1", "W1")
	// At query 50_000 UTC, region baseline OLD (offset 0) shows local
	// 13:53 -> outside [22:00,23:00) -> deny; the querier zone QZ (+9)
	// would show 22:53 -> inside -> allow. Denial proves the querier zone
	// is not the baseline. The entry zone NEW (+1) would shift the value
	// instant, but never drives window evaluation either.
	store.UpsertWindowSet("W1", []WindowVersion{
		{EffectiveFrom: -1 << 62, Rules: WindowRules{StartSec: 22 * 3600, EndSec: 23 * 3600}},
	})

	out := eng.Check(ViewRequest{
		RequestID: "r1", ObjectID: "O1", AttrName: "A1", At: 50_000, HasAt: true,
		Querier: &Querier{ID: "u1", Zone: zones["qz"]},
	})
	if out.Decision != DecisionDeny {
		t.Fatalf("decision = %v, want DENY under region baseline", out.Decision)
	}
	rec := sink.Records()[0]
	if rec.BaselineZoneAtQuery != "OLD" || rec.BaselineZoneAtValue != "OLD" {
		t.Fatalf("baselines = %q/%q, want OLD/OLD", rec.BaselineZoneAtValue, rec.BaselineZoneAtQuery)
	}
}

func TestHistoricalValueUsesBaselineAtValueInstant(t *testing.T) {
	store, eng, sink, zones := fixture(t)
	// Entry zone OLD, wall reading 20_000 -> instant 20_000 (pre-migration).
	putSimpleObject(store, zones, 20_000, zones["old"])
	store.BindPolicy("T1", "A1", "W1")
	// Window covers local hour 02 only under whichever baseline is chosen.
	// Pre-migration baseline OLD maps query-anchored checks consistently.
	store.UpsertWindowSet("W1", []WindowVersion{
		{EffectiveFrom: -1 << 62, Rules: WindowRules{StartSec: 2 * 3600, EndSec: 3 * 3600}},
	})

	// Evaluate the same request at two different "now" points, both after
	// the migration. Value baseline must be OLD both times.
	for _, now := range []int64{migrateInstant + 10, migrateInstant + 10_000_000} {
		out := eng.Check(ViewRequest{
			ObjectID: "O1", AttrName: "A1", At: now, HasAt: true,
			Querier: &Querier{ID: "u1", Zone: zones["qz"]},
		})
		_ = out
	}
	recs := sink.Records()
	for _, r := range recs {
		if r.BaselineZoneAtValue != "OLD" {
			t.Fatalf("value baseline drifted to %q at now=%d", r.BaselineZoneAtValue, r.At)
		}
	}
}

func TestWindowBoundaryHalfOpenAcrossDSTDay(t *testing.T) {
	// Region zone DST: offset 0 before, +3600 after. The jump is at UTC
	// 02:00 on day 2, i.e. local clocks spring 02:00 -> 03:00. Window
	// [03:30,04:30) local, fully after the jump, still exercises the same
	// half-open rule on the 23-hour transition day.
	jump := int64(86_400 + 2*3600)
	dst := &ZoneRules{Name: "DST", Transitions: []Transition{
		{At: -1 << 62, Offset: 0},
		{At: jump, Offset: 3600},
	}}
	store := NewStore()
	store.UpsertRegion("R", []ZoneVersion{{EffectiveFrom: -1 << 62, Zone: dst}})
	store.UpsertObjectType("T", []TypeVersion{{EffectiveFrom: -1 << 62}})
	store.UpsertWindowSet("W", []WindowVersion{{
		EffectiveFrom: -1 << 62,
		Rules:         WindowRules{StartSec: 3*3600 + 30*60, EndSec: 4*3600 + 30*60},
	}})
	store.BindPolicy("T", "A", "W")
	store.PutObject(Object{
		ID: "O", TypeID: "T", RegionID: "R",
		Attrs: map[string]*TimeAttribute{"A": {Name: "A", WallSec: 0, EntryZone: dst}},
	})
	sink := NewMemorySink()
	eng := NewEngine(store, sink)

	// Local wall readings after the jump are UTC+1.
	startInstant, _ := dst.ResolveWall(jump + 3600 + 30*60)
	endInstant, _ := dst.ResolveWall(jump + 3600 + 90*60)

	check := func(at int64, want Decision) {
		t.Helper()
		out := eng.Check(ViewRequest{ObjectID: "O", AttrName: "A", At: at, HasAt: true,
			Querier: &Querier{ID: "u"}})
		if out.Decision != want {
			t.Fatalf("at=%d decision=%v want %v", at, out.Decision, want)
		}
	}
	check(startInstant, DecisionAllow) // start boundary included
	check(endInstant, DecisionDeny)    // end boundary excluded
}

func TestErrorPriorityReportsOneCode(t *testing.T) {
	// Build a request that simultaneously hits: deprecation, unknown region
	// baseline, invalid window and missing querier. Expect only the
	// highest-priority deprecation code.
	store := NewStore()
	store.UpsertObjectType("T", []TypeVersion{
		{EffectiveFrom: -1 << 62, AttrsDeprecated: map[string]bool{"A": true}},
	})
	// No region defined for "R" -> REGION_ZONE_UNDEFINED.
	store.UpsertWindowSet("W", []WindowVersion{{
		EffectiveFrom: -1 << 62,
		Rules:         WindowRules{StartSec: -1, EndSec: 2}, // invalid
	}})
	store.BindPolicy("T", "A", "W")
	store.PutObject(Object{
		ID: "O", TypeID: "T", RegionID: "R",
		Attrs: map[string]*TimeAttribute{"A": {Name: "A", WallSec: 100,
			EntryZone: &ZoneRules{Name: "E", Transitions: []Transition{{At: -1 << 62, Offset: 0}}}}},
	})
	eng := NewEngine(store, nil)
	out := eng.Check(ViewRequest{ObjectID: "O", AttrName: "A", At: 200, HasAt: true})
	if out.ErrorCode != CodeAttrDeprecated || out.Decision != DecisionDeny {
		t.Fatalf("out = %+v", out)
	}

	// Remove deprecation: next priority must be the region error.
	store.UpsertObjectType("T", []TypeVersion{{EffectiveFrom: -1 << 62}})
	out = eng.Check(ViewRequest{ObjectID: "O", AttrName: "A", At: 200, HasAt: true})
	if out.ErrorCode != CodeRegionZoneUnknown {
		t.Fatalf("got code %q", out.ErrorCode)
	}

	// Add a region: next priority must be invalid window.
	store.UpsertRegion("R", []ZoneVersion{{EffectiveFrom: -1 << 62,
		Zone: &ZoneRules{Name: "Z", Transitions: []Transition{{At: -1 << 62, Offset: 0}}}}})
	out = eng.Check(ViewRequest{ObjectID: "O", AttrName: "A", At: 200, HasAt: true})
	if out.ErrorCode != CodeWindowInvalid {
		t.Fatalf("got code %q", out.ErrorCode)
	}

	// Fix the window: missing querier is all that remains.
	store.UpsertWindowSet("W", []WindowVersion{{
		EffectiveFrom: -1 << 62, Rules: WindowRules{StartSec: 0, EndSec: 1},
	}})
	out = eng.Check(ViewRequest{ObjectID: "O", AttrName: "A", At: 200, HasAt: true})
	if out.ErrorCode != CodeQuerierMissing {
		t.Fatalf("got code %q", out.ErrorCode)
	}
}

func TestOutcomeLeakFree(t *testing.T) {
	store, eng, _, zones := fixture(t)
	putSimpleObject(store, zones, 20_000, zones["old"])
	store.BindPolicy("T1", "A1", "W1")
	store.UpsertWindowSet("W1", []WindowVersion{{
		EffectiveFrom: -1 << 62,
		Rules:         WindowRules{StartSec: 4 * 3600, EndSec: 5 * 3600},
	}})
	deny := eng.Check(ViewRequest{RequestID: "secret-req", ObjectID: "O1", AttrName: "A1", At: 20_000, HasAt: true,
		Querier: &Querier{ID: "u", Zone: zones["qz"]}})
	if deny.Decision != DecisionDeny {
		t.Fatalf("want deny, got %v", deny)
	}
	s := structFields(deny)
	for _, forbidden := range []string{"OLD", "NEW", "QZ", "20000", "14400", "18000"} {
		if contains(s, forbidden) {
			t.Fatalf("outcome leaks %q: %+v", forbidden, deny)
		}
	}

	// Error outcomes leak neither value nor zone.
	err := eng.Check(ViewRequest{ObjectID: "O1", AttrName: "A1", At: 20_000, HasAt: true})
	if err.ErrorCode != CodeQuerierMissing {
		t.Fatalf("got %+v", err)
	}
	s = structFields(err)
	for _, forbidden := range []string{"OLD", "NEW", "20000"} {
		if contains(s, forbidden) {
			t.Fatalf("error outcome leaks %q: %+v", forbidden, err)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
