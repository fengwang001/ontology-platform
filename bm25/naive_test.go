package bm25

import (
	"math/big"
	"sort"
)

type naiveDoc struct {
	terms []string
}

type journalOp struct {
	kind   string
	docID  string
	terms  []string
	keep   int
	result string
}

func snapshotFromCurrent(r *Ranker, asOf int) map[string]naiveDoc {
	snapshot := make(map[string]naiveDoc)
	for docID := range r.docs {
		if r.docs[docID].start <= asOf {
			terms := make([]string, 0)
			for term, tf := range r.currentTerms[docID] {
				for i := 0; i < tf; i++ {
					terms = append(terms, term)
				}
			}
			sort.Strings(terms)
			snapshot[docID] = naiveDoc{terms: terms}
		}
	}
	return snapshot
}

func naiveSearch(snapshot map[string]naiveDoc, rawQuery []string, k int) []Result {
	query := make([]string, 0)
	seen := make(map[string]struct{})
	for _, term := range rawQuery {
		if _, ok := seen[term]; !ok {
			seen[term] = struct{}{}
			query = append(query, term)
		}
	}

	n := len(snapshot)
	l := 0
	df := make(map[string]int)
	for _, doc := range snapshot {
		l += len(doc.terms)
		present := make(map[string]bool)
		for _, term := range doc.terms {
			present[term] = true
		}
		for term := range present {
			df[term]++
		}
	}
	if n == 0 {
		return []Result{}
	}

	type scored struct {
		docID string
		score *big.Rat
	}
	ranked := make([]scored, 0)
	for docID, doc := range snapshot {
		score := new(big.Rat)
		tf := make(map[string]int)
		for _, term := range doc.terms {
			tf[term]++
		}
		matched := false
		for _, term := range query {
			if tf[term] == 0 {
				continue
			}
			matched = true
			score.Add(score, naiveTermScore(n, l, df[term], tf[term], len(doc.terms)))
		}
		if matched {
			ranked = append(ranked, scored{docID: docID, score: score})
		}
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score.Cmp(ranked[j].score) == 0 {
			return ranked[i].docID < ranked[j].docID
		}
		return ranked[i].score.Cmp(ranked[j].score) > 0
	})
	if len(ranked) > k {
		ranked = ranked[:k]
	}

	results := make([]Result, 0, len(ranked))
	for _, item := range ranked {
		results = append(results, Result{DocID: item.docID, Score: item.score.Num().String() + "/" + item.score.Denom().String()})
	}
	return results
}

func naiveScore(snapshot map[string]naiveDoc, docID string, query []string) *big.Rat {
	for _, result := range naiveSearch(snapshot, query, 1000) {
		if result.DocID == docID {
			score, _ := new(big.Rat).SetString(result.Score)
			return score
		}
	}
	return nil
}

func naiveTermScore(n, l, df, tf, dl int) *big.Rat {
	idf := big.NewRat(int64(2*(n-df)+1), int64(2*df+1))
	numerator := big.NewRat(int64(tf*5), 2)
	numerator.Mul(numerator, idf)

	avgdl := big.NewRat(int64(l), int64(n))
	dlPart := big.NewRat(int64(dl), 1)
	dlPart.Quo(dlPart, avgdl)
	dlPart.Mul(big.NewRat(3, 4), dlPart)
	dlPart.Add(big.NewRat(1, 4), dlPart)
	dlPart.Mul(big.NewRat(3, 2), dlPart)

	denominator := big.NewRat(int64(tf), 1)
	denominator.Add(denominator, dlPart)
	return numerator.Quo(numerator, denominator)
}
