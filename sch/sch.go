// Package sch holds the schema version table: per-version field sets and
// defaults, and the write-time-frozen default lookup def(f, V).
package sch

import "sync/atomic"

// Field is one schema entry: a name and its default in that version.
type Field struct {
	Name string
	Def  int
}

// Version is one numbered schema version (slice index + 1 == version).
type Version struct {
	Fields []Field
}

// Catalog is an immutable schema history.
type Catalog struct {
	n       int
	order   [][]string       // ordered field names per version (1-indexed)
	present []map[string]int // default of each field present per version
	defTab  map[string][]int // defTab[f][V] == def(f, V), precomputed

	// probeCount records how many version entries the most recent Def call
	// inspected. It is unexported, absent from every public method, and atomic
	// so concurrent Def calls stay race-free; in-package tests read it.
	probeCount atomic.Int64
}

// New builds a catalog from an ordered version history.
func New(hist []Version) *Catalog {
	n := len(hist)
	c := &Catalog{
		n:       n,
		order:   make([][]string, n+1),
		present: make([]map[string]int, n+1),
		defTab:  make(map[string][]int),
	}
	first := make(map[string]int) // default at first introduction
	for V := 1; V <= n; V++ {
		c.order[V] = make([]string, 0, len(hist[V-1].Fields))
		c.present[V] = make(map[string]int, len(hist[V-1].Fields))
		for _, f := range hist[V-1].Fields {
			c.order[V] = append(c.order[V], f.Name)
			c.present[V][f.Name] = f.Def
			if _, ok := first[f.Name]; !ok {
				first[f.Name] = f.Def
			}
		}
	}
	// Precompute def(f, V) for every field/version pair so Def is a direct
	// indexed load: present value wins; a past value freezes the last seen
	// default; a not-yet-introduced field freezes its introduction default.
	for f, intro := range first {
		tab := make([]int, n+1)
		last, hasPast := 0, false
		for V := 1; V <= n; V++ {
			switch d, ok := c.present[V][f]; {
			case ok:
				tab[V], last, hasPast = d, d, true
			case hasPast:
				tab[V] = last
			default:
				tab[V] = intro
			}
		}
		c.defTab[f] = tab
	}
	return c
}

// NewStandard builds the fixed v1..v3 history used by the task.
func NewStandard() *Catalog {
	return New([]Version{
		{[]Field{{"a", 1}, {"b", 2}}},
		{[]Field{{"a", 1}, {"b", 2}, {"c", 10}}},
		{[]Field{{"a", 1}, {"c", 30}, {"d", 20}}},
	})
}

// N reports the number of versions in the history.
func (c *Catalog) N() int { return c.n }

// Fields returns the ordered field names defined by schema[V]; the returned
// slice is read-only.
func (c *Catalog) Fields(V int) []string {
	if V < 1 || V > c.n {
		return nil
	}
	return c.order[V]
}

// Has reports whether f belongs to schema[V].
func (c *Catalog) Has(V int, f string) bool {
	if V < 1 || V > c.n {
		return false
	}
	_, ok := c.present[V][f]
	return ok
}

// Def resolves def(f, V): the default of f as frozen at writer version V.
// It is a direct field-keyed, version-indexed load (one inspected entry),
// independent of history length m, and records that count in the unexported
// probeCount. Safe for concurrent use.
func (c *Catalog) Def(f string, V int) int {
	tab := c.defTab[f] // one direct, field-keyed index
	p := int64(1)
	if tab == nil || V < 1 || V > c.n {
		c.probeCount.Store(p)
		return 0
	}
	c.probeCount.Store(p)
	return tab[V]
}
