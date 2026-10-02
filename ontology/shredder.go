package ontology

import (
	"sync"
	"sync/atomic"
)

// Shredder shreds nested records into leaf columns and reassembles them.
type Shredder struct {
	mu          sync.RWMutex
	schema      []Field
	pageEntries int
	maxEntries  int
	leaves      []leafPath
	leafCols    []*column
	colByName   map[string]*column
	recordCount int
	entriesRead atomic.Int64
}

type colMeta struct {
	path   string
	maxRep int
	maxDef int
}

type column struct {
	colMeta
	entries []Entry
	// openPage describes the page currently being filled; all prior
	// pages are finalized in pages.
	pages      []PageInfo
	openPage   *PageInfo
	nulls      int
	present    int
	min        int64
	max        int64
	hasPresent bool
}

// New validates the schema and parameters and returns a Shredder.
func New(schema []Field, pageEntries, maxEntries int) (*Shredder, error) {
	if pageEntries < 1 || maxEntries < 1 {
		return nil, ErrParam
	}
	leaves, err := validateSchema(schema)
	if err != nil {
		return nil, err
	}
	s := &Shredder{
		schema:      append([]Field(nil), schema...),
		pageEntries: pageEntries,
		maxEntries:  maxEntries,
		colByName:   make(map[string]*column),
	}
	for _, lp := range leaves {
		s.leaves = append(s.leaves, lp)
		c := &column{
			colMeta: colMeta{path: lp.path, maxRep: lp.maxRep, maxDef: lp.maxDef},
		}
		s.leafCols = append(s.leafCols, c)
		s.colByName[lp.path] = c
	}
	return s, nil
}

// Shred appends one record atomically.
func (s *Shredder) Shred(rec map[string]any) error {
	if err := validateRecord(s.schema, rec); err != nil {
		return err
	}
	produced := make([][]Entry, len(s.leaves))
	for i, lp := range s.leaves {
		es := emitColumn(lp, rec)
		if len(es) > s.maxEntries {
			return fieldErr(ErrTooLarge, lp.path)
		}
		produced[i] = es
	}

	s.mu.Lock()
	recIdx := s.recordCount
	for i, es := range produced {
		s.leafCols[i].appendRecord(es, recIdx, s.pageEntries)
	}
	s.recordCount++
	s.mu.Unlock()
	return nil
}

// Columns returns leaf column paths in schema DFS order.
func (s *Shredder) Columns() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.leafCols))
	for i, c := range s.leafCols {
		out[i] = c.path
	}
	return out
}

// Entries returns a copy of all entries of a column.
func (s *Shredder) Entries(col string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.colByName[col]
	if c == nil {
		return nil
	}
	return append([]Entry(nil), c.entries...)
}

// Pages returns the pages of a column, including the still-open final page.
func (s *Shredder) Pages(col string) []PageInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.colByName[col]
	if c == nil {
		return nil
	}
	out := make([]PageInfo, 0, len(c.pages)+1)
	out = append(out, c.pages...)
	if c.openPage != nil {
		out = append(out, *c.openPage)
	}
	return out
}

// Stats returns column statistics.
func (s *Shredder) Stats(col string) Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.colByName[col]
	if c == nil {
		return Stats{}
	}
	return Stats{Nulls: c.nulls, Present: c.present, Min: c.min, Max: c.max}
}

// RecordCount returns the number of successfully shredded records.
func (s *Shredder) RecordCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recordCount
}

// EntriesRead returns the total number of entries consumed by Assemble calls.
func (s *Shredder) EntriesRead() int64 {
	return s.entriesRead.Load()
}
