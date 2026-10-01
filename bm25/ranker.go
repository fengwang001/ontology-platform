package bm25

import (
	"sync"
	"sync/atomic"
)

// Result is one ranked document. Score is always "numerator/denominator".
type Result struct {
	DocID string
	Score string
}

type interval struct {
	start int
	end   int
	tf    int
}

type docState struct {
	start int
	end   int
	dl    int
}

type dfEvent struct {
	version int
	delta   int
}

type globalEvent struct {
	version int
	deltaN  int
	deltaL  int
}

type termData struct {
	active   map[string]*interval
	archive  map[string][]*interval
	dfEvents []dfEvent
}

type globalStats struct {
	n int
	l int
}

// Ranker is a versioned BM25 index. A read-write lock gives a serializable
// total order; Search observes one immutable version snapshot.
type Ranker struct {
	mu        sync.RWMutex
	version   int
	watermark int

	docs         map[string]*docState
	docArchive   map[string][]*docState
	currentTerms map[string]map[string]int
	terms        map[string]*termData
	global       []globalEvent

	lastSearchPostingReads atomic.Int64
}

func NewRanker() *Ranker {
	return &Ranker{
		docs:         make(map[string]*docState),
		docArchive:   make(map[string][]*docState),
		currentTerms: make(map[string]map[string]int),
		terms:        make(map[string]*termData),
	}
}

func (r *Ranker) Version() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

func (r *Ranker) Watermark() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.watermark
}

// LastSearchPostingReads reports current inverted-list entries read by the
// most recent current-version Search. Historical searches record zero.
func (r *Ranker) LastSearchPostingReads() int {
	return int(r.lastSearchPostingReads.Load())
}

func invalidTerms(terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	for _, term := range terms {
		if term == "" {
			return true
		}
	}
	return false
}

func termCounts(terms []string) map[string]int {
	counts := make(map[string]int, len(terms))
	for _, term := range terms {
		counts[term]++
	}
	return counts
}

func uniqueTerms(terms []string) []string {
	seen := make(map[string]struct{}, len(terms))
	unique := make([]string, 0, len(terms))
	for _, term := range terms {
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		unique = append(unique, term)
	}
	return unique
}

func intervalContains(value *interval, version int) bool {
	return value.start <= version && (value.end == 0 || version < value.end)
}
