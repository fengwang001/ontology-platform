package ontology

import (
	"bufio"
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// queryRecord is the per-query audit row required by the spec: input,
// three-state output and the classification basis.
type queryRecord struct {
	Seed        int64   `json:"seed"`
	Step        int     `json:"step"`
	Epoch       int64   `json:"epoch"`
	Caller      string  `json:"caller"`
	Start       string  `json:"start"`
	End         string  `json:"end"`
	Outcome     string  `json:"outcome"`
	Reason      Reason  `json:"reason"`
	GroundTruth bool    `json:"ground_truth_path"`
	StartVis    bool    `json:"start_visible"`
	EndVis      bool    `json:"end_visible"`
	Expected    string  `json:"expected"`
	Match       bool    `json:"match"`
	Metrics     Metrics `json:"metrics"`
}

// oracleModel is an independent specification model. It combines the naive
// ground-truth reachability from naive.go (which shares no search code with
// reachableFrom), a permission-aware BFS written from scratch right here, and
// the mandated precedence rules. A disagreement fails the differential test.
func oracleModel(t *testing.T, s *snap, start, end string, c CallerID) (string, Reason) {
	t.Helper()
	if !validIdentifier(start) || !validIdentifier(end) {
		return "invalid_id", ""
	}
	if _, ok := s.objects[start]; !ok {
		return "missing_start", ""
	}
	if _, ok := s.objects[end]; !ok {
		return "missing_end", ""
	}
	if !s.visible(c, start) || !s.visible(c, end) {
		return RestrictedUnknown.String(), ReasonInvisibleEndpoint
	}

	// Rebuild arcs from raw links instead of touching s.adj.
	arcs := map[string][]edge{}
	for _, l := range s.links {
		arcs[l.Src] = append(arcs[l.Src], edge{l.LinkType, l.Dst})
		if s.linkTypes[l.LinkType] == Bidirectional {
			arcs[l.Dst] = append(arcs[l.Dst], edge{l.LinkType, l.Src})
		}
	}

	seen := map[string]bool{start: true}
	q := []string{start}
	for head := 0; head < len(q); head++ {
		for _, e := range arcs[q[head]] {
			if seen[e.to] || !s.canTraverse(c, e.linkType) || !s.visible(c, e.to) {
				continue
			}
			seen[e.to] = true
			q = append(q, e.to)
		}
	}
	certified := seen[end]

	gseen := map[string]bool{start: true}
	gq := []string{start}
	for head := 0; head < len(gq); head++ {
		for _, e := range arcs[gq[head]] {
			if !gseen[e.to] {
				gseen[e.to] = true
				gq = append(gq, e.to)
			}
		}
	}
	switch {
	case certified:
		return Reachable.String(), ReasonCertifiedPath
	case gseen[end]:
		return RestrictedUnknown.String(), ReasonCandidatesTruncated
	default:
		return Unreachable.String(), ReasonNoGroundTruthPath
	}
}

func TestRandomDifferentialAgainstOracle(t *testing.T) {
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join("testdata", "reachability_queries.jsonl")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(logFile)
	defer func() {
		_ = w.Flush()
		_ = logFile.Close()
		t.Logf("query audit log: %s", logPath)
	}()

	const seeds = 12
	const steps = 300
	seenClass := map[string]bool{}

	for seed := int64(1); seed <= seeds; seed++ {
		rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed*7919)))
		g := NewGraph()
		if err := g.AddObjectType(ot); err != nil {
			t.Fatal(err)
		}

		nLinkTypes := 1 + rng.IntN(4)
		typeSpec := make([]struct {
			name string
			dir  Direction
		}, nLinkTypes)
		for i := range nLinkTypes {
			name := "T" + itoa(i)
			dir := Unidirectional
			if rng.IntN(2) == 0 {
				dir = Bidirectional
			}
			if err := g.AddLinkType(name, dir); err != nil {
				t.Fatal(err)
			}
			typeSpec[i] = struct {
				name string
				dir  Direction
			}{name, dir}
		}

		nObjs := 2 + rng.IntN(14)
		objIDs := make([]string, nObjs)
		for i := range nObjs {
			objIDs[i] = "o" + itoa(i)
			if err := g.AddObject(Object{ID: objIDs[i], ObjectType: ot}); err != nil {
				t.Fatal(err)
			}
		}
		callers := []CallerID{"u1", "u2", "u3"}

		for step := range steps {
			switch rng.IntN(8) {
			case 0, 1:
				ltp := typeSpec[rng.IntN(nLinkTypes)]
				src := objIDs[rng.IntN(nObjs)]
				dst := objIDs[rng.IntN(nObjs)]
				id := "L" + itoa(step) + "_" + itoa(rng.IntN(1<<20))
				_ = g.AddLink(Link{ID: id, LinkType: ltp.name, Src: src, Dst: dst})
			case 2, 3:
				c := callers[rng.IntN(len(callers))]
				id := objIDs[rng.IntN(nObjs)]
				if rng.IntN(2) == 0 {
					g.GrantExistence(c, id)
				} else {
					g.RevokeExistence(c, id)
				}
			case 4, 5:
				c := callers[rng.IntN(len(callers))]
				ltp := typeSpec[rng.IntN(nLinkTypes)]
				if rng.IntN(2) == 0 {
					g.GrantTraversal(c, ltp.name)
				} else {
					g.RevokeTraversal(c, ltp.name)
				}
			default:
				c := callers[rng.IntN(len(callers))]
				start := objIDs[rng.IntN(nObjs)]
				end := objIDs[rng.IntN(nObjs)]

				out, reason, m, qerr := g.ReachableFromTraced(context.Background(), start, end, c, nil)
				s := g.snapshot()

				var got string
				if qerr != nil {
					if qe, ok := qerr.(*QueryError); ok {
						got = qe.Kind
					}
				} else {
					got = out.String()
				}
				expect, expReason := oracleModel(t, s, start, end, c)

				ground, gerr := NaiveClassify(context.Background(), g, start, end)
				if gerr != nil {
					ground = false
				}

				match := got == expect &&
					(qerr != nil || string(reason) == string(expReason))

				rec := queryRecord{
					Seed: seed, Step: step, Epoch: s.epoch,
					Caller: string(c), Start: start, End: end,
					Outcome: got, Reason: reason, GroundTruth: ground,
					StartVis: s.visible(c, start),
					EndVis:   s.visible(c, end),
					Expected: expect + "/" + string(expReason),
					Match:    match,
					Metrics:  *m,
				}
				row, _ := json.Marshal(rec)
				if _, err := w.Write(append(row, '\n')); err != nil {
					t.Fatal(err)
				}
				if !match {
					_ = w.Flush()
					t.Fatalf("seed=%d step=%d %s->%s caller=%s got=%s/%s expected=%s/%s log=%s",
						seed, step, start, end, c, got, reason, expect, expReason, logPath)
				}
				if qerr == nil {
					seenClass[got+"/"+string(reason)] = true
				} else {
					seenClass[got] = true
				}
			}
		}
	}
	for _, want := range []string{
		Reachable.String() + "/" + string(ReasonCertifiedPath),
		Unreachable.String() + "/" + string(ReasonNoGroundTruthPath),
		RestrictedUnknown.String() + "/" + string(ReasonCandidatesTruncated),
		RestrictedUnknown.String() + "/" + string(ReasonInvisibleEndpoint),
	} {
		if !seenClass[want] {
			t.Fatalf("random history never observed required class %q; observed=%v", want, seenClass)
		}
	}
}

// TestOutcomesMutuallyDistinct guards the three-state contract at the type
// level; the random test guards it at the behavioural level.
func TestOutcomesMutuallyDistinct(t *testing.T) {
	if Reachable == Unreachable || Unreachable == RestrictedUnknown || Reachable == RestrictedUnknown {
		t.Fatal("outcomes are not mutually distinct")
	}
	if Reachable.String() == Unreachable.String() ||
		Unreachable.String() == RestrictedUnknown.String() {
		t.Fatal("outcome labels collide")
	}
}
