package bm25

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestRandomOperationsAgainstNaiveReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	termsPool := []string{"q", "x", "z", "noise"}

	for trial := 0; trial < 2000; trial++ {
		r := NewRanker()
		versions := []map[string]naiveDoc{{}}
		ops := make([]string, 0, 20)

		for step := 0; step < 20; step++ {
			docID := fmt.Sprintf("d%d", rng.Intn(6))
			terms := randomTerms(rng, termsPool)
			current := versions[len(versions)-1]
			_, exists := current[docID]

			var err error
			var op string
			switch {
			case !exists:
				err = r.Add(docID, terms)
				op = fmt.Sprintf("Add(%q,%v)", docID, terms)
			case rng.Intn(3) == 0:
				err = r.Delete(docID)
				op = fmt.Sprintf("Delete(%q)", docID)
			default:
				terms = randomTerms(rng, termsPool)
				err = r.Update(docID, terms)
				op = fmt.Sprintf("Update(%q,%v)", docID, terms)
			}
			ops = append(ops, op)

			next := copySnapshot(versions[len(versions)-1])
			switch {
			case err == nil && op[0] == 'A':
				next[docID] = naiveDoc{terms: append([]string(nil), terms...)}
			case err == nil && op[0] == 'U':
				next[docID] = naiveDoc{terms: append([]string(nil), terms...)}
			case err == nil && op[0] == 'D':
				delete(next, docID)
			case err != nil:
				next = versions[len(versions)-1]
			}
			versions = append(versions, next)
			if r.Version() != len(versions)-1 {
				t.Fatalf("trial %d after %s: version=%d replay=%d", trial, op, r.Version(), len(versions)-1)
			}
		}

		queries := [][]string{{"q"}, {"x", "q"}, {"z", "z"}, {"missing"}, {"q", "x", "noise"}}
		for version := 0; version <= r.Version(); version++ {
			for _, query := range queries {
				k := 1 + rng.Intn(5)
				got := mustSearch(t, r, query, k, version)
				want := naiveSearch(versions[version], query, k)
				t.Logf("trial=%d input=%v Search(query=%v,k=%d,asOf=%d) output=%v basis=naive replay %v",
					trial, ops, query, k, version, got, want)
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Fatalf("trial %d mismatch at v%d query=%v: got=%v want=%v ops=%v",
						trial, version, query, got, want, ops)
				}
			}
		}

		keep := rng.Intn(r.Version() + 1)
		beforeCompact := map[int][]Result{}
		for version := keep; version <= r.Version(); version++ {
			beforeCompact[version] = mustSearch(t, r, queries[1], 8, version)
		}
		if err := r.Compact(keep); err != nil {
			t.Fatalf("trial %d compact(%d): %v", trial, keep, err)
		}
		t.Logf("trial=%d Compact(keep=%d) output=nil basis=versions>=keep unchanged", trial, keep)
		for version := keep; version <= r.Version(); version++ {
			got := mustSearch(t, r, queries[1], 8, version)
			want := beforeCompact[version]
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("trial %d compact changed v%d: got=%v want=%v", trial, version, got, want)
			}
		}
	}
}

func randomTerms(rng *rand.Rand, pool []string) []string {
	length := 1 + rng.Intn(6)
	terms := make([]string, length)
	for i := range terms {
		terms[i] = pool[rng.Intn(len(pool))]
	}
	return terms
}

func copySnapshot(in map[string]naiveDoc) map[string]naiveDoc {
	out := make(map[string]naiveDoc, len(in))
	for docID, doc := range in {
		out[docID] = naiveDoc{terms: append([]string(nil), doc.terms...)}
	}
	return out
}
