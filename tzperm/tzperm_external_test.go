package tzperm_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/tzperm"
	"ontology/tzperm/tznaive"
)

type naiveRef struct{}

func (naiveRef) Decide(snap tzperm.Snapshot, req tzperm.ViewRequest) (tzperm.Decision, string, int) {
	return tznaive.Decide(snap, req)
}

func TestEndToEndComparisonCounts(t *testing.T) {
	const n = 4096
	store := tzperm.NewStore()
	versions := make([]tzperm.ZoneVersion, n)
	for i := range versions {
		versions[i] = tzperm.ZoneVersion{
			EffectiveFrom: int64(i * 10),
			Zone: &tzperm.ZoneRules{
				Name:        fmt.Sprintf("Z%d", i),
				Transitions: []tzperm.Transition{{At: -1 << 62, Offset: 0}},
			},
		}
	}
	store.UpsertRegion("R", versions)
	store.UpsertObjectType("T", []tzperm.TypeVersion{{EffectiveFrom: -1 << 62}})
	store.UpsertWindowSet("W", []tzperm.WindowVersion{{
		EffectiveFrom: -1 << 62, Rules: tzperm.WindowRules{StartSec: 0, EndSec: 12 * 3600},
	}})
	store.BindPolicy("T", "A", "W")
	store.PutObject(tzperm.Object{
		ID: "O", TypeID: "T", RegionID: "R",
		Attrs: map[string]*tzperm.TimeAttribute{
			"A": {Name: "A", WallSec: (n - 1) * 10, EntryZone: versions[n-1].Zone},
		},
	})
	sink := tzperm.NewMemorySink()
	eng := tzperm.NewEngine(store, sink).WithReference(naiveRef{})

	at := int64(5 * 3600) // local 05:00 under zero-offset region zones
	out := eng.Check(tzperm.ViewRequest{ObjectID: "O", AttrName: "A", At: at, HasAt: true,
		Querier: &tzperm.Querier{ID: "u"}})
	if out.Decision != tzperm.DecisionAllow {
		t.Fatalf("out=%+v", out)
	}
	recs := sink.Records()
	rec := recs[0]
	if !rec.ReferenceConsistent {
		t.Fatal("naive reference disagrees")
	}
	if rec.EnginePathComparisons >= rec.NaiveComparisons {
		t.Fatalf("engine comparisons %d not below naive %d",
			rec.EnginePathComparisons, rec.NaiveComparisons)
	}
	if rec.EnginePathComparisons > 60 {
		t.Fatalf("engine path unexpectedly large: %d", rec.EnginePathComparisons)
	}
	if rec.NaiveComparisons < 2*n-30 {
		t.Fatalf("naive comparisons %d not linear", rec.NaiveComparisons)
	}
}

// TestRandomizedAgainstNaive runs random interleaved operations and checks
// every decision against the independent linear-scan model.
func TestRandomizedAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))

	// Fixed pool of zones: several fixed-offset zones and two DST zones
	// whose jumps bracket the region migration instants.
	zones := []*tzperm.ZoneRules{
		{Name: "z0", Transitions: []tzperm.Transition{{At: -1 << 62, Offset: 0}}},
		{Name: "z1", Transitions: []tzperm.Transition{{At: -1 << 62, Offset: 3600}}},
		{Name: "zm5", Transitions: []tzperm.Transition{{At: -1 << 62, Offset: -5 * 3600}}},
		{Name: "z9", Transitions: []tzperm.Transition{{At: -1 << 62, Offset: 9 * 3600}}},
		{Name: "dstA", Transitions: []tzperm.Transition{
			{At: -1 << 62, Offset: 0},
			{At: 1_000, Offset: 3600},
			{At: 500_000, Offset: 0},
		}},
		{Name: "dstB", Transitions: []tzperm.Transition{
			{At: -1 << 62, Offset: -2 * 3600},
			{At: 2_000, Offset: -1 * 3600},
			{At: 600_000, Offset: -2 * 3600},
		}},
	}

	store := tzperm.NewStore()
	// Region with several version migrations, some reusing DST zones.
	regionVers := []tzperm.ZoneVersion{
		{EffectiveFrom: -1 << 62, Zone: zones[0]},
		{EffectiveFrom: 100, Zone: zones[4]},
		{EffectiveFrom: 200_000, Zone: zones[1]},
		{EffectiveFrom: 700_000, Zone: zones[5]},
	}
	store.UpsertRegion("R", regionVers)
	store.UpsertObjectType("T", []tzperm.TypeVersion{{EffectiveFrom: -1 << 62}})

	mutateWindow := func(start, end int, from int64) {
		store.UpsertWindowSet("W", []tzperm.WindowVersion{{
			EffectiveFrom: from,
			Rules:         tzperm.WindowRules{StartSec: start, EndSec: end},
		}})
	}
	mutateWindow(8*3600, 18*3600, -1<<62)
	store.BindPolicy("T", "A", "W")

	sink := tzperm.NewMemorySink()
	eng := tzperm.NewEngine(store, sink).WithReference(naiveRef{})

	queryInstants := []int64{0, 50, 99, 100, 999, 1000, 1001, 50_000,
		199_999, 200_000, 200_001, 499_999, 500_000, 500_001,
		699_999, 700_000, 700_001, 2_000_000}

	for iter := 0; iter < 3000; iter++ {
		// Occasionally change the window rule to wrap midnight or become
		// invalid, and occasionally migrate the region mid-sequence, all
		// while issuing view requests.
		switch rng.Intn(7) {
		case 0:
			mutateWindow(22*3600, 6*3600, rng.Int63n(900_000)-100_000)
		case 1:
			mutateWindow(8*3600, 18*3600, -1<<62)
		case 2:
			mutateWindow(12*3600, 12*3600, -1<<62)
		case 3:
			mutateWindow(-1, 10, -1<<62) // invalid
		case 4:
			store.UpsertObjectType("T", []tzperm.TypeVersion{
				{EffectiveFrom: -1 << 62},
				{EffectiveFrom: 1 + rng.Int63n(899_998), AttrsDeprecated: map[string]bool{"A": true}},
			})
		case 5:
			store.UpsertObjectType("T", []tzperm.TypeVersion{{EffectiveFrom: -1 << 62}})
		}

		entry := zones[rng.Intn(len(zones))]
		wall := rng.Int63n(1_500_000) - 200_000
		store.PutObject(tzperm.Object{
			ID: "O", TypeID: "T", RegionID: "R",
			Attrs: map[string]*tzperm.TimeAttribute{
				"A": {Name: "A", WallSec: wall, EntryZone: entry},
			},
		})

		at := queryInstants[rng.Intn(len(queryInstants))]
		req := tzperm.ViewRequest{
			RequestID: fmt.Sprintf("it-%d", iter),
			ObjectID:  "O", AttrName: "A", At: at, HasAt: true,
		}
		if rng.Intn(5) != 0 {
			req.Querier = &tzperm.Querier{ID: "u", Zone: zones[rng.Intn(len(zones))]}
		}

		out := eng.Check(req)
		rec := sink.Records()[len(sink.Records())-1]
		if !rec.ReferenceConsistent {
			d, c, comps := tznaive.Decide(store.Snapshot(), req)
			t.Logf("types=%+v windows=%+v regions_at=%v",
				store.Snapshot().Types["T"], store.Snapshot().WindowSets["W"], rec.BaselineZoneAtValue)
			t.Fatalf("iter %d divergence: engine=(%s,%s) naive=(%s,%s) naiveComps=%d req=%+v",
				iter, out.Decision, out.ErrorCode, d, c, comps, req)
		}
	}
}
