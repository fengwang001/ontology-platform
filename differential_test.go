package ontology

import (
	"bufio"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// recordedRun is one JSONL line documenting an extraction: request input,
// production output and dangling-link side information.
type recordedRun struct {
	Seed           int64          `json:"seed"`
	Iteration      int            `json:"iteration"`
	Principal      string         `json:"principal"`
	Revision       int64          `json:"revision"`
	Scope          []ID           `json:"scope"`
	Objects        []ID           `json:"objects"`
	Links          []ID           `json:"links"`
	CandidateLinks int            `json:"candidate_links_checked"`
	Dangling       []recordedDang `json:"dangling"`
}

type recordedDang struct {
	LinkID   ID     `json:"link_id"`
	From     ID     `json:"from"`
	To       ID     `json:"to"`
	Included ID     `json:"included_endpoint"`
	Excluded ID     `json:"excluded_endpoint"`
	Dir      string `json:"direction"`
	Source   string `json:"source"`
}

func dirName(d Direction) string {
	switch d {
	case DirOut:
		return "out"
	case DirIn:
		return "in"
	default:
		return "either"
	}
}

func snapshotRecord(seed int64, iter int, snap *Snapshot) recordedRun {
	r := recordedRun{
		Seed:           seed,
		Iteration:      iter,
		Principal:      snap.Principal,
		Revision:       snap.Revision,
		Scope:          append([]ID(nil), snap.Scope...),
		Objects:        objectIDs(snap),
		Links:          linkIDs(snap),
		CandidateLinks: snap.candidateLinksChecked,
		Dangling:       []recordedDang{},
	}
	for _, d := range snap.Dangling {
		r.Dangling = append(r.Dangling, recordedDang{
			LinkID: d.LinkID, From: d.From, To: d.To,
			Included: d.Included, Excluded: d.Excluded,
			Dir: dirName(d.Dir), Source: d.Source.String(),
		})
	}
	return r
}

// TestNaiveDifferentialRandom builds random graphs, permission matrices,
// scopes (including self links and duplicate/unsorted inputs) and compares
// the production extractor against the independent naive model on the same
// pinned state. Every extraction is recorded as JSONL under testdata/.
func TestNaiveDifferentialRandom(t *testing.T) {
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join("testdata", "extraction_runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	enc := json.NewEncoder(w)

	const seed int64 = 1724
	rng := rand.New(rand.NewSource(seed))
	g := NewGraph()
	const nObj = 24
	for i := 0; i < nObj; i++ {
		mustObj(t, g, ID("o"+padID(i)))
	}
	principals := []string{"alice", "bob", "carol"}
	// Random permission matrix.
	for i := 0; i < nObj; i++ {
		id := ID("o" + padID(i))
		for _, p := range principals {
			if rng.Intn(2) == 0 {
				if _, err := g.GrantExistence(p, id); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	// Random links: directed/undirected, including parallel and self links.
	linkSeq := 0
	mkLink := func() {
		from := rng.Intn(nObj)
		to := from
		if rng.Intn(5) != 0 { // 80% non-self
			to = rng.Intn(nObj)
		}
		lt := undT
		if rng.Intn(2) == 0 {
			lt = dirT
		}
		id := ID("L" + padID(linkSeq))
		linkSeq++
		if _, err := g.AddLink(Link{
			ID: id, Type: lt,
			From: ID("o" + padID(from)), To: ID("o" + padID(to)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 60; i++ {
		mkLink()
	}

	ex := NewExtractor(g).WithMaxScope(30)
	iter := 0
	for round := 0; round < 200; round++ {
		// Build a random scope: nonempty, possibly with duplicates;
		// occasionally include a syntactically malformed ID, in which
		// case both implementations must agree on the parameter error.
		k := 1 + rng.Intn(10)
		scope := make([]ID, 0, k)
		malformed := false
		for j := 0; j < k; j++ {
			if rng.Intn(20) == 0 {
				scope = append(scope, "bad id")
				malformed = true
			} else {
				scope = append(scope, ID("o"+padID(rng.Intn(nObj))))
			}
		}
		principal := principals[rng.Intn(len(principals))]

		s := g.snapshotState()

		if malformed {
			if _, err := ex.Extract(principal, scope); err != ErrInvalidID {
				t.Fatalf("iter %d: production err = %v, want ErrInvalidID", iter, err)
			}
			continue
		}

		validated, err := ex.validateScope(scope)
		if err != nil {
			t.Fatalf("iter %d: unexpected validation error: %v", iter, err)
		}
		prod := extractState(s, validated, principal)
		naive := naiveSnapshot(s, validated, principal)
		if !reflect.DeepEqual(prod, naive) {
			t.Fatalf("iter %d mismatch:\nprod  = %+v\nnaive = %+v",
				iter, prod, naive)
		}
		if err := enc.Encode(snapshotRecord(seed, iter, prod)); err != nil {
			t.Fatal(err)
		}
		iter++

		// Random interleaved mutation to exercise revision windows.
		switch rng.Intn(4) {
		case 0:
			mkLink()
		case 1:
			idx := rng.Intn(linkSeq)
			_, _ = g.RemoveLink(ID("L" + padID(idx)))
		case 2:
			p := principals[rng.Intn(len(principals))]
			id := ID("o" + padID(rng.Intn(nObj)))
			_, _ = g.RevokeExistence(p, id)
		case 3:
			p := principals[rng.Intn(len(principals))]
			id := ID("o" + padID(rng.Intn(nObj)))
			_, _ = g.GrantExistence(p, id)
		}
	}
	if iter == 0 {
		t.Fatal("no successful extractions recorded")
	}
	t.Logf("recorded %d extraction runs to testdata/extraction_runs.jsonl", iter)
}

// TestRepeatedWindowAttributionRandom: for random scopes, two extractions
// over a window with no relevant change must be identical; when they
// differ every diff category must correspond to a journaled change.
func TestRepeatedWindowAttributionRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	g := NewGraph()
	for i := 0; i < 12; i++ {
		mustObj(t, g, ID("o"+padID(i)))
		if _, err := g.GrantExistence("p", ID("o"+padID(i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		a, b := rng.Intn(12), rng.Intn(12)
		lt := dirT
		if rng.Intn(2) == 0 {
			lt = undT
		}
		_, _ = g.AddLink(Link{
			ID: ID("w" + padID(i)), Type: lt,
			From: ID("o" + padID(a)), To: ID("o" + padID(b)),
		})
	}
	ex := NewExtractor(g)
	pickScope := func() []ID {
		k := 1 + rng.Intn(8)
		out := make([]ID, 0, k)
		for i := 0; i < k; i++ {
			out = append(out, ID("o"+padID(rng.Intn(12))))
		}
		return out
	}
	for i := 0; i < 100; i++ {
		scope := pickScope()
		s1, err := ex.Extract("p", scope)
		if err != nil {
			t.Fatal(err)
		}
		s2, err := ex.Extract("p", scope)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(s1, s2) {
			t.Fatalf("window without changes produced differences")
		}
		// Apply a mutation, then require any diff to be journal-attributable.
		idx := rng.Intn(20)
		removed := ID("w" + padID(idx))
		_, remErr := g.RemoveLink(removed)
		removedOK := remErr == nil
		s3, err := ex.Extract("p", scope)
		if err != nil {
			t.Fatal(err)
		}
		d := Compare(s1, s3)
		attr := g.Attribute(s1, s3)
		if len(attr.Changes) == 0 && !d.Empty() {
			t.Fatalf("unexplained diff with empty journal window: %+v", d)
		}
		if removedOK {
			foundLink := false
			for _, l := range d.LinksRemoved {
				if l.ID == removed {
					foundLink = true
				}
			}
			foundDangling := false
			for _, r := range d.DanglingRemoved {
				if r.LinkID == removed {
					foundDangling = true
				}
			}
			// On the pre-removal snapshot the removed link either was
			// included/dangling (then it must show as removed) or was
			// invisible (then diff must be empty).
			wasVisible := s1.HasDangling(removed)
			for _, l := range s1.Links {
				if l.ID == removed {
					wasVisible = true
				}
			}
			if wasVisible && !foundLink && !foundDangling {
				t.Fatalf("removed visible link %s not in diff: %+v", removed, d)
			}
			if !wasVisible && !d.Empty() &&
				!(len(d.DanglingRemoved) == 0 && len(d.LinksRemoved) == 0) {
				t.Fatalf("invisible removal caused diff: %+v", d)
			}
		}
	}
}

func padID(i int) string {
	switch {
	case i < 10:
		return "0" + itoa(i)
	default:
		return itoa(i)
	}
}
