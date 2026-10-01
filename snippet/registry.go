package snippet

import (
	"sort"
	"sync"
	"unicode/utf8"
)

// maxTextBytes is the maximum legal document text length in UTF-8 bytes.
const maxTextBytes = 1 << 20

const maxHits = 10000

// Interval is a byte-offset half-open interval [Start, End).
type Interval struct {
	Start int
	End   int
}

// Snippet is one selected window with its merged highlight intervals and
// selection score (number of distinct hits covered by the window).
type Snippet struct {
	Start      int
	End        int
	Highlights []Interval
	Score      int
}

type document struct {
	text     string
	boundary []bool
}

// Registry stores registered documents keyed by docID.
// All methods are safe for concurrent use; their effects are equivalent to
// some serial execution order.
type Registry struct {
	mu   sync.RWMutex
	docs map[string]document
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{docs: make(map[string]document)}
}

// Register stores text under docID.
//
// Rejection order: empty docID / invalid or over-long text
// (ReasonInvalidArgument), then an already-existing docID
// (ReasonDuplicateDoc).
func (r *Registry) Register(docID string, text string) error {
	if docID == "" {
		return &RejectError{ReasonInvalidArgument, "docID must not be empty"}
	}
	if !utf8.ValidString(text) {
		return &RejectError{ReasonInvalidArgument, "text is not valid UTF-8"}
	}
	if len(text) > maxTextBytes {
		return &RejectError{ReasonInvalidArgument, "text exceeds 1048576 bytes"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.docs[docID]; ok {
		return &RejectError{ReasonDuplicateDoc, "docID already registered: " + docID}
	}
	// Copy text so the caller cannot mutate registered state through the
	// backing string after the call.
	stored := string(append([]byte(nil), text...))
	r.docs[docID] = document{text: stored, boundary: utf8Boundaries(stored)}
	return nil
}

// Unregister removes the document stored under docID.
func (r *Registry) Unregister(docID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.docs[docID]; !ok {
		return &RejectError{ReasonDocNotFound, "docID not registered: " + docID}
	}
	delete(r.docs, docID)
	return nil
}

// Snippets selects up to K non-overlapping snippets for docID from the given
// byte-offset hits and window width W.
//
// Rejection order: unknown docID (ReasonDocNotFound), then W/K range or
// over-10000 hits (ReasonInvalidArgument), then any out-of-range, empty or
// boundary-crossing hit (ReasonInvalidHit). hits is validated before
// deduplication, so duplicates count toward the 10000 limit.
func (r *Registry) Snippets(docID string, hits []Interval, W, K int) ([]Snippet, error) {
	r.mu.RLock()
	doc, ok := r.docs[docID]
	r.mu.RUnlock()
	if !ok {
		return nil, &RejectError{ReasonDocNotFound, "docID not registered: " + docID}
	}
	if W < 1 || W > 4096 {
		return nil, &RejectError{ReasonInvalidArgument, "W must be in [1, 4096]"}
	}
	if K < 1 || K > 16 {
		return nil, &RejectError{ReasonInvalidArgument, "K must be in [1, 16]"}
	}
	if len(hits) > maxHits {
		return nil, &RejectError{ReasonInvalidArgument, "more than 10000 hits"}
	}
	textLen := len(doc.text)
	for _, h := range hits {
		if h.Start < 0 || h.End > textLen || h.Start >= h.End {
			return nil, &RejectError{ReasonInvalidHit, "hit out of range or empty"}
		}
		if !doc.boundary[h.Start] || !doc.boundary[h.End] {
			return nil, &RejectError{ReasonInvalidHit, "hit endpoint splits a multi-byte rune"}
		}
	}

	// Dedup exact (s,e) pairs; order-independent via sorting.
	unique := make([]Interval, len(hits))
	copy(unique, hits)
	sort.Slice(unique, func(i, j int) bool {
		if unique[i].Start != unique[j].Start {
			return unique[i].Start < unique[j].Start
		}
		return unique[i].End < unique[j].End
	})
	unique = unique[:sortDedup(unique)]

	selected := selectSnippets(unique, W, K, textLen)

	// Return by Start ascending (selection order may differ) and copy every
	// slice so the result never aliases internal state.
	if len(selected) == 0 {
		return nil, nil
	}
	out := make([]Snippet, len(selected))
	copy(out, selected)
	return out, nil
}

// sortDedup removes adjacent duplicate intervals from the sorted slice in
// place and returns the new length.
func sortDedup(sorted []Interval) int {
	if len(sorted) == 0 {
		return 0
	}
	n := 1
	for i := 1; i < len(sorted); i++ {
		if sorted[i] != sorted[n-1] {
			sorted[n] = sorted[i]
			n++
		}
	}
	return n
}
