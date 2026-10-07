package tzperm

import (
	"fmt"
	"testing"
)

// BenchmarkCheckVsVersionCount measures a single Check at several region
// version counts. Run:
//
//	go test -bench=BenchmarkCheckVsVersionCount -benchmem ./tzperm/
//
// ns/op must stay essentially flat across counts because lookup is
// O(log n); the audit counters (TestLookupCostNotLinear) provide the
// comparison-level proof.
func BenchmarkCheckVsVersionCount(b *testing.B) {
	entry := &ZoneRules{Name: "ENTRY", Transitions: []Transition{{At: -1 << 62, Offset: 0}}}
	for _, n := range []int{64, 4096, 262144} {
		b.Run(fmt.Sprintf("versions=%d", n), func(b *testing.B) {
			store := NewStore()
			versions := make([]ZoneVersion, n)
			for i := range versions {
				versions[i] = ZoneVersion{
					EffectiveFrom: int64(i * 10),
					Zone: &ZoneRules{
						Name:        fmt.Sprintf("Z%d", i),
						Transitions: []Transition{{At: -1 << 62, Offset: 0}},
					},
				}
			}
			store.UpsertRegion("R", versions)
			store.UpsertObjectType("T", []TypeVersion{{EffectiveFrom: -1 << 62}})
			store.UpsertWindowSet("W", []WindowVersion{{
				EffectiveFrom: -1 << 62, Rules: WindowRules{StartSec: 0, EndSec: 12 * 3600},
			}})
			store.BindPolicy("T", "A", "W")
			store.PutObject(Object{
				ID: "O", TypeID: "T", RegionID: "R",
				Attrs: map[string]*TimeAttribute{
					"A": {Name: "A", WallSec: int64(5 * 3600), EntryZone: entry},
				},
			})
			eng := NewEngine(store, nil)
			req := ViewRequest{ObjectID: "O", AttrName: "A", At: 5 * 3600, HasAt: true,
				Querier: &Querier{ID: "u"}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if out := eng.Check(req); out.Decision != DecisionAllow {
					b.Fatalf("unexpected %+v", out)
				}
			}
		})
	}
}
