package ontology

import (
	"sort"
	"unicode/utf8"
)

type scoredEntry struct {
	entry        *indexEntry
	intersection int
	denominator  int
}

func (f *Finder) Similar(query string, theta int, k int) ([]Match, error) {
	if !validWord(query) {
		return nil, ErrInvalidWord
	}
	if theta < 1 || theta > 100 {
		return nil, ErrInvalidThreshold
	}
	if k < 1 {
		return nil, ErrInvalidLimit
	}

	effectiveThreshold := theta
	if utf8.RuneCountInString(query) <= 2 {
		effectiveThreshold += 20
		if effectiveThreshold > 100 {
			effectiveThreshold = 100
		}
	}

	queryGrams := trigrams(query)
	querySize := trigramSize(query)
	candidates := make(map[*indexEntry]int)

	f.mu.RLock()
	for gram := range queryGrams {
		for entry := range f.postings[gram] {
			f.postingReads.Add(1)
			candidates[entry]++
		}
	}
	f.mu.RUnlock()

	hits := make([]scoredEntry, 0, len(candidates))
	for entry := range candidates {
		intersection := intersectionCount(queryGrams, entry.grams)
		denominator := querySize + entry.size
		if 200*intersection >= effectiveThreshold*denominator {
			hits = append(hits, scoredEntry{
				entry:        entry,
				intersection: intersection,
				denominator:  denominator,
			})
		}
	}

	sort.Slice(hits, func(i, j int) bool {
		leftProduct := hits[i].intersection * hits[j].denominator
		rightProduct := hits[j].intersection * hits[i].denominator
		if leftProduct != rightProduct {
			return leftProduct > rightProduct
		}
		return hits[i].entry.word < hits[j].entry.word
	})

	return foldHits(hits, k), nil
}

func foldHits(hits []scoredEntry, k int) []Match {
	retained := make([]Match, 0, min(k, len(hits)))

	for _, hit := range hits {
		owner := -1
		for i := range retained {
			ownerWord := retained[i].Word
			if properRunePrefix(ownerWord, hit.entry.word) ||
				properRunePrefix(hit.entry.word, ownerWord) {
				owner = i
				break
			}
		}

		if owner >= 0 {
			retained[owner].Folded++
			continue
		}

		retained = append(retained, Match{
			Word:   hit.entry.word,
			Score:  reducedScore(2*hit.intersection, hit.denominator),
			Folded: 0,
		})
	}

	if len(retained) > k {
		retained = retained[:k]
	}
	return retained
}
