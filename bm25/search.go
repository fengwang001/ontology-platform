package bm25

import (
	"math/big"
	"sort"
)

func (r *Ranker) Search(query []string, k int, asOf int) ([]Result, error) {
	if len(query) == 0 || k < 1 || invalidTerms(query) {
		return nil, ErrInvalidArgument
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if asOf < 0 || asOf > r.version {
		return nil, ErrVersionOutOfRange
	}
	if asOf < r.watermark {
		return nil, ErrVersionCompacted
	}

	stats := r.statsAt(asOf)
	current := asOf == r.version
	postingReads := 0
	queryTerms := uniqueTerms(query)
	candidates := make(map[string]*big.Rat)

	if stats.n > 0 {
		for _, term := range queryTerms {
			data := r.terms[term]
			if data == nil {
				continue
			}
			if current {
				postingReads += len(data.active)
			}

			df := dfAt(data.dfEvents, asOf, r.watermark)
			if df == 0 {
				continue
			}
			idf := big.NewRat(int64(2*(stats.n-df)+1), int64(2*df+1))

			for docID, posting := range data.active {
				if !current && !intervalContains(posting, asOf) {
					continue
				}
				doc := r.docAt(docID, asOf)
				if doc != nil {
					addTermScore(candidates, docID, doc.dl, posting.tf, stats, idf)
				}
			}
			for docID, postings := range data.archive {
				for _, posting := range postings {
					if !intervalContains(posting, asOf) {
						continue
					}
					doc := r.docAt(docID, asOf)
					if doc != nil {
						addTermScore(candidates, docID, doc.dl, posting.tf, stats, idf)
					}
				}
			}
		}
	}

	ranked := make([]Result, 0, len(candidates))
	for docID, score := range candidates {
		ranked = append(ranked, Result{DocID: docID, Score: rationalString(score)})
	}
	sort.Slice(ranked, func(i, j int) bool {
		left, _ := new(big.Rat).SetString(ranked[i].Score)
		right, _ := new(big.Rat).SetString(ranked[j].Score)
		if cmp := left.Cmp(right); cmp != 0 {
			return cmp > 0
		}
		return ranked[i].DocID < ranked[j].DocID
	})
	if len(ranked) > k {
		ranked = ranked[:k]
	}

	r.lastSearchPostingReads.Store(int64(postingReads))
	return ranked, nil
}

func rationalString(score *big.Rat) string {
	return score.Num().String() + "/" + score.Denom().String()
}

func (r *Ranker) statsAt(asOf int) globalStats {
	stats := globalStats{}
	for _, event := range r.global {
		if event.version <= asOf {
			stats.n += event.deltaN
			stats.l += event.deltaL
		}
	}
	return stats
}

func dfAt(events []dfEvent, asOf, watermark int) int {
	df := 0
	for _, event := range events {
		if watermark > 0 && event.version == watermark && asOf >= watermark {
			df = event.delta
		} else if event.version <= asOf {
			df += event.delta
		}
	}
	return df
}

func (r *Ranker) docAt(docID string, version int) *docState {
	if doc := r.docs[docID]; doc != nil && doc.start <= version {
		return doc
	}
	for _, doc := range r.docArchive[docID] {
		if doc.start <= version && version < doc.end {
			return doc
		}
	}
	return nil
}

func addTermScore(candidates map[string]*big.Rat, docID string, dl, tf int, stats globalStats, idf *big.Rat) {
	numerator := big.NewRat(int64(tf*5), 2)
	numerator.Mul(numerator, idf)

	dlRatio := big.NewRat(int64(dl), 1)
	dlRatio.Quo(dlRatio, big.NewRat(int64(stats.l), int64(stats.n)))
	lengthPart := big.NewRat(3, 4)
	lengthPart.Mul(lengthPart, dlRatio)
	lengthPart.Add(big.NewRat(1, 4), lengthPart)
	lengthPart.Mul(big.NewRat(3, 2), lengthPart)
	denominator := big.NewRat(int64(tf), 1)
	denominator.Add(denominator, lengthPart)

	termScore := new(big.Rat).Quo(numerator, denominator)
	if score := candidates[docID]; score != nil {
		score.Add(score, termScore)
	} else {
		candidates[docID] = termScore
	}
}
