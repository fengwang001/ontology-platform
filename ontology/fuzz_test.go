package ontology_test

import (
	"bufio"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"ontology/ontology"
	"ontology/ontology/naive"
)

// queryRecord is one line of the differential decision log: inputs,
// both implementations' outputs and the tri-state classification basis.
type queryRecord struct {
	Trial       int    `json:"trial"`
	From        string `json:"from"`
	To          string `json:"to"`
	Caller      string `json:"caller"`
	Outcome     string `json:"outcome"`
	Reason      string `json:"reason"`
	Naive       string `json:"naive_outcome"`
	NaiveReason string `json:"naive_reason"`
	Err         string `json:"error,omitempty"`
	Metric      struct {
		Objects   int `json:"objects_expanded"`
		Links     int `json:"links_attempted"`
		BtObjects int `json:"backtrack_objects"`
		BtLinks   int `json:"backtrack_links"`
	} `json:"internal_metrics"`
	Objects int `json:"graph_objects"`
	Links   int `json:"graph_links"`
}

// TestRandomDifferential runs random object/link/permission sequences and
// compares every query against the independent naive exhaustive model.
// All query decisions are recorded as JSONL under testdata/ (and the temp
// dir) for inspection of inputs, outputs and classification reasons.
func TestRandomDifferential(t *testing.T) {
	logPath := filepath.Join("testdata", "differential_queries.jsonl")
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	w := bufio.NewWriter(logFile)
	defer w.Flush()

	const trials = 400
	totalQueries := 0
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(int64(20261007 + trial)))
		g := ontology.NewGraph()
		if err := g.AddObjectType(ontology.ObjectType{ID: "T"}); err != nil {
			t.Fatal(err)
		}

		ltIDs := []string{"L1", "L2", "L3"}
		for i, id := range ltIDs {
			dir := ontology.Directed
			if i == 2 {
				dir = ontology.Bidirectional
			}
			if err := g.AddLinkType(ontology.LinkType{ID: id, FromType: "T", ToType: "T", Direction: dir}); err != nil {
				t.Fatal(err)
			}
		}

		n := 3 + rng.Intn(10)
		objIDs := make([]string, 0, n)
		for i := 0; i < n; i++ {
			id := nodeID(i)
			if err := g.AddObject(ontology.ObjectInstance{ID: id, ObjectType: "T"}); err != nil {
				t.Fatal(err)
			}
			objIDs = append(objIDs, id)
		}

		m := 2 + rng.Intn(2*n)
		for i := 0; i < m; i++ {
			_ = g.AddLink(ontology.LinkInstance{
				ID:       linkID(trial, i),
				LinkType: ltIDs[rng.Intn(len(ltIDs))],
				Tail:     objIDs[rng.Intn(n)],
				Head:     objIDs[rng.Intn(n)],
			})
		}

		callers := []string{"c1", "c2"}
		ops := 2 * n
		for i := 0; i < ops; i++ {
			caller := callers[rng.Intn(len(callers))]
			switch rng.Intn(4) {
			case 0:
				_ = g.GrantExistence(caller, objIDs[rng.Intn(n)])
			case 1:
				_ = g.RevokeExistence(caller, objIDs[rng.Intn(n)])
			case 2:
				_ = g.GrantTraversal(caller, ltIDs[rng.Intn(len(ltIDs))])
			case 3:
				_ = g.RevokeTraversal(caller, ltIDs[rng.Intn(len(ltIDs))])
			}
		}

		caller := callers[trial%2]
		// Bias the scenario toward endpoint-visible searches so the
		// reachable/restricted/unreachable classification (not just the
		// endpoint-invisible prefix) gets dense differential coverage.
		for _, id := range objIDs {
			if rng.Intn(2) == 0 {
				_ = g.GrantExistence(caller, id)
			}
		}
		objs, lts, links, ex, tr := g.ExportForFuzz(caller)
		snap := naive.Snapshot{
			Objects:     objs,
			LinkTypes:   lts,
			Links:       links,
			Existence:   ex,
			Traversable: tr,
		}

		queries := 6
		for q := 0; q < queries; q++ {
			from := objIDs[rng.Intn(n)]
			to := objIDs[rng.Intn(n)]
			if rng.Intn(4) != 0 {
				_ = g.GrantExistence(caller, from)
				_ = g.GrantExistence(caller, to)
				objs, lts, links, ex, tr = g.ExportForFuzz(caller)
				snap = naive.Snapshot{Objects: objs, LinkTypes: lts, Links: links,
					Existence: ex, Traversable: tr}
			}
			out, trace, gerr := g.Reachable(from, to, caller)
			nOut, nReason, nerr := naive.Decide(snap, from, to)

			rec := queryRecord{Trial: trial, From: from, To: to, Caller: caller,
				Objects: n, Links: len(links)}
			if gerr != nil {
				rec.Err = gerr.Error()
			} else {
				rec.Outcome = out.String()
				rec.Reason = trace.Reason
				rec.Metric.Objects = trace.Metrics.ObjectsExpanded
				rec.Metric.Links = trace.Metrics.LinksAttempted
				rec.Metric.BtObjects = trace.Metrics.BacktrackObjects
				rec.Metric.BtLinks = trace.Metrics.BacktrackLinks
			}
			if nerr != nil {
				rec.Naive = nerr.Error()
			} else {
				rec.Naive = nOut.String()
				rec.NaiveReason = nReason
			}

			equivalent := (gerr != nil && nerr != nil && gerr.Error() == nerr.Error()) ||
				(gerr == nil && nerr == nil && out == nOut)
			if !equivalent {
				t.Fatalf("trial=%d %s->%s caller=%s: engine=(%v,%v) naive=(%v,%v)",
					trial, from, to, caller, out, gerr, nOut, nerr)
			}

			line, _ := json.Marshal(rec)
			if _, err := w.Write(append(line, '\n')); err != nil {
				t.Fatal(err)
			}
			totalQueries++
		}
	}
	t.Logf("differential queries recorded: %d -> %s", totalQueries, logPath)
}

// TestRandomInvalidAndAbsent additionally stresses the decision-order prefix
// (illegal identifiers, absent nodes) across random graphs.
func TestRandomInvalidAndAbsent(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 50; trial++ {
		g := ontology.NewGraph()
		_ = g.AddObjectType(ontology.ObjectType{ID: "T"})
		_ = g.AddLinkType(ontology.LinkType{ID: "L", FromType: "T", ToType: "T", Direction: ontology.Directed})
		_ = g.AddObject(ontology.ObjectInstance{ID: "a", ObjectType: "T"})
		_ = g.AddObject(ontology.ObjectInstance{ID: "b", ObjectType: "T"})
		_ = g.AddLink(ontology.LinkInstance{ID: "e", LinkType: "L", Tail: "a", Head: "b"})

		caller := "c"
		objs, lts, links, ex, tr := g.ExportForFuzz(caller)
		snap := naive.Snapshot{Objects: objs, LinkTypes: lts, Links: links, Existence: ex, Traversable: tr}

		bad := []string{"", "has space", string([]rune{'好'})}
		_ = rng
		for _, id := range bad {
			if _, _, err := g.Reachable(id, "b", caller); err != ontology.ErrInvalidID {
				t.Fatalf("invalid id %q: %v", id, err)
			}
			if _, _, err := naive.Decide(snap, id, "b"); err != ontology.ErrInvalidID {
				t.Fatalf("naive invalid id %q: %v", id, err)
			}
			if _, _, err := g.Reachable("a", "missing", caller); err != ontology.ErrNotFound {
				t.Fatalf("absent: %v", err)
			}
			if _, _, err := naive.Decide(snap, "a", "missing"); err != ontology.ErrNotFound {
				t.Fatalf("naive absent: %v", err)
			}
		}
	}
}

func nodeID(i int) string        { return "n" + itoa(i) }
func linkID(trial, i int) string { return "l_" + itoa(trial) + "_" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
