// Package col defines the source-column schema, per-row value access,
// and evaluation of a single output column (alias pick / refs sum).
// It depends on no other package in this module.
package col

import "errors"

var (
	ErrNoColumns    = errors.New("col: schema has no columns")
	ErrEmptyColName = errors.New("col: empty column name")
	ErrDuplicateCol = errors.New("col: duplicate column name")
	ErrUnknownCol   = errors.New("col: unknown source column")
	ErrMissingCol   = errors.New("col: row missing source column")
)

// Schema is an ordered, non-empty, duplicate-free list of int source columns.
type Schema struct {
	names []string
	index map[string]int
}

// NewSchema validates cols and builds a Schema. On error nothing is kept.
func NewSchema(cols []string) (*Schema, error) {
	if len(cols) == 0 {
		return nil, ErrNoColumns
	}
	idx := make(map[string]int, len(cols))
	for i, c := range cols {
		if c == "" {
			return nil, ErrEmptyColName
		}
		if _, dup := idx[c]; dup {
			return nil, ErrDuplicateCol
		}
		idx[c] = i
	}
	names := make([]string, len(cols))
	copy(names, cols)
	return &Schema{names: names, index: idx}, nil
}

// Names returns the source column names in definition order.
func (s *Schema) Names() []string {
	out := make([]string, len(s.names))
	copy(out, s.names)
	return out
}

// Has reports whether name is a source column.
func (s *Schema) Has(name string) bool {
	_, ok := s.index[name]
	return ok
}

// Row holds one change row's values, validated against the schema:
// every source column present exactly once, no unknown columns.
type Row struct {
	vals map[string]int
}

// NewRow validates m against the schema. It fails (keeping nothing) if
// any source column is missing or any unknown column appears.
func (s *Schema) NewRow(m map[string]int) (Row, error) {
	for _, name := range s.names {
		if _, ok := m[name]; !ok {
			return Row{}, ErrMissingCol
		}
	}
	for k := range m {
		if !s.Has(k) {
			return Row{}, ErrUnknownCol
		}
	}
	vals := make(map[string]int, len(m))
	for k, v := range m {
		vals[k] = v
	}
	return Row{vals: vals}, nil
}

// Get returns this row's value of a source column. Callers must only
// ask for columns they intend to read; pruned columns are never read.
func (r Row) Get(name string) int {
	return r.vals[name]
}

// Eval computes one output column over this row: the sum of the values
// of every source column in refs. A single ref is a plain alias pick.
// It performs exactly len(refs) source-column reads.
func (r Row) Eval(refs []string) int {
	sum := 0
	for _, ref := range refs {
		sum += r.vals[ref]
	}
	return sum
}
