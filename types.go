package ontology

import (
	"sync"
)

type Error string

const (
	ErrInvalidArgument   Error = "invalid argument"
	ErrDuplicateDoc      Error = "duplicate document"
	ErrDocNotFound       Error = "document not found"
	ErrVersionRange      Error = "version out of range"
	ErrWatermarkRollback Error = "watermark rollback"
	ErrVersionReclaimed  Error = "version reclaimed"
)

func (e Error) Error() string { return string(e) }

type Result struct {
	DocID string
	Score string
}

type globalStat struct{ n, l int }

type point struct {
	version int
	value   int
}

type boolPoint struct {
	version int
	value   bool
}

type docRevision struct {
	version int
	exists  bool
	terms   []string
}

type docState struct {
	baselineVersion int
	baselineExists  bool
	baselineTerms   []string
	changes         []docRevision
}

type termState struct {
	baselineVersion int
	baselineDF      int
	changes         []point
	postings        map[string]*postingState
}

type postingState struct {
	baselineVersion int
	baselineExists  bool
	changes         []boolPoint
}

type Index struct {
	mu        sync.RWMutex
	version   int
	watermark int

	statsBaseVersion int
	stats            []globalStat
	docs             map[string]*docState
	terms            map[string]*termState

	currentPostingReads int64
}

func NewIndex() *Index {
	return &Index{
		stats: []globalStat{{}},
		docs:  make(map[string]*docState),
		terms: make(map[string]*termState),
	}
}

func (idx *Index) Version() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.version
}

func (idx *Index) Watermark() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.watermark
}
