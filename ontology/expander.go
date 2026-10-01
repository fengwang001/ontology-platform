package ontology

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrDuplicateDocID  = errors.New("duplicate document id")
	ErrDocNotFound     = errors.New("document not found")
	ErrTermNotFound    = errors.New("term not found")
	ErrEmptyQuery      = errors.New("empty query")
	ErrQueryTooLong    = errors.New("query too long")
	ErrInvalidMaxExp   = errors.New("invalid maxExp")
)

type Candidate struct {
	Term          string
	ConditionalDF int
	Score         int
}

type ExpandResult struct {
	Candidates []Candidate
	Truncated  bool
	Hits       []string
}

type pickRecord struct {
	term string
	t    int
}

type QueryExpander struct {
	mu    sync.RWMutex
	docs  map[string]map[string]struct{}
	picks []pickRecord
	t     int
}

func NewQueryExpander() *QueryExpander {
	return &QueryExpander{}
}

func (e *QueryExpander) Index(docID string, terms []string) error {
	if docID == "" {
		return ErrInvalidArgument
	}
	if len(terms) == 0 {
		return ErrInvalidArgument
	}
	for _, term := range terms {
		if term == "" || strings.ContainsRune(term, ' ') {
			return ErrInvalidArgument
		}
	}

	uniqueTerms := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		uniqueTerms[term] = struct{}{}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.docs[docID]; ok {
		return ErrDuplicateDocID
	}
	if e.docs == nil {
		e.docs = make(map[string]map[string]struct{})
	}
	e.docs[docID] = uniqueTerms
	return nil
}

func (e *QueryExpander) Remove(docID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.docs[docID]; !ok {
		return ErrDocNotFound
	}
	delete(e.docs, docID)
	return nil
}

func (e *QueryExpander) Pick(term string) error {
	if term == "" || strings.ContainsRune(term, ' ') {
		return ErrInvalidArgument
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.termExistsLocked(term) {
		return ErrTermNotFound
	}
	for _, record := range e.picks {
		if record.term == term && record.t == e.t {
			return nil
		}
	}
	e.picks = append(e.picks, pickRecord{term: term, t: e.t})
	return nil
}

func (e *QueryExpander) Expand(query string, maxExp int) (ExpandResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	words, hasPrefix, err := parseQuery(query)
	if err != nil {
		return ExpandResult{}, err
	}
	if len(words) > 8 {
		return ExpandResult{}, ErrQueryTooLong
	}
	if maxExp < 1 || maxExp > 1000 {
		return ExpandResult{}, ErrInvalidMaxExp
	}

	e.t++
	expandT := e.t
	completeWords := words
	prefix := ""
	if hasPrefix {
		completeWords = words[:len(words)-1]
		prefix = words[len(words)-1]
	}

	conditionalDocs := e.conditionalDocsLocked(completeWords)
	if prefix == "" {
		return ExpandResult{
			Candidates: []Candidate{},
			Hits:       sortedDocIDs(conditionalDocs),
		}, nil
	}

	df := make(map[string]int)
	for docID := range conditionalDocs {
		for term := range e.docs[docID] {
			if strings.HasPrefix(term, prefix) {
				df[term]++
			}
		}
	}

	effectivePicks := make(map[string]int)
	for _, record := range e.picks {
		if expandT-record.t <= 3 {
			effectivePicks[record.term]++
		}
	}

	candidates := make([]Candidate, 0, len(df))
	for term, conditionalDF := range df {
		candidates = append(candidates, Candidate{
			Term:          term,
			ConditionalDF: conditionalDF,
			Score:         conditionalDF + 2*effectivePicks[term],
		})
	}
	sortCandidates(candidates)

	truncated := len(candidates) > maxExp
	if truncated {
		candidates = candidates[:maxExp]
	}

	retained := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		retained[candidate.Term] = struct{}{}
	}
	hits := make([]string, 0)
	for docID := range conditionalDocs {
		for term := range retained {
			if _, ok := e.docs[docID][term]; ok {
				hits = append(hits, docID)
				break
			}
		}
	}
	sort.Strings(hits)

	return ExpandResult{
		Candidates: candidates,
		Truncated:  truncated,
		Hits:       hits,
	}, nil
}

func (e *QueryExpander) termExistsLocked(term string) bool {
	for _, docTerms := range e.docs {
		if _, ok := docTerms[term]; ok {
			return true
		}
	}
	return false
}

func parseQuery(query string) (words []string, hasPrefix bool, err error) {
	for _, word := range strings.Split(query, " ") {
		if word != "" {
			words = append(words, word)
		}
	}
	if len(words) == 0 {
		return nil, false, ErrEmptyQuery
	}
	return words, query[len(query)-1] != ' ', nil
}

func (e *QueryExpander) conditionalDocsLocked(completeWords []string) map[string]struct{} {
	docs := make(map[string]struct{}, len(e.docs))
	for docID := range e.docs {
		docs[docID] = struct{}{}
	}
	for _, word := range completeWords {
		for docID := range docs {
			if _, ok := e.docs[docID][word]; !ok {
				delete(docs, docID)
			}
		}
	}
	return docs
}

func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Term < candidates[j].Term
	})
}

func sortedDocIDs(docs map[string]struct{}) []string {
	docIDs := make([]string, 0, len(docs))
	for docID := range docs {
		docIDs = append(docIDs, docID)
	}
	sort.Strings(docIDs)
	return docIDs
}
