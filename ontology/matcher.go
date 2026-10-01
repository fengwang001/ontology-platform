package ontology

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"
)

const sentenceBoundary = "<S>"

var (
	ErrInvalidDocument = errors.New("invalid document")
	ErrDuplicateDocID  = errors.New("duplicate document id")
	ErrDocNotFound     = errors.New("document not found")
	ErrInvalidQuery    = errors.New("invalid query")
	ErrInvalidGap      = errors.New("invalid gap")
	ErrInvalidK        = errors.New("invalid k")
)

type document struct {
	terms      []string
	postings   map[string][]int
	boundaries []int
}

type Matcher struct {
	mu        sync.RWMutex
	docs      map[string]*document
	inverted  map[string]map[string]*document
	readItems atomic.Uint64
}

type Match struct {
	DocID  string
	Freq   int
	MinGap int
}

func NewMatcher() *Matcher {
	return &Matcher{
		docs:     make(map[string]*document),
		inverted: make(map[string]map[string]*document),
	}
}

func (m *Matcher) Add(docID string, terms []string) error {
	if docID == "" || len(terms) == 0 {
		return ErrInvalidDocument
	}
	copied := make([]string, len(terms))
	postings := make(map[string][]int)
	var boundaries []int
	for i, term := range terms {
		if term == "" {
			return ErrInvalidDocument
		}
		copied[i] = term
		if term == sentenceBoundary {
			boundaries = append(boundaries, i)
		} else {
			postings[term] = append(postings[term], i)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.docs[docID]; exists {
		return ErrDuplicateDocID
	}

	doc := &document{
		terms:      copied,
		postings:   postings,
		boundaries: boundaries,
	}
	m.docs[docID] = doc
	for term := range postings {
		if m.inverted[term] == nil {
			m.inverted[term] = make(map[string]*document)
		}
		m.inverted[term][docID] = doc
	}
	return nil
}

func (m *Matcher) Delete(docID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	doc, exists := m.docs[docID]
	if !exists {
		return ErrDocNotFound
	}
	for term := range doc.postings {
		if postingDocs := m.inverted[term]; postingDocs != nil {
			delete(postingDocs, docID)
			if len(postingDocs) == 0 {
				delete(m.inverted, term)
			}
		}
	}
	delete(m.docs, docID)
	return nil
}

func (m *Matcher) Phrase(query []string, slop, maxGap, k int) ([]Match, error) {
	if len(query) == 0 || len(query) > 8 {
		return nil, ErrInvalidQuery
	}
	for _, term := range query {
		if term == "" || term == sentenceBoundary {
			return nil, ErrInvalidQuery
		}
	}
	if slop < 0 || slop > 100 || maxGap < 0 || maxGap > 100 {
		return nil, ErrInvalidGap
	}
	if k < 1 {
		return nil, ErrInvalidK
	}

	uniqueTerms := make([]string, 0, len(query))
	seenTerms := make(map[string]struct{}, len(query))
	for _, term := range query {
		if _, seen := seenTerms[term]; !seen {
			seenTerms[term] = struct{}{}
			uniqueTerms = append(uniqueTerms, term)
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	rarestTerm := uniqueTerms[0]
	rarestDocs := m.inverted[rarestTerm]
	for _, term := range uniqueTerms[1:] {
		postingDocs := m.inverted[term]
		if len(postingDocs) < len(rarestDocs) {
			rarestTerm = term
			rarestDocs = postingDocs
		}
	}
	if len(rarestDocs) == 0 {
		return []Match{}, nil
	}

	results := make([]Match, 0)
	for docID, doc := range rarestDocs {
		if doc != m.docs[docID] {
			continue
		}
		positionsByTerm := make(map[string][]int, len(uniqueTerms))
		containsAll := true
		for _, term := range uniqueTerms {
			positions := doc.postings[term]
			if len(positions) == 0 {
				containsAll = false
				break
			}
			positionsByTerm[term] = positions
			m.readItems.Add(uint64(len(positions)))
		}
		if !containsAll {
			continue
		}

		queryPositions := make([][]int, len(query))
		for i, term := range query {
			queryPositions[i] = positionsByTerm[term]
		}
		freq, minGap := matchDocument(queryPositions, doc.boundaries, len(doc.terms), slop, maxGap)
		if freq > 0 {
			results = append(results, Match{DocID: docID, Freq: freq, MinGap: minGap})
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

func matchDocument(postings [][]int, boundaries []int, docLen, slop, maxGap int) (int, int) {
	if len(postings) == 1 {
		return len(postings[0]), 0
	}

	freq := 0
	minGap := 0
	for _, start := range postings[0] {
		boundaryIndex := sort.SearchInts(boundaries, start)
		sentenceEnd := docLen
		if boundaryIndex < len(boundaries) {
			sentenceEnd = boundaries[boundaryIndex]
		}
		hardEnd := start + len(postings) - 1 + slop
		if hardEnd >= sentenceEnd {
			hardEnd = sentenceEnd - 1
		}
		if hardEnd < start+len(postings)-1 {
			continue
		}

		reachable := []int{start}
		for layer := 1; layer < len(postings); layer++ {
			next := make([]int, 0, len(reachable))
			seen := make(map[int]struct{}, len(reachable))
			positions := postings[layer]
			for _, prev := range reachable {
				low := sort.SearchInts(positions, prev+1)
				lastAllowed := prev + maxGap + 1
				if lastAllowed > hardEnd {
					lastAllowed = hardEnd
				}
				high := sort.SearchInts(positions, lastAllowed+1)
				for _, pos := range positions[low:high] {
					if _, exists := seen[pos]; !exists {
						seen[pos] = struct{}{}
						next = append(next, pos)
					}
				}
			}
			if len(next) == 0 {
				reachable = nil
				break
			}
			reachable = next
		}
		if len(reachable) == 0 {
			continue
		}

		end := reachable[0]
		for _, pos := range reachable[1:] {
			if pos < end {
				end = pos
			}
		}
		gap := end - start - (len(postings) - 1)
		if freq == 0 || gap < minGap {
			minGap = gap
		}
		freq++
	}
	return freq, minGap
}

func (m *Matcher) readPostingItems() uint64 {
	return m.readItems.Load()
}

func (m *Matcher) resetReadPostingItems() {
	m.readItems.Store(0)
}
