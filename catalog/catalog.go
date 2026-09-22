// Package catalog registers tables (with declared row counts and optional
// statistics) and join predicates, and detects missing, stale and corrupt
// statistics. A Catalog is read-only after registration and safe for
// concurrent use by the planner.
package catalog

import (
	"errors"
	"fmt"
	"math"

	"ontology/stats"
)

var (
	ErrNoTables       = errors.New("no tables registered")
	ErrUnknownTable   = errors.New("unknown table")
	ErrDuplicateTable = errors.New("duplicate table")
	ErrSelfJoin       = errors.New("self-join predicate not supported")
	ErrMissingStats   = errors.New("missing statistics")
	ErrStaleStats     = errors.New("stale statistics")
	// ErrCorruptStats aliases stats.ErrCorrupt so errors.Is works across
	// package boundaries.
	ErrCorruptStats = stats.ErrCorrupt
)

// StaleThreshold is the tolerated relative drift between the row count in
// the statistics and the row count registered in the catalog.
const StaleThreshold = 0.1

// Predicate is an equality join predicate LTable.LCol = RTable.RCol.
type Predicate struct {
	LTable, LCol string
	RTable, RCol string
}

func (p Predicate) String() string {
	return p.LTable + "." + p.LCol + "=" + p.RTable + "." + p.RCol
}

// TableInfo pairs the catalog-declared row count (authoritative) with the
// collected statistics. Stale marks a drift beyond StaleThreshold.
type TableInfo struct {
	Name  string
	Rows  int64
	Stats *stats.Table
	Stale bool
}

// Catalog holds registered tables and predicates.
type Catalog struct {
	tables map[string]*TableInfo
	preds  []Predicate
}

func New() *Catalog {
	return &Catalog{tables: make(map[string]*TableInfo)}
}

// AddTable registers a table. Corrupt statistics are rejected with an error
// wrapping ErrCorruptStats. Stale statistics are accepted but flagged, and
// the catalog row count stays authoritative.
func (c *Catalog) AddTable(name string, rows int64, st *stats.Table) error {
	if _, dup := c.tables[name]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicateTable, name)
	}
	if rows < 0 {
		return fmt.Errorf("%w: table %s: negative rows %d", ErrCorruptStats, name, rows)
	}
	if st != nil {
		if err := st.Validate(); err != nil {
			return err
		}
	}
	info := &TableInfo{Name: name, Rows: rows, Stats: st}
	if st != nil && rows > 0 {
		drift := math.Abs(float64(st.Rows-rows)) / float64(rows)
		info.Stale = drift > StaleThreshold
	} else if st != nil && st.Rows != rows {
		info.Stale = true
	}
	c.tables[name] = info
	return nil
}

// AddPredicate registers an equality predicate. Self-joins are rejected.
func (c *Catalog) AddPredicate(lt, lc, rt, rc string) error {
	if lt == rt {
		return fmt.Errorf("%w: %s.%s=%s.%s", ErrSelfJoin, lt, lc, rt, rc)
	}
	for _, t := range []string{lt, rt} {
		if _, ok := c.tables[t]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownTable, t)
		}
	}
	c.preds = append(c.preds, Predicate{lt, lc, rt, rc})
	return nil
}

// Table returns the info for a registered table.
func (c *Catalog) Table(name string) (*TableInfo, bool) {
	info, ok := c.tables[name]
	return info, ok
}

// Predicates returns a copy of all registered predicates.
func (c *Catalog) Predicates() []Predicate {
	out := make([]Predicate, len(c.preds))
	copy(out, c.preds)
	return out
}

// Check reports diagnostics: missing column statistics for predicate
// endpoints (ErrMissingStats) and stale table statistics (ErrStaleStats).
func (c *Catalog) Check() []error {
	var errs []error
	for _, p := range c.preds {
		for _, ep := range [][2]string{{p.LTable, p.LCol}, {p.RTable, p.RCol}} {
			info := c.tables[ep[0]]
			if info.Stats == nil {
				errs = append(errs, fmt.Errorf("%w: table %s has no statistics", ErrMissingStats, ep[0]))
				continue
			}
			if _, ok := info.Stats.Columns[ep[1]]; !ok {
				errs = append(errs, fmt.Errorf("%w: table %s column %s", ErrMissingStats, ep[0], ep[1]))
			}
		}
	}
	for _, info := range c.tables {
		if info.Stale {
			errs = append(errs, fmt.Errorf("%w: table %s: stats rows %d, catalog rows %d",
				ErrStaleStats, info.Name, info.Stats.Rows, info.Rows))
		}
	}
	return errs
}
