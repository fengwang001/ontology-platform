package policy

import (
	"fmt"
	"sync"
)

// table is the mutable per-table state.
type table struct {
	cols []Column
	rows [][]Cell // row number is 1-based: row n is rows[n-1]
}

// Store is the concurrency-safe policy state.
type Store struct {
	mu     sync.RWMutex
	tables map[string]*table
	pols   map[string][]RowPolicy // per table, insertion order
	masks  map[string][]MaskRule  // per table, insertion order
	polOf  map[string]string      // policy id -> table
	epoch  int
}

// ReadSnapshot is an immutable, post-lock view of one table for one subject.
type ReadSnapshot struct {
	Epoch    int
	Table    string
	Columns  []Column
	Rows     [][]Cell
	Policies []RowPolicy
	Masks    []MaskRule
}

// NewStore creates an empty store with epoch 0.
func NewStore() *Store {
	return &Store{
		tables: map[string]*table{},
		pols:   map[string][]RowPolicy{},
		masks:  map[string][]MaskRule{},
		polOf:  map[string]string{},
	}
}

// AddTable registers a table with 1..32 uniquely named, well-typed columns.
func (s *Store) AddTable(name string, cols []Column) error {
	if !validName(name) || len(cols) < 1 || len(cols) > 32 {
		return fmt.Errorf("add table %q: %w", name, ErrInvalidArgument)
	}
	seen := map[string]bool{}
	for _, c := range cols {
		if !validName(c.Name) || seen[c.Name] {
			return fmt.Errorf("add table %q: %w", name, ErrInvalidArgument)
		}
		if c.Type != TypeInt && c.Type != TypeStr || c.Def < 0 || c.Def > 4 {
			return fmt.Errorf("add table %q: %w", name, ErrInvalidArgument)
		}
		seen[c.Name] = true
	}
	colsCopy := append([]Column(nil), cols...)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tables[name]; ok {
		return fmt.Errorf("add table %q: %w", name, ErrAlreadyExists)
	}
	s.tables[name] = &table{cols: colsCopy}
	s.epoch++
	return nil
}

// Insert appends a row. Every non-NIL cell must match its column type.
func (s *Store) Insert(table string, row []Cell) error {
	if len(row) < 1 || len(row) > 32 {
		return fmt.Errorf("insert %q: %w", table, ErrInvalidArgument)
	}
	for _, v := range row {
		if v != nil && !validCell(v) {
			return fmt.Errorf("insert %q: %w", table, ErrInvalidArgument)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tables[table]
	if !ok {
		return fmt.Errorf("insert %q: %w", table, ErrTableNotFound)
	}
	if len(row) != len(t.cols) {
		return fmt.Errorf("insert %q: %w", table, ErrInvalidArgument)
	}
	for i, v := range row {
		if v != nil && cellType(v) != t.cols[i].Type {
			return fmt.Errorf("insert %q col %q: %w", table, t.cols[i].Name, ErrInvalidArgument)
		}
	}
	t.rows = append(t.rows, append([]Cell(nil), row...))
	return nil
}

// AddRowPolicy registers a conjunction policy. Operators/constants must fit
// the referenced columns; zero atoms means the predicate is always true.
func (s *Store) AddRowPolicy(p RowPolicy) error {
	if !validName(p.ID) || !validName(p.Table) || !validRole(p.Role) {
		return fmt.Errorf("add policy %q: %w", p.ID, ErrInvalidArgument)
	}
	if p.Kind != Permissive && p.Kind != Restrictive || len(p.Pred) > 4 {
		return fmt.Errorf("add policy %q: %w", p.ID, ErrInvalidArgument)
	}
	for _, a := range p.Pred {
		if !validName(a.Col) || !validOp(a.Op) || a.Constant == nil || !validCell(a.Constant) {
			return fmt.Errorf("add policy %q: %w", p.ID, ErrInvalidArgument)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tables[p.Table]
	if !ok {
		return fmt.Errorf("add policy %q table %q: %w", p.ID, p.Table, ErrTableNotFound)
	}
	for _, a := range p.Pred {
		ci := colIndex(t.cols, a.Col)
		if ci < 0 {
			return fmt.Errorf("add policy %q col %q: %w", p.ID, a.Col, ErrColumnNotFound)
		}
		if cellType(a.Constant) != t.cols[ci].Type || !opAllowed(t.cols[ci].Type, a.Op) {
			return fmt.Errorf("add policy %q atom %q: %w", p.ID, a.Col, ErrInvalidArgument)
		}
	}
	if _, dup := s.polOf[p.ID]; dup {
		return fmt.Errorf("add policy %q: %w", p.ID, ErrAlreadyExists)
	}
	s.polOf[p.ID] = p.Table
	s.pols[p.Table] = append(s.pols[p.Table], RowPolicy{
		ID:    p.ID,
		Table: p.Table,
		Role:  p.Role,
		Kind:  p.Kind,
		Pred:  append([]Atom(nil), p.Pred...),
	})
	s.epoch++
	return nil
}

// DropRowPolicy removes a policy by id.
func (s *Store) DropRowPolicy(id string) error {
	if !validName(id) {
		return fmt.Errorf("drop policy %q: %w", id, ErrInvalidArgument)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tname, ok := s.polOf[id]
	if !ok {
		return fmt.Errorf("drop policy %q: %w", id, ErrNotFound)
	}
	list := s.pols[tname]
	for i, p := range list {
		if p.ID == id {
			s.pols[tname] = append(list[:i:i], list[i+1:]...)
			delete(s.polOf, id)
			s.epoch++
			return nil
		}
	}
	return fmt.Errorf("drop policy %q: %w", id, ErrNotFound)
}

// SetMask upserts an explicit per-role mask level for one column.
func (s *Store) SetMask(r MaskRule) error {
	if !validName(r.Table) || !validName(r.Col) || !validName(r.Role) {
		return fmt.Errorf("set mask: %w", ErrInvalidArgument)
	}
	if r.Level < 0 || r.Level > 4 {
		return fmt.Errorf("set mask %q.%q: %w", r.Table, r.Col, ErrInvalidArgument)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tables[r.Table]
	if !ok {
		return fmt.Errorf("set mask table %q: %w", r.Table, ErrTableNotFound)
	}
	if colIndex(t.cols, r.Col) < 0 {
		return fmt.Errorf("set mask col %q.%q: %w", r.Table, r.Col, ErrColumnNotFound)
	}
	list := s.masks[r.Table]
	for i := range list {
		if list[i].Col == r.Col && list[i].Role == r.Role {
			if list[i].Level == r.Level {
				return nil
			}
			list[i].Level = r.Level
			s.epoch++
			return nil
		}
	}
	s.masks[r.Table] = append(list, MaskRule{Table: r.Table, Col: r.Col, Role: r.Role, Level: r.Level})
	s.epoch++
	return nil
}

// ClearMask removes an explicit mask rule for one column/role.
func (s *Store) ClearMask(table, col, role string) error {
	if !validName(table) || !validName(col) || !validName(role) {
		return fmt.Errorf("clear mask: %w", ErrInvalidArgument)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tables[table]
	if !ok {
		return fmt.Errorf("clear mask table %q: %w", table, ErrTableNotFound)
	}
	if colIndex(t.cols, col) < 0 {
		return fmt.Errorf("clear mask col %q.%q: %w", table, col, ErrColumnNotFound)
	}
	list := s.masks[table]
	for i := range list {
		if list[i].Col == col && list[i].Role == role {
			s.masks[table] = append(list[:i:i], list[i+1:]...)
			s.epoch++
			return nil
		}
	}
	return fmt.Errorf("clear mask %q.%q for %q: %w", table, col, role, ErrNotFound)
}

// Epoch returns the current policy epoch.
func (s *Store) Epoch() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.epoch
}

// SnapshotRead is the exported entry used by the access package to obtain one
// epoch-consistent, copied view of a table for a subject.
func (s *Store) SnapshotRead(table string, roles []string, needCols map[string]bool, touches *int) (*ReadSnapshot, error) {
	return s.snapshotRead(table, roles, needCols, touches)
}

// snapshotRead copies only applicable policies (role in roles or "*") and
// mask rules for subject roles on needed columns; touches counts exactly the
// examined entries, independent of rules for other tables/roles.
func (s *Store) snapshotRead(table string, roles []string, needCols map[string]bool, touches *int) (*ReadSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tables[table]
	if !ok {
		return nil, fmt.Errorf("read table %q: %w", table, ErrTableNotFound)
	}
	roleSet := make(map[string]bool, len(roles))
	for _, r := range roles {
		roleSet[r] = true
	}

	pols := make([]RowPolicy, 0)
	for _, p := range s.pols[table] {
		if p.Role == "*" || roleSet[p.Role] {
			pols = append(pols, p)
		}
	}
	if touches != nil {
		*touches += len(pols)
	}

	mr := make([]MaskRule, 0)
	for _, m := range s.masks[table] {
		if roleSet[m.Role] && needCols[m.Col] {
			mr = append(mr, m)
			if touches != nil {
				*touches++
			}
		}
	}

	rows := make([][]Cell, len(t.rows))
	for i, row := range t.rows {
		rows[i] = append([]Cell(nil), row...)
	}
	return &ReadSnapshot{
		Epoch:    s.epoch,
		Table:    table,
		Columns:  append([]Column(nil), t.cols...),
		Rows:     rows,
		Policies: pols,
		Masks:    mr,
	}, nil
}

func validName(n string) bool { return len(n) >= 1 && len(n) <= 64 }

func validRole(r string) bool { return r == "*" || validName(r) }

func validOp(o Op) bool { return o >= OpEq && o <= OpLe }

func validCell(c Cell) bool {
	switch c.(type) {
	case int64, string:
		return true
	default:
		return false
	}
}

func cellType(c Cell) ColType {
	if _, ok := c.(int64); ok {
		return TypeInt
	}
	return TypeStr
}

func colIndex(cols []Column, name string) int {
	for i := range cols {
		if cols[i].Name == name {
			return i
		}
	}
	return -1
}

func opAllowed(ct ColType, o Op) bool {
	if ct == TypeInt {
		return o >= OpEq && o <= OpLe
	}
	return o == OpEq || o == OpNe
}
