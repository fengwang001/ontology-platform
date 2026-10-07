package ontology

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// verdict records one cross-check between the incremental view and the
// naive full-rebuild model: the inputs (operation prefix), and the
// conclusion. Verdicts are persisted to a JSONL file for after-the-fact
// inspection.
type verdict struct {
	Seed       int64  `json:"seed"`
	Op         int    `json:"op"`
	OpDesc     string `json:"opDesc"`
	Match      bool   `json:"match"`
	ViewItems  int    `json:"viewItems"`
	ModelItems int    `json:"modelItems"`
	Diff       string `json:"diff,omitempty"`
}

var tzZones = []string{
	"UTC", "Asia/Shanghai", "Europe/Berlin", "America/New_York",
	"Australia/Sydney", "Pacific/Kiritimati", "Pacific/Auckland",
}

// TestRandomOpsAgainstNaiveModel drives random operation sequences (writes,
// rewrites, pinned writes, links, unlinks, migrations, deprecations, and
// randomly delayed event delivery) and cross-checks the incremental view
// against the naive full-rebuild model after every step.
func TestRandomOpsAgainstNaiveModel(t *testing.T) {
	seeds := []int64{1, 2, 3, 7, 42, 99, 2026, 31337}
	// Verdicts go to a stable location so they survive the test run and can
	// be audited afterwards; override with ONTOLOGY_VERDICT_LOG.
	verdictPath := os.Getenv("ONTOLOGY_VERDICT_LOG")
	if verdictPath == "" {
		verdictPath = filepath.Join(os.TempDir(), "ontology_verdicts.jsonl")
	}
	vf, err := os.Create(verdictPath)
	mustDo(t, err)
	defer vf.Close()
	enc := json.NewEncoder(vf)
	total := 0
	for _, seed := range seeds {
		runRandomSequence(t, seed, 400, enc)
		total += 400
	}
	t.Logf("recorded %d verdicts to %s", total, verdictPath)
}

func runRandomSequence(t *testing.T, seed int64, steps int, enc *json.Encoder) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	e := setupEnv(t)
	mustDo(t, e.DefineTimezone("A", "Asia/Shanghai"))
	mustDo(t, e.DefineTimezone("B", "UTC"))
	aIDs := []string{"a0", "a1", "a2", "a3", "a4", "a5"}
	bIDs := []string{"b0", "b1", "b2", "b3", "b4", "b5"}
	live := make(map[[2]string]bool)
	deprecated := make(map[string]bool) // type -> grouping prop deprecated
	var pending []Event                 // held back to arrive late
	dispatch := func(evs ...Event) {
		for _, ev := range evs {
			if rng.Intn(100) < 30 {
				pending = append(pending, ev) // delay delivery
			} else {
				e.Dispatch(ev)
			}
		}
		// occasionally flush some held-back events, in random order
		for len(pending) > 0 && rng.Intn(100) < 40 {
			i := rng.Intn(len(pending))
			e.Dispatch(pending[i])
			pending = append(pending[:i], pending[i+1:]...)
		}
	}
	wall := func() WallClock {
		return MustWall(fmt.Sprintf("2026-01-%02dT%02d:%02d:00",
			14+rng.Intn(3), rng.Intn(24), []int{0, 29, 30, 59}[rng.Intn(4)]))
	}
	check := func(step int, desc string) {
		t.Helper()
		groups, _, err := e.Query("v")
		mustDo(t, err)
		model := e.NaiveRebuild("L")
		diff := CompareGroups(model, groups)
		v := verdict{
			Seed:       seed,
			Op:         step,
			OpDesc:     desc,
			Match:      diff == "",
			ViewItems:  countItems(groups),
			ModelItems: countItems(model),
			Diff:       diff,
		}
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
		if diff != "" {
			t.Fatalf("seed %d step %d (%s): %s\nview=%v\nmodel=%v",
				seed, step, desc, diff, flatten(groups), flatten(model))
		}
		checkViewInvariants(t, e, "v")
	}
	for step := 0; step < steps; step++ {
		var desc string
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4: // migrate a type's timezone (valid)
			typ := []string{"A", "B"}[rng.Intn(2)]
			zone := tzZones[rng.Intn(len(tzZones))]
			err := e.MigrateTimezone(typ, zone, e.CurrentTZVersion(typ))
			desc = fmt.Sprintf("migrate %s -> %s", typ, zone)
			if err != nil {
				t.Fatalf("seed %d step %d: %s: %v", seed, step, desc, err)
			}
		case 5: // invalid migration (rejected, consumes no version)
			typ := []string{"A", "B"}[rng.Intn(2)]
			_ = e.MigrateTimezone(typ, tzZones[rng.Intn(len(tzZones))], rng.Intn(4))
			desc = "invalid migration attempt"
		case 6, 7: // pinned write, possibly to a nonexistent version
			typ := []string{"A", "B"}[rng.Intn(2)]
			prop := map[string]string{"A": "ta", "B": "tb"}[typ]
			ids := aIDs
			if typ == "B" {
				ids = bIDs
			}
			if deprecated[typ] {
				desc = "pinned write skipped (deprecated)"
				break
			}
			ev, err := e.WritePinned(ids[rng.Intn(len(ids))], typ, prop, wall(), rng.Intn(4))
			if err != nil {
				t.Fatalf("seed %d step %d: %v", seed, step, err)
			}
			dispatch(ev)
			desc = "pinned write"
		case 8: // deprecate a grouping property (once per type)
			typ := []string{"A", "B"}[rng.Intn(2)]
			prop := map[string]string{"A": "ta", "B": "tb"}[typ]
			if !deprecated[typ] {
				if err := e.DeprecateProperty(typ, prop); err != nil {
					t.Fatalf("seed %d step %d: %v", seed, step, err)
				}
				deprecated[typ] = true
			}
			desc = "deprecate " + typ
		case 9, 10, 11: // unlink a live pair
			for pair := range live {
				u, err := e.Unlink("L", pair[0], "A", pair[1], "B")
				mustDo(t, err)
				delete(live, pair)
				dispatch(u)
				break
			}
			desc = "unlink"
		case 12, 13, 14, 15, 16, 17: // link a fresh pair
			a, b := aIDs[rng.Intn(len(aIDs))], bIDs[rng.Intn(len(bIDs))]
			pair := [2]string{a, b}
			if !live[pair] {
				l, err := e.Link("L", a, "A", b, "B")
				mustDo(t, err)
				live[pair] = true
				dispatch(l)
			}
			desc = "link " + a + "-" + b
		default: // plain write (new or rewrite)
			typ := []string{"A", "B"}[rng.Intn(2)]
			prop := map[string]string{"A": "ta", "B": "tb"}[typ]
			ids := aIDs
			if typ == "B" {
				ids = bIDs
			}
			if deprecated[typ] {
				desc = "write skipped (deprecated)"
				break
			}
			ev, err := e.Write(ids[rng.Intn(len(ids))], typ, prop, wall())
			if err != nil {
				t.Fatalf("seed %d step %d: %v", seed, step, err)
			}
			dispatch(ev)
			desc = "write " + typ
		}
		check(step, desc)
	}
	// flush everything still held back and do a final check
	for _, ev := range pending {
		e.Dispatch(ev)
	}
	check(steps, "final flush")
}

func countItems(groups []Group) int {
	n := 0
	for _, g := range groups {
		n += len(g.Items)
	}
	return n
}
