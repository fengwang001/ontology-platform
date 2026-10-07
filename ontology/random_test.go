package ontology

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// extractionRecord is one logged differential-test extraction: its inputs,
// both implementations' outputs, and the dangling attachment records.
type extractionRecord struct {
	Seed            int64          `json:"seed"`
	Step            int            `json:"step"`
	Caller          Principal      `json:"caller"`
	Scope           []ObjectID     `json:"scope"`
	Epoch           uint64         `json:"epoch"`
	Objects         []ObjectID     `json:"objects"`
	Links           []Link         `json:"links"`
	Dangling        []DanglingLink `json:"dangling"`
	CandidateLinks  int            `json:"candidate_links_examined"`
	PermissionCheck int            `json:"permission_checks"`
	OracleObjects   []ObjectID     `json:"oracle_objects"`
	OracleLinks     []Link         `json:"oracle_links"`
	OracleDangling  []DanglingLink `json:"oracle_dangling"`
	Match           bool           `json:"match"`
}

func TestRandomDifferentialAgainstNaiveOracle(t *testing.T) {
	// Persist the record under testdata/ so every extraction's inputs, outputs
	// and dangling attachment remain inspectable after the test run.
	dir := filepath.Join("..", "testdata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	logPath := filepath.Join(dir, "extraction-log.jsonl")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer logFile.Close()
	t.Logf("extraction log: %s", logPath)

	enc := json.NewEncoder(logFile)
	totalExtractions := 0

	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		g := newTestGraph(t)
		n := newNaiveGraph()
		n.addObjectType(tNode)
		n.addLinkType(LinkType{Name: lEdge, Source: tNode, Sink: tNode, Direct: Directed})
		n.addLinkType(LinkType{Name: lPair, Source: tNode, Sink: tNode, Direct: Undirected})

		callers := []Principal{alice, bob, Principal("carol")}

		// Object ids the generator knows about (alive or dead), for realistic
		// scopes that include stale and non-existent ids.
		var known []ObjectID

		for step := 0; step < 120; step++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3: // add object
				id := ObjectID(fmt.Sprintf("o%d", rng.Intn(60)))
				if _, exists := n.objects[id]; !exists {
					// random non-empty reader subset among callers
					var readers []Principal
					for _, c := range callers {
						if rng.Intn(2) == 0 {
							readers = append(readers, c)
						}
					}
					gErr := g.AddObject(Object{ID: id, Type: tNode, Readers: readers})
					nOk := n.addObject(id, tNode, readers)
					if (gErr == nil) != nOk {
						t.Fatalf("seed %d step %d addObject divergence: g=%v n=%v", seed, step, gErr, nOk)
					}
					if gErr == nil {
						known = appendUniqueID(known, id)
					}
				}
			case 4, 5: // add link
				if len(known) >= 1 {
					a := known[rng.Intn(len(known))]
					b := known[rng.Intn(len(known))]
					typ := lEdge
					if rng.Intn(2) == 0 {
						typ = lPair
					}
					gErr := g.AddLink(Link{Type: typ, Source: a, Sink: b})
					nOk := n.addLink(typ, a, b)
					if (gErr == nil) != nOk {
						t.Fatalf("seed %d step %d addLink %s %s->%s divergence: g=%v n=%v",
							seed, step, typ, a, b, gErr, nOk)
					}
				}
			case 6: // remove object
				if len(known) > 0 {
					id := known[rng.Intn(len(known))]
					gErr := g.RemoveObject(id)
					nOk := n.removeObject(id)
					if (gErr == nil) != nOk {
						t.Fatalf("seed %d step %d removeObject %s divergence: g=%v n=%v",
							seed, step, id, gErr, nOk)
					}
				}
			case 7: // remove link
				if len(known) > 0 {
					a := known[rng.Intn(len(known))]
					b := known[rng.Intn(len(known))]
					typ := lEdge
					if rng.Intn(2) == 0 {
						typ = lPair
					}
					gErr := g.RemoveLink(Link{Type: typ, Source: a, Sink: b})
					nOk := n.removeLink(typ, a, b)
					if (gErr == nil) != nOk {
						t.Fatalf("seed %d step %d removeLink divergence: g=%v n=%v", seed, step, gErr, nOk)
					}
				}
			default: // extract
				if len(known) == 0 {
					continue
				}
				caller := callers[rng.Intn(len(callers))]
				k := 1 + rng.Intn(8)
				scopeSet := map[ObjectID]bool{}
				var scope []ObjectID
				for i := 0; i < k; i++ {
					var id ObjectID
					if rng.Intn(5) == 0 {
						// sometimes a never-existed id
						id = ObjectID(fmt.Sprintf("ghost%d", rng.Intn(100000)))
					} else {
						id = known[rng.Intn(len(known))]
					}
					if !scopeSet[id] {
						scopeSet[id] = true
						scope = append(scope, id)
					}
				}

				snap, gErr := g.Extract(caller, scope)
				oracle := n.extract(caller, scope)
				if (gErr != nil) != oracle.err {
					t.Fatalf("seed %d step %d error divergence: g=%v oracle=%v", seed, step, gErr, oracle.err)
				}
				if gErr != nil {
					continue
				}

				gotIDs := objectIDs(snap.Objects)
				gotLinks := snap.Links
				gotDangling := snap.Dangling
				if gotLinks == nil {
					gotLinks = []Link{}
				}
				if gotDangling == nil {
					gotDangling = []DanglingLink{}
				}
				match := snap.Epoch == oracle.epoch &&
					reflect.DeepEqual(gotIDs, oracle.objects) &&
					reflect.DeepEqual(gotLinks, oracle.links) &&
					reflect.DeepEqual(gotDangling, oracle.dangling)

				rec := extractionRecord{
					Seed:            seed,
					Step:            step,
					Caller:          caller,
					Scope:           append([]ObjectID(nil), scope...),
					Epoch:           snap.Epoch,
					Objects:         gotIDs,
					Links:           snap.Links,
					Dangling:        snap.Dangling,
					CandidateLinks:  snap.metrics.candidateLinksExamined,
					PermissionCheck: snap.metrics.permissionChecks,
					OracleObjects:   oracle.objects,
					OracleLinks:     oracle.links,
					OracleDangling:  oracle.dangling,
					Match:           match,
				}
				if rec.Objects == nil {
					rec.Objects = []ObjectID{}
				}
				if rec.OracleObjects == nil {
					rec.OracleObjects = []ObjectID{}
				}
				if err := enc.Encode(rec); err != nil {
					t.Fatalf("write log: %v", err)
				}
				totalExtractions++

				if !match {
					t.Fatalf("seed %d step %d snapshot mismatch:\nimpl epoch=%d objs=%v links=%v dangling=%+v\noracle epoch=%d objs=%v links=%v dangling=%+v",
						seed, step,
						snap.Epoch, gotIDs, snap.Links, snap.Dangling,
						oracle.epoch, oracle.objects, oracle.links, oracle.dangling)
				}
			}
		}
	}
	t.Logf("differential extractions logged: %d at %s", totalExtractions, logPath)
}

func appendUniqueID(xs []ObjectID, x ObjectID) []ObjectID {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}
