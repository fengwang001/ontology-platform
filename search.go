package ontology

import (
	"math/big"
	"sort"
	"sync/atomic"
)

func (idx *Index) Search(query []string, k int, asOf int) ([]Result, error) {
	if !validTerms(query) || k < 1 {
		return nil, ErrInvalidArgument
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if asOf < 0 || asOf > idx.version {
		return nil, ErrVersionRange
	}
	if asOf < idx.watermark {
		return nil, ErrVersionReclaimed
	}

	uniqueQuery := uniqueTerms(query)
	if asOf == idx.version {
		var reads int64
		for _, term := range uniqueQuery {
			if state := idx.terms[term]; state != nil {
				reads += int64(len(state.postings))
			}
		}
		atomic.StoreInt64(&idx.currentPostingReads, reads)
	}

	stat := idx.stats[asOf-idx.statsBaseVersion]
	if stat.n == 0 {
		return []Result{}, nil
	}

	candidates := make(map[string]struct{})
	for _, term := range uniqueQuery {
		state := idx.terms[term]
		if state == nil || state.dfAt(asOf) == 0 {
			continue
		}
		for docID, posting := range state.postings {
			if posting.existsAt(asOf) {
				candidates[docID] = struct{}{}
			}
		}
	}

	results := make([]Result, 0, len(candidates))
	for docID := range candidates {
		snapshot := idx.docs[docID].existsAt(asOf)
		frequencies := termFrequencies(snapshot.terms)
		totalScore := new(big.Rat)
		for _, term := range uniqueQuery {
			tf := frequencies[term]
			if tf == 0 {
				continue
			}
			df := idx.terms[term].dfAt(asOf)
			totalScore.Add(totalScore, termScore(tf, df, len(snapshot.terms), stat))
		}
		results = append(results, Result{DocID: docID, Score: ratString(totalScore)})
	}

	sort.Slice(results, func(i, j int) bool {
		left, _ := new(big.Rat).SetString(results[i].Score)
		right, _ := new(big.Rat).SetString(results[j].Score)
		if cmp := left.Cmp(right); cmp != 0 {
			return cmp > 0
		}
		return results[i].DocID < results[j].DocID
	})
	if len(results) > k {
		results = results[:k]
	}
	return results, nil
}
