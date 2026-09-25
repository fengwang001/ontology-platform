// Package evol implements record writing and reading over a sch.Catalog,
// including write-time-frozen default fallback.
package evol

import (
	"errors"
	"sync"

	"ontology/sch"
)

// Sentinel errors: the three rejection kinds are pairwise distinct.
var (
	ErrBadWriteVersion = errors.New("evol: write version out of range")
	ErrFieldOutOfScope = errors.New("evol: value for field outside schema[W]")
	ErrBadReadVersion  = errors.New("evol: read version out of range")
)

// Record is an immutable written record: writer version W plus explicit
// values, always a subset of schema[W].
type Record struct {
	W      int
	values map[string]int
}

// Values returns a copy of the record's explicit values.
func (r *Record) Values() map[string]int {
	out := make(map[string]int, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out
}

// Engine owns the in-process record store.
type Engine struct {
	cat *sch.Catalog

	mu   sync.RWMutex
	recs []*Record
}

// NewEngine builds an engine over the given schema catalog.
func NewEngine(cat *sch.Catalog) *Engine { return &Engine{cat: cat} }

// Write validates W and every value key before touching any state, then stores
// one record and returns it. A rejected write changes nothing.
func (e *Engine) Write(W int, values map[string]int) (*Record, error) {
	if W < 1 || W > e.cat.N() {
		return nil, ErrBadWriteVersion
	}
	// Validate fully before mutation: empty names and out-of-scope fields.
	for f := range values {
		if f == "" || !e.cat.Has(W, f) {
			return nil, ErrFieldOutOfScope
		}
	}
	rec := &Record{W: W, values: make(map[string]int, len(values))}
	for f, v := range values {
		rec.values[f] = v
	}
	e.mu.Lock()
	e.recs = append(e.recs, rec)
	e.mu.Unlock()
	return rec, nil
}

// Read resolves rec under reader version R: explicit value wins for each
// schema[R] field, otherwise the writer-frozen default; unknown writer fields
// never enter the result. It is read-only and safe for concurrent use.
func (e *Engine) Read(rec *Record, R int) (map[string]int, error) {
	if R < 1 || R > e.cat.N() {
		return nil, ErrBadReadVersion
	}
	fields := e.cat.Fields(R)
	out := make(map[string]int, len(fields))
	for _, f := range fields {
		if v, ok := rec.values[f]; ok {
			out[f] = v
		} else {
			out[f] = e.cat.Def(f, rec.W)
		}
	}
	return out, nil
}

// count reports stored records; unexported, tests in this package only.
func (e *Engine) count() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.recs)
}
