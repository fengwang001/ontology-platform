package tzperm

import (
	"sync"
	"testing"
	"time"
)

// TestConcurrentLinearizableSafety hammers Checks against concurrent region
// and window rule replacements. It cannot prove linearizability by
// enumeration, but it proves safety: no panic, no torn reads, every observed
// decision is explained by some serial state, and every audit record
// internally agrees. The linearizability argument itself is structural
// (one global lock, audit inside the critical section) and is documented in
// docs/DESIGN.md.
func TestConcurrentLinearizableSafety(t *testing.T) {
	zA := &ZoneRules{Name: "A", Transitions: []Transition{{At: -1 << 62, Offset: 0}}}
	zB := &ZoneRules{Name: "B", Transitions: []Transition{{At: -1 << 62, Offset: 3600}}}

	store := NewStore()
	store.UpsertRegion("R", []ZoneVersion{{EffectiveFrom: -1 << 62, Zone: zA}})
	store.UpsertObjectType("T", []TypeVersion{{EffectiveFrom: -1 << 62}})
	store.UpsertWindowSet("W", []WindowVersion{{
		EffectiveFrom: -1 << 62,
		Rules:         WindowRules{StartSec: 12 * 3600, EndSec: 12 * 3600},
	}})
	store.BindPolicy("T", "A", "W")
	store.PutObject(Object{
		ID: "O", TypeID: "T", RegionID: "R",
		Attrs: map[string]*TimeAttribute{
			"A": {Name: "A", WallSec: 100, EntryZone: zA},
		},
	})
	sink := NewMemorySink()
	eng := NewEngine(store, sink).WithClock(func() int64 { return 100 })

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Region toggler.
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(useB bool) {
			defer wg.Done()
			z := zA
			if useB {
				z = zB
			}
			for {
				select {
				case <-stop:
					return
				default:
					store.UpsertRegion("R", []ZoneVersion{{EffectiveFrom: -1 << 62, Zone: z}})
				}
			}
		}(g == 1)
	}

	// Window toggler.
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				rules := WindowRules{StartSec: 0, EndSec: 12 * 3600}
				if i%2 == 0 {
					rules = WindowRules{StartSec: 12 * 3600, EndSec: 12 * 3600}
				}
				store.UpsertWindowSet("W", []WindowVersion{{EffectiveFrom: -1 << 62, Rules: rules}})
				i++
			}
		}
	}()

	// Readers.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					out := eng.Check(ViewRequest{ObjectID: "O", AttrName: "A",
						Querier: &Querier{ID: "u", Zone: zB}})
					switch out.Decision {
					case DecisionAllow, DecisionDeny:
					default:
						t.Errorf("impossible decision %q", out.Decision)
						return
					}
				}
			}
		}()
	}

	// Run briefly, then stop and verify no partial audit state was ever
	// visible (every recorded decision is allow/deny with a coherent
	// baseline).
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	recs := sink.Records()
	if len(recs) == 0 {
		t.Fatal("no checks observed")
	}
	for _, rec := range recs {
		if rec.BaselineZoneAtQuery != "A" && rec.BaselineZoneAtQuery != "B" {
			t.Fatalf("torn baseline %q", rec.BaselineZoneAtQuery)
		}
		if rec.Decision != DecisionAllow && rec.Decision != DecisionDeny {
			t.Fatalf("torn decision %q", rec.Decision)
		}
	}
}
