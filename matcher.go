// Package ontology implements a phrase proximity matcher with sentence
// boundaries, a total slop budget and a per-step gap limit.
package ontology

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"
)

// Boundary is the reserved term marking a sentence boundary. It occupies one
// position and a match must never span across it.
const Boundary = "<S>"

// Result is one matching document in the ranked output of Phrase.
type Result struct {
	DocID  string
	Freq   int
	MinGap int
}

// Sentinel errors let callers distinguish rejection reasons.
var (
	ErrInvalidArguments = errors.New("invalid arguments")
	ErrDuplicateDoc     = errors.New("duplicate document")
	ErrDocNotFound      = errors.New("document not found")
	ErrInvalidQuery     = errors.New("invalid query")
	ErrInvalidGap       = errors.New("invalid gap")
	ErrInvalidK         = errors.New("invalid k")
)

// Index is the concurrent-safe phrase index.
type Index struct {
	mu sync.RWMutex
	// docs holds the registered documents. Re-adding a docID after Delete
	// replaces the old entry entirely.
	docs map[string]*docData
	// inverted maps a term to the set of docIDs currently containing it.
	inverted map[string]map[string]struct{}
	// readCnt counts inverted-list position entries read by Phrase calls.
	// It is unexported and updated atomically so concurrent Phrase calls
	// (which share the read lock) do not race.
	readCnt atomic.Int64
}

type docData struct {
	// postings maps every non-boundary term to its ascending position list.
	postings map[string][]int
	// bounds holds positions of Boundary terms in ascending order.
	bounds []int
}

// NewIndex creates an empty index.
func NewIndex() *Index {
	return &Index{
		docs:     make(map[string]*docData),
		inverted: make(map[string]map[string]struct{}),
	}
}

// Add registers a document.
func (ix *Index) Add(docID string, terms []string) error {
	if docID == "" || len(terms) == 0 {
		return ErrInvalidArguments
	}
	for _, term := range terms {
		if term == "" {
			return ErrInvalidArguments
		}
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, ok := ix.docs[docID]; ok {
		return ErrDuplicateDoc
	}
	d := &docData{postings: make(map[string][]int)}
	for pos, term := range terms {
		if term == Boundary {
			d.bounds = append(d.bounds, pos)
			continue
		}
		d.postings[term] = append(d.postings[term], pos)
	}
	ix.docs[docID] = d
	for term := range d.postings {
		set, ok := ix.inverted[term]
		if !ok {
			set = make(map[string]struct{})
			ix.inverted[term] = set
		}
		set[docID] = struct{}{}
	}
	return nil
}

// Delete removes a document.
func (ix *Index) Delete(docID string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	d, ok := ix.docs[docID]
	if !ok {
		return ErrDocNotFound
	}
	for term := range d.postings {
		if set, ok := ix.inverted[term]; ok {
			delete(set, docID)
			if len(set) == 0 {
				delete(ix.inverted, term)
			}
		}
	}
	delete(ix.docs, docID)
	return nil
}

// Phrase returns the top-k documents containing matches of query.
func (ix *Index) Phrase(query []string, slop, maxGap, k int) ([]Result, error) {
	if len(query) == 0 || len(query) > 8 {
		return nil, ErrInvalidQuery
	}
	for _, term := range query {
		if term == "" || term == Boundary {
			return nil, ErrInvalidQuery
		}
	}
	if slop < 0 || slop > 100 || maxGap < 0 || maxGap > 100 {
		return nil, ErrInvalidGap
	}
	if k < 1 {
		return nil, ErrInvalidK
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	// Distinct query terms share one posting slice; the read counter is
	// therefore bounded by the summed posting lengths of distinct terms.
	terms := distinctTerms(query)
	candidates := ix.candidateDocs(terms)
	results := make([]Result, 0)
	for _, docID := range candidates {
		d := ix.docs[docID]
		posts := make([][]int, len(terms))
		for i, term := range terms {
			p := d.postings[term]
			ix.readCnt.Add(int64(len(p)))
			posts[i] = p
		}
		byTerm := make(map[string][]int, len(terms))
		for i, term := range terms {
			byTerm[term] = posts[i]
		}
		occ := make([][]int, len(query))
		for i, term := range query {
			occ[i] = byTerm[term]
		}
		freq, minGap := matchDoc(occ, d.bounds, slop, maxGap)
		if freq > 0 {
			results = append(results, Result{DocID: docID, Freq: freq, MinGap: minGap})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Freq != results[j].Freq {
			return results[i].Freq > results[j].Freq
		}
		if results[i].MinGap != results[j].MinGap {
			return results[i].MinGap < results[j].MinGap
		}
		return results[i].DocID < results[j].DocID
	})
	if len(results) > k {
		results = results[:k]
	}
	return results, nil
}

func distinctTerms(query []string) []string {
	seen := make(map[string]struct{}, len(query))
	out := make([]string, 0, len(query))
	for _, term := range query {
		if _, ok := seen[term]; !ok {
			seen[term] = struct{}{}
			out = append(out, term)
		}
	}
	return out
}

// candidateDocs returns docIDs containing every distinct query term, starting
// from the rarest term's posting set.
func (ix *Index) candidateDocs(terms []string) []string {
	var rarest map[string]struct{}
	best := -1
	for _, term := range terms {
		set, ok := ix.inverted[term]
		if !ok {
			return nil
		}
		if best < 0 || len(set) < best {
			best = len(set)
			rarest = set
		}
	}
	out := make([]string, 0, len(rarest))
	for docID := range rarest {
		d := ix.docs[docID]
		containsAll := true
		for _, term := range terms {
			if _, ok := d.postings[term]; !ok {
				containsAll = false
				break
			}
		}
		if containsAll {
			out = append(out, docID)
		}
	}
	sort.Strings(out)
	return out
}

// matchDoc returns (number of distinct first positions with a valid match,
// minimum total gap over all valid matches) for one document.
func matchDoc(occ [][]int, bounds []int, slop, maxGap int) (int, int) {
	n := len(occ)
	if n == 1 {
		// A one-term match never spans a boundary and always has gap 0.
		return len(occ[0]), 0
	}

	// Matches are independent inside each sentence segment delimited by
	// boundary positions.
	minGapByStart := make(map[int]int)
	segStart, segEnd := 0, 0
	segNext := 0
	for segment := 0; ; segment++ {
		if segment < len(bounds) {
			segStart, segEnd = segNext, bounds[segment]
			segNext = bounds[segment] + 1
		} else {
			segStart, segEnd = segNext, -1
		}
		if segStart < 0 {
			break
		}
		matchSegment(occ, segStart, segEnd, slop, maxGap, minGapByStart)
		if segEnd < 0 {
			break
		}
	}
	if len(minGapByStart) == 0 {
		return 0, 0
	}
	best := -1
	for _, gap := range minGapByStart {
		if best < 0 || gap < best {
			best = gap
		}
	}
	return len(minGapByStart), best
}

// matchSegment evaluates all matches contained in one sentence segment
// [segStart, segEnd) (segEnd < 0 means open-ended).
//
// For each candidate first position s, it runs a left-to-right reachability
// DP over the remaining query terms: the earliest reachable occurrence of each
// term is tracked using a two-pointer window of width maxGap. If the last term
// is reached with total gap <= slop (total gap depends only on first and last
// position), s is counted once, with the minimum total gap across ends.
func matchSegment(occ [][]int, segStart, segEnd, slop, maxGap int, minGapByStart map[int]int) {
	n := len(occ)
	starts := occ[0]
	s0 := sort.SearchInts(starts, segStart)
	var s1 int
	if segEnd < 0 {
		s1 = len(starts)
	} else {
		s1 = sort.SearchInts(starts, segEnd)
	}
	// Reusable reachability row; every position strictly within the segment
	// starts reachable=false.
	reach := make([][]bool, n)
	for j := 0; j < n; j++ {
		reach[j] = make([]bool, len(occ[j]))
	}
	for si := s0; si < s1; si++ {
		startPos := starts[si]
		for j := 0; j < n; j++ {
			clear(reach[j])
		}
		reach[0][si] = true
		for j := 1; j < n; j++ {
			prev := occ[j-1]
			cur := occ[j]
			// Window [w0, w1) of prev occurrences within maxGap of cur[e].
			w0, w1 := 0, 0
			for e, pos := range cur {
				if segEnd >= 0 && pos >= segEnd {
					break
				}
				for w0 < len(prev) && prev[w0] < pos-maxGap-1 {
					w0++
				}
				if w1 < w0 {
					w1 = w0
				}
				for w1 < len(prev) && prev[w1] <= pos-1 {
					w1++
				}
				for q := w0; q < w1; q++ {
					if reach[j-1][q] {
						reach[j][e] = true
						break
					}
				}
			}
		}

		// Total gap depends only on the endpoints; scan ends ascending so the
		// first reachable one within the slop window gives the minimum gap.
		ends := occ[n-1]
		limit := startPos + slop + (n - 1)
		for e, endPos := range ends {
			if segEnd >= 0 && endPos >= segEnd {
				break
			}
			if endPos < startPos+(n-1) {
				continue
			}
			if endPos > limit {
				break
			}
			if reach[n-1][e] {
				gap := endPos - startPos - (n - 1)
				if prev, ok := minGapByStart[startPos]; !ok || gap < prev {
					minGapByStart[startPos] = gap
				}
				break
			}
		}
	}
}

// PostingReads returns the total number of inverted-list position entries read
// by all Phrase calls since the index was created.
func (ix *Index) PostingReads() int {
	return int(ix.readCnt.Load())
}
