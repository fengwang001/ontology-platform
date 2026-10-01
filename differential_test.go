package ontology

import (
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"strconv"
	"testing"
)

type naiveIndex struct {
	versions []map[string][]string
}

func newNaiveIndex() *naiveIndex {
	return &naiveIndex{versions: []map[string][]string{{}}}
}

func copyNaiveState(state map[string][]string) map[string][]string {
	next := make(map[string][]string, len(state))
	for docID, terms := range state {
		next[docID] = append([]string(nil), terms...)
	}
	return next
}

func (idx *naiveIndex) add(docID string, terms []string) {
	state := copyNaiveState(idx.versions[len(idx.versions)-1])
	state[docID] = append([]string(nil), terms...)
	idx.versions = append(idx.versions, state)
}

func (idx *naiveIndex) update(docID string, terms []string) {
	state := copyNaiveState(idx.versions[len(idx.versions)-1])
	state[docID] = append([]string(nil), terms...)
	idx.versions = append(idx.versions, state)
}

func (idx *naiveIndex) delete(docID string) {
	state := copyNaiveState(idx.versions[len(idx.versions)-1])
	delete(state, docID)
	idx.versions = append(idx.versions, state)
}

func naiveSearch(state map[string][]string, query []string, k int) []Result {
	uniqueQuery := uniqueTerms(query)
	n := len(state)
	if n == 0 {
		return []Result{}
	}

	l := 0
	df := make(map[string]int)
	for _, terms := range state {
		l += len(terms)
		seen := make(map[string]bool)
		for _, term := range terms {
			seen[term] = true
		}
		for term := range seen {
			df[term]++
		}
	}

	var results []Result
	for docID, terms := range state {
		frequencies := termFrequencies(terms)
		score := new(big.Rat)
		for _, term := range uniqueQuery {
			if frequencies[term] > 0 {
				score.Add(score, termScore(frequencies[term], df[term], len(terms), globalStat{n: n, l: l}))
			}
		}
		if score.Sign() > 0 {
			results = append(results, Result{DocID: docID, Score: ratString(score)})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		left, _ := new(big.Rat).SetString(results[i].Score)
		right, _ := new(big.Rat).SetString(results[j].Score)
		if left.Cmp(right) != 0 {
			return left.Cmp(right) > 0
		}
		return results[i].DocID < results[j].DocID
	})
	if len(results) > k {
		results = results[:k]
	}
	return results
}

func TestRandomDifferential2000(t *testing.T) {
	t.Log("input=random Add/Update/Delete + Search/Compact; output=incremental and naive results; criterion=same version, N, L, df, score, tie order, and compaction boundary")
	rng := rand.New(rand.NewSource(20261002))

	for iteration := 0; iteration < 2000; iteration++ {
		idx := NewIndex()
		naive := newNaiveIndex()
		watermark := 0
		var log []string

		for operation := 0; operation < 18; operation++ {
			docID := "d" + strconv.Itoa(rng.Intn(6))
			terms := randomTerms(rng, rng.Intn(3)+1)
			switch rng.Intn(6) {
			case 0:
				_, err := idx.Add(docID, terms)
				if err == nil {
					naive.add(docID, terms)
				}
				log = append(log, fmt.Sprintf("add %s %v => %v", docID, terms, err))
			case 1:
				_, err := idx.Update(docID, terms)
				if err == nil {
					naive.update(docID, terms)
				}
				log = append(log, fmt.Sprintf("update %s %v => %v", docID, terms, err))
			case 2:
				_, err := idx.Delete(docID)
				if err == nil {
					naive.delete(docID)
				}
				log = append(log, fmt.Sprintf("delete %s => %v", docID, err))
			default:
				version := len(naive.versions) - 1
				asOf := rng.Intn(version + 2)
				if rng.Intn(5) == 0 {
					asOf = -1
				}
				query := randomTerms(rng, rng.Intn(3)+1)
				if rng.Intn(8) == 0 {
					query = append(query, "")
				}
				k := rng.Intn(5) + 1
				got, err := idx.Search(query, k, asOf)
				var want []Result
				var wantErr error
				if !validTerms(query) || k < 1 {
					wantErr = ErrInvalidArgument
				} else if asOf < 0 || asOf > version {
					wantErr = ErrVersionRange
				} else if asOf < watermark {
					wantErr = ErrVersionReclaimed
				} else {
					want = naiveSearch(naive.versions[asOf], query, k)
				}
				if fmt.Sprint(err) != fmt.Sprint(wantErr) || !sameResults(got, want) {
					t.Fatalf("iteration=%d query mismatch\nops=%s\ngot=%v err=%v\nwant=%v wantErr=%v\ncriterion=incremental must equal naive full recomputation",
						iteration, log, got, err, want, wantErr)
				}
				log = append(log, fmt.Sprintf("search %v k=%d asOf=%d => %v %v", query, k, asOf, got, err))
			}

			if rng.Intn(5) == 0 && len(naive.versions) > 1 {
				version := len(naive.versions) - 1
				keep := rng.Intn(version + 2)
				_, err := idx.Compact(keep)
				if err == nil {
					watermark = keep
				}
				log = append(log, fmt.Sprintf("compact %d => W=%d %v", keep, watermark, err))
			}
		}

		version := len(naive.versions) - 1
		for asOf := watermark; asOf <= version; asOf++ {
			query := []string{"t0", "t1", "t0"}
			got, err := idx.Search(query, 20, asOf)
			if err != nil {
				t.Fatalf("post-compaction asOf=%d: %v", asOf, err)
			}
			want := naiveSearch(naive.versions[asOf], query, 20)
			if !sameResults(got, want) {
				t.Fatalf("iteration=%d post-compact mismatch asOf=%d: got=%v want=%v", iteration, asOf, got, want)
			}
		}
		t.Logf("iteration=%d accepted operations=%d W=%d V=%d; output matched naive recomputation at every queryable version; sample=%s",
			iteration, len(naive.versions)-1, watermark, len(naive.versions)-1, log[len(log)-1])
	}
}

func randomTerms(rng *rand.Rand, count int) []string {
	terms := make([]string, count)
	for i := range terms {
		terms[i] = "t" + strconv.Itoa(rng.Intn(4))
	}
	return terms
}

func sameResults(left, right []Result) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
