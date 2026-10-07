package temporal

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// TestDifferentialAgainstNaive runs many random commit sequences (object and
// link types, property migrations, cardinality adjustments, creates,
// revocations, property updates, deletions) and checks at every instant that
// production snapshot answers and full traversals agree edge-by-edge with the
// independent log-scanning oracle.
func TestDifferentialAgainstNaive(t *testing.T) {
	const iterations = 40
	for seed := int64(1); seed <= iterations; seed++ {
		seed := seed
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			s := NewStore()
			naive := NewNaiveModel()

			// Stable initial schema.
			tx := s.Begin()
			tx.CreateObjectType("T", []Property{{Name: "v", Type: "int"}})
			tx.CreateLinkType("e", Cardinality{MaxOut: -1})
			mustCommit(t, tx)

			var liveObjs []ObjectID
			nextObj := 0
			head := Instant(1)

			migrated := false
			for step := 0; step < 60; step++ {
				tx = s.Begin()
				acted := false

				switch rng.Intn(10) {
				case 0, 1: // create object + maybe links
					id := ObjectID("o" + itoa(nextObj))
					nextObj++
					tx.CreateObject(id, "T", PropertyValues{"v": rng.Intn(100)})
					liveObjs = append(liveObjs, id)
					acted = true
					// Maybe add up to 2 links from existing objects.
					for k := 0; k < 2 && len(liveObjs) > 1; k++ {
						src := liveObjs[rng.Intn(len(liveObjs))]
						dst := liveObjs[rng.Intn(len(liveObjs))]
						if src == dst {
							continue
						}
						if err := tx.CreateLink("e", src, dst); err != nil {
							// Duplicate/illegal staging: roll the whole tx
							// back and retry nothing this step.
							tx.Rollback()
							acted = false
							break
						}
					}
				case 2: // update properties on a live object
					if len(liveObjs) > 0 {
						id := liveObjs[rng.Intn(len(liveObjs))]
						tx.SetProperties(id, PropertyValues{"v": rng.Intn(1000)})
						acted = true
					}
				case 3: // revoke a random link by scanning current state
					if e := randomExistingEdge(t, s, head, rng); e != nil {
						tx.RevokeLink(e.linkType, e.src, e.dst)
						acted = true
					}
				case 4: // delete a random leaf-ish object (oracle handles cascade)
					if len(liveObjs) > 1 {
						idx := rng.Intn(len(liveObjs))
						id := liveObjs[idx]
						tx.DeleteObject(id)
						liveObjs = append(liveObjs[:idx], liveObjs[idx+1:]...)
						acted = true
					}
				case 5: // property migration after some history exists
					if step > 5 && !migrated && rng.Intn(2) == 0 {
						tx.MigrateObjectType("T", []Property{
							{Name: "v", Type: "int"},
							{Name: "tag", Type: "string"},
						})
						migrated = true
						acted = true
					}
				case 6: // cardinality adjustment (unbounded -> various)
					tx.AdjustCardinality("e", Cardinality{MaxOut: []int{-1, 1, 2, 3, 5}[rng.Intn(5)]})
					acted = true
				default:
					// no-op step: commit an empty transaction is allowed and
					// advances the clock, which also tests sparse history.
					acted = true
				}

				if acted {
					at, err := tx.Commit()
					if err != nil {
						// Cardinality rejection is a legitimate outcome; the
						// oracle must simply not ingest that commit.
						if _, ok := err.(*StagingError); !ok {
							t.Fatalf("seed=%d step=%d unexpected commit error: %v", seed, step, err)
						}
					} else {
						head = at
					}
				}

				// Refresh the oracle from the canonical log and compare at a
				// random already-committed instant.
				naive.IngestLog(s.LogEntries())
				at := Instant(1 + rng.Int63n(int64(head)))

				// Compare a random object state.
				if len(liveObjs) > 0 {
					id := liveObjs[rng.Intn(len(liveObjs))]
					sn, err := s.Snapshot(at)
					if err != nil {
						t.Fatalf("snapshot: %v", err)
					}
					got, err := sn.Object(id)
					if err != nil {
						t.Fatalf("object: %v", err)
					}
					want := naive.NaiveObject(at, id)
					if !reflect.DeepEqual(got.Properties, want.Properties) || got.Exists != want.Exists || got.Type != want.Type {
						t.Fatalf("seed=%d object %s @%d mismatch:\n got=%+v\nwant=%+v", seed, id, at, got, want)
					}
				}

				// Compare full traversals from a random live object against
				// the naive BFS, object sets, link sets and depths.
				if len(liveObjs) > 0 {
					start := liveObjs[rng.Intn(len(liveObjs))]
					cfg := TraversalConfig{Start: start, At: at,
						Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}
					got, gerr := s.Traverse(cfg, nil)
					want := naive.NaiveTraverse(cfg)
					if (gerr != nil) != (want == nil) {
						t.Fatalf("seed=%d traverse existence mismatch: err=%v want-nil=%v", seed, gerr, want == nil)
					}
					if gerr == nil {
						compareResults(t, seed, at, got, want)
					}
				}
			}
		})
	}
}

func compareResults(t *testing.T, seed int64, at Instant, got, want *TraversalResult) {
	t.Helper()
	gotObjs := objectSummary(got.Objects)
	wantObjs := objectSummary(want.Objects)
	sort.Strings(gotObjs)
	sort.Strings(wantObjs)
	if !reflect.DeepEqual(gotObjs, wantObjs) {
		t.Fatalf("seed=%d @%d object sets differ:\n got=%v\nwant=%v", seed, at, gotObjs, wantObjs)
	}
	gotLinks := linkSummary(got.Links)
	wantLinks := linkSummary(want.Links)
	sort.Strings(gotLinks)
	sort.Strings(wantLinks)
	if !reflect.DeepEqual(gotLinks, wantLinks) {
		t.Fatalf("seed=%d @%d link sets differ:\n got=%v\nwant=%v", seed, at, gotLinks, wantLinks)
	}
	// Depths must agree per object.
	gd := map[ObjectID]int{}
	for _, o := range got.Objects {
		gd[o.State.ID] = o.Depth
	}
	wd := map[ObjectID]int{}
	for _, o := range want.Objects {
		wd[o.State.ID] = o.Depth
	}
	if !reflect.DeepEqual(gd, wd) {
		t.Fatalf("seed=%d @%d depths differ: got=%v want=%v", seed, at, gd, wd)
	}
}

func objectSummary(os []VisitedObject) []string {
	out := make([]string, len(os))
	for i, o := range os {
		out[i] = string(o.State.ID) + ":" + string(o.State.Type)
	}
	return out
}

func linkSummary(ls []Link) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = string(l.Type) + ":" + string(l.Src) + "->" + string(l.Dst)
	}
	return out
}

func randomExistingEdge(t *testing.T, s *Store, at Instant, rng *rand.Rand) *edgeKey {
	t.Helper()
	sn, err := s.Snapshot(at)
	if err != nil {
		return nil
	}
	var edges []edgeKey
	sn.allEdgesAt(func(k edgeKey) {
		if rng.Intn(3) == 0 {
			edges = append(edges, k)
		}
	})
	if len(edges) == 0 {
		return nil
	}
	return &edges[rng.Intn(len(edges))]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
