// Package sch holds the versioned schema table: per-version field sets and
// defaults, plus O(1) lookup of the writer-time default def(f, V).
package sch

import (
	"errors"
	"sort"
	"sync/atomic"
)

// Table is an immutable version history built once at construction.
type Table struct {
	versions []map[string]int // version v is versions[v-1]; copies, never mutated
	fields   [][]string       // sorted field names per version
	defAt    map[string][]int // defAt[f][v-1] == def(f, v) for every version
	probe    atomic.Int64     // entries inspected by the most recent Def call (unexported)
}

// New builds a table from version 1 onward; each map is field=default.
func New(versions []map[string]int) *Table {
	t := &Table{versions: make([]map[string]int, len(versions)), defAt: map[string][]int{}}
	for v, defs := range versions {
		cp := make(map[string]int, len(defs))
		for f, d := range defs {
			cp[f] = d
		}
		t.versions[v] = cp
		fs := make([]string, 0, len(cp))
		for f := range cp {
			fs = append(fs, f)
		}
		sort.Strings(fs)
		t.fields = append(t.fields, fs)
	}
	// First appearance of each field (forward pass).
	intro := map[string]int{}
	for v := 1; v <= len(versions); v++ {
		for f := range t.versions[v-1] {
			if _, seen := intro[f]; !seen {
				intro[f] = v
			}
		}
	}
	// Precompute def(f, v) for every (field, version): present value, else the
	// introduction default (before introduction) or the last pre-removal value.
	for f := range intro {
		col := make([]int, len(versions))
		last := t.versions[intro[f]-1][f]
		for v := 1; v <= len(versions); v++ {
			if d, ok := t.versions[v-1][f]; ok {
				col[v-1], last = d, d
			} else if v < intro[f] {
				col[v-1] = t.versions[intro[f]-1][f]
			} else {
				col[v-1] = last
			}
		}
		t.defAt[f] = col
	}
	return t
}

// N reports the number of schema versions.
func (t *Table) N() int { return len(t.versions) }

// ValidVersion reports whether v is a legal version number.
func (t *Table) ValidVersion(v int) bool { return v >= 1 && v <= len(t.versions) }

// Has reports whether field belongs to schema[v].
func (t *Table) Has(v int, field string) bool {
	if !t.ValidVersion(v) {
		return false
	}
	_, ok := t.versions[v-1][field]
	return ok
}

// Fields returns the sorted field names of schema[v] (a copy).
func (t *Table) Fields(v int) []string {
	if !t.ValidVersion(v) {
		return nil
	}
	return append([]string(nil), t.fields[v-1]...)
}

// Defaults returns a copy of the raw field=default table of schema[v].
func (t *Table) Defaults(v int) map[string]int {
	if !t.ValidVersion(v) {
		return nil
	}
	cp := make(map[string]int, len(t.versions[v-1]))
	for f, d := range t.versions[v-1] {
		cp[f] = d
	}
	return cp
}

// Def returns def(field, v): the writer-time frozen default. Missing fields
// resolve by direct index into the precomputed column, so exactly one version
// entry is inspected regardless of history length. ok is false iff the field
// never appears in any version.
func (t *Table) Def(field string, v int) (val int, ok bool) {
	col, exists := t.defAt[field]
	t.probe.Store(1) // one indexed entry: col[v-1]
	if !exists || !t.ValidVersion(v) {
		return 0, false
	}
	return col[v-1], true
}

// ErrSelfTest is returned by SelfTest when the constant-time bound is violated.
var ErrSelfTest = errors.New("sch: default lookup cost grows with history length")

// SelfTest builds histories of several sizes m and verifies that resolving a
// missing default inspects a constant number of entries. It exposes only
// pass/fail, never the counter value itself.
func SelfTest() error {
	for _, m := range []int{100, 1000, 10000} {
		vs := make([]map[string]int, m)
		vs[0] = map[string]int{"base": 1}
		for v := 1; v < m-1; v++ {
			vs[v] = map[string]int{"base": 1}
		}
		vs[m-1] = map[string]int{"base": 1, "late": 7} // late is introduced last
		t := New(vs)
		if d, ok := t.Def("late", 1); !ok || d != 7 { // missing: introduced far later
			return errors.New("sch: wrong pre-introduction default")
		}
		if got := t.probe.Load(); got > 2 {
			return ErrSelfTest
		}
	}
	return nil
}
