package ontology

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentOpsSerializable: incremental maintenance, timezone
// migrations and queries may be issued concurrently; the final observable
// state must equal the naive model replayed over the engine's authoritative
// serial log (i.e. some global serial order explains everything).
func TestConcurrentOpsSerializable(t *testing.T) {
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "Asia/Shanghai"))
	mustDo(t, e.DefineTimezone("B", "UTC"))

	const workers = 8
	const opsPerWorker = 150
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w*1000) + 1))
			for i := 0; i < opsPerWorker; i++ {
				switch rng.Intn(10) {
				case 0, 1: // migrate (best-effort; losers are rejected)
					typ := []string{"A", "B"}[rng.Intn(2)]
					zone := tzZones[rng.Intn(len(tzZones))]
					_ = e.MigrateTimezone(typ, zone, e.CurrentTZVersion(typ))
				case 2, 3: // query
					if _, _, err := e.Query("v"); err != nil {
						t.Errorf("query: %v", err)
						return
					}
				case 4: // sync only
					if _, err := e.Sync("v"); err != nil {
						t.Errorf("sync: %v", err)
						return
					}
				case 5, 6: // link
					a := fmt.Sprintf("a%d", rng.Intn(6))
					b := fmt.Sprintf("b%d", rng.Intn(6))
					l, err := e.Link("L", a, "A", b, "B")
					if err == nil {
						e.Dispatch(l)
					}
				default: // write + dispatch
					typ := []string{"A", "B"}[rng.Intn(2)]
					prop := map[string]string{"A": "ta", "B": "tb"}[typ]
					id := fmt.Sprintf("%s%d", map[string]string{"A": "a", "B": "b"}[typ], rng.Intn(6))
					wall := MustWall(fmt.Sprintf("2026-01-%02dT%02d:30:00", 14+rng.Intn(3), rng.Intn(24)))
					ev, err := e.Write(id, typ, prop, wall)
					if err == nil {
						e.Dispatch(ev)
					}
				}
			}
		}(w)
	}
	wg.Wait()

	groups, _, err := e.Query("v")
	mustDo(t, err)
	if diff := CompareGroups(e.NaiveRebuild("L"), groups); diff != "" {
		t.Fatalf("concurrent run not serializable-equivalent: %s", diff)
	}
	checkViewInvariants(t, e, "v")
}
