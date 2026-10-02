// Package expander implements a type-ahead query expander with a
// recency-windowed pick bonus.
package expander

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// Rejection reasons. Each rejected operation leaves the index, the pick
// records and the counter T untouched, and only the first applicable
// reason (in the order listed per operation) is reported.
var (
	// ErrInvalidArgument: Index docID empty, terms empty, term empty or
	// containing 0x20; Pick term empty or containing 0x20.
	ErrInvalidArgument = errors.New("expander: invalid argument")
	// ErrDuplicateDoc: Index docID already registered.
	ErrDuplicateDoc = errors.New("expander: duplicate document")
	// ErrDocNotFound: Remove docID not registered.
	ErrDocNotFound = errors.New("expander: document not found")
	// ErrTermNotFound: Pick term not present in any document.
	ErrTermNotFound = errors.New("expander: term not found")
	// ErrEmptyQuery: Expand query empty or all spaces.
	ErrEmptyQuery = errors.New("expander: empty query")
	// ErrQueryTooLong: Expand query has more than 8 words.
	ErrQueryTooLong = errors.New("expander: query too long")
	// ErrInvalidMaxExp: Expand maxExp outside [1, 1000].
	ErrInvalidMaxExp = errors.New("expander: invalid maxExp")
)

const (
	// MaxWords is the maximum number of words (counting duplicates)
	// allowed in a query.
	MaxWords = 8
	// MaxExpLimit is the maximum allowed value of maxExp.
	MaxExpLimit = 1000
	// PickWindow is the sliding validity window of a pick: a pick made
	// at counter value c counts for Expand number t while t-c <= 3.
	PickWindow = 3
	// PickBonus is the score bonus per effective pick.
	PickBonus = 2
)

// Candidate is one expanded term with its conditional document
// frequency and final score.
type Candidate struct {
	Term  string
	DF    int
	Score int
}

// Result is the outcome of a successful Expand.
type Result struct {
	Candidates []Candidate
	Truncated  bool
	Docs       []string
}

// Merger holds the document index, the pick records and the counter T.
// All methods are safe for concurrent use; the result is equivalent to
// some serial order of the calls.
type Merger struct {
	mu    sync.Mutex
	docs  map[string]map[string]struct{}
	picks map[string]map[int]struct{}
	t     int
}

// New returns an empty Merger.
func New() *Merger {
	return &Merger{
		docs:  make(map[string]map[string]struct{}),
		picks: make(map[string]map[int]struct{}),
	}
}

// Index registers a document. terms are deduplicated within the
// document.
func (m *Merger) Index(docID string, terms []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if docID == "" || len(terms) == 0 {
		return ErrInvalidArgument
	}
	set := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		if !validTerm(term) {
			return ErrInvalidArgument
		}
		set[term] = struct{}{}
	}
	if _, ok := m.docs[docID]; ok {
		return ErrDuplicateDoc
	}
	m.docs[docID] = set
	return nil
}

// Remove deletes a document.
func (m *Merger) Remove(docID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.docs[docID]; !ok {
		return ErrDocNotFound
	}
	delete(m.docs, docID)
	return nil
}

// Pick records a pick of term at the current counter value.
func (m *Merger) Pick(term string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !validTerm(term) {
		return ErrInvalidArgument
	}
	if !m.termExists(term) {
		return ErrTermNotFound
	}
	cs, ok := m.picks[term]
	if !ok {
		cs = make(map[int]struct{})
		m.picks[term] = cs
	}
	cs[m.t] = struct{}{}
	return nil
}

// Expand parses query, expands the trailing prefix word into at most
// maxExp candidates and returns the hit documents.
func (m *Merger) Expand(query string, maxExp int) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	words := strings.FieldsFunc(query, func(r rune) bool { return r == ' ' })
	if len(words) == 0 {
		return Result{}, ErrEmptyQuery
	}
	if len(words) > MaxWords {
		return Result{}, ErrQueryTooLong
	}
	if maxExp < 1 || maxExp > MaxExpLimit {
		return Result{}, ErrInvalidMaxExp
	}

	m.t++
	t := m.t

	complete := words
	prefix := ""
	if !strings.HasSuffix(query, " ") {
		complete = words[:len(words)-1]
		prefix = words[len(words)-1]
	}

	cond := m.conditionalDocs(complete)

	if prefix == "" {
		return Result{Candidates: []Candidate{}, Docs: cond}, nil
	}

	df := make(map[string]int)
	for _, docID := range cond {
		for term := range m.docs[docID] {
			if strings.HasPrefix(term, prefix) {
				df[term]++
			}
		}
	}

	cands := make([]Candidate, 0, len(df))
	for term, d := range df {
		cands = append(cands, Candidate{
			Term:  term,
			DF:    d,
			Score: d + PickBonus*m.effectivePicks(term, t),
		})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Score != cands[j].Score {
			return cands[i].Score > cands[j].Score
		}
		return cands[i].Term < cands[j].Term
	})

	truncated := len(cands) > maxExp
	if truncated {
		cands = cands[:maxExp]
	}

	kept := make(map[string]struct{}, len(cands))
	for _, c := range cands {
		kept[c.Term] = struct{}{}
	}
	hits := make([]string, 0, len(cond))
	for _, docID := range cond {
		for term := range m.docs[docID] {
			if _, ok := kept[term]; ok {
				hits = append(hits, docID)
				break
			}
		}
	}

	return Result{Candidates: cands, Truncated: truncated, Docs: hits}, nil
}

// validTerm reports whether term is a non-empty string without 0x20.
func validTerm(term string) bool {
	return term != "" && !strings.ContainsRune(term, ' ')
}

// termExists reports whether term appears in at least one document.
func (m *Merger) termExists(term string) bool {
	for _, set := range m.docs {
		if _, ok := set[term]; ok {
			return true
		}
	}
	return false
}

// conditionalDocs returns the sorted IDs of documents containing all
// deduplicated complete words, or all documents when there are none.
func (m *Merger) conditionalDocs(complete []string) []string {
	required := make(map[string]struct{}, len(complete))
	for _, w := range complete {
		required[w] = struct{}{}
	}
	out := make([]string, 0, len(m.docs))
	for docID, set := range m.docs {
		ok := true
		for w := range required {
			if _, has := set[w]; !has {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, docID)
		}
	}
	sort.Strings(out)
	return out
}

// effectivePicks counts the pick records of term valid at Expand
// number t, i.e. records with t-c <= PickWindow.
func (m *Merger) effectivePicks(term string, t int) int {
	n := 0
	for c := range m.picks[term] {
		if t-c <= PickWindow {
			n++
		}
	}
	return n
}
