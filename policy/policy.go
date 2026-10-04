// Package policy owns schema, rows, row policies, mask rules and the epoch.
package policy

import (
	"errors"
	"sync"

	"ontology/mask"
)

var (
	ErrInvalidArgument = errors.New("policy: invalid argument")
	ErrTableNotFound   = errors.New("policy: table not found")
	ErrColumnNotFound  = errors.New("policy: column not found")
	ErrAlreadyExists   = errors.New("policy: already exists")
	ErrNotFound        = errors.New("policy: not found")
)

// Kind is the row policy kind.
type Kind uint8

const (
	Permissive Kind = iota + 1
	Restrictive
)

// Op is an atomic predicate operator.
type Op uint8

const (
	OpEq Op = iota + 1
	OpNe
	OpLt
	OpLe
)

// Column is a table column definition.
type Column struct {
	Name string
	Type mask.Type
	Def  mask.Level
}

// Atom is 列 op 常量. Const is never NULL.
type Atom struct {
	Col   string
	Op    Op
	Const mask.Value
}

// Predicate is a conjunction of 0..4 atoms.
type Predicate struct {
	Atoms []Atom
}

// RowPolicy is one permissive/restrictive row policy.
type RowPolicy struct {
	ID    string
	Table string
	Role  string
	Kind  Kind
	Pred  Predicate
}

// ValidName reports whether n is a 1..64 byte non-empty identifier.
func ValidName(n string) bool { return len(n) >= 1 && len(n) <= 64 }

type maskKey struct{ table, col, role string }

type tableData struct {
	name     string
	cols     []Column
	colIndex map[string]int
	rows     [][]mask.Value
	pols     []*RowPolicy
}

// Store is the concurrency-safe policy state.
type Store struct {
	mu         sync.RWMutex
	tables     map[string]*tableData
	policyByID map[string]*RowPolicy
	maskRules  map[maskKey]mask.Level
	epoch      uint64
}

// NewStore creates an empty store at epoch 0.
func NewStore() *Store {
	return &Store{
		tables:     map[string]*tableData{},
		policyByID: map[string]*RowPolicy{},
		maskRules:  map[maskKey]mask.Level{},
	}
}

// AddTable registers a table with 1..32 columns.
func (s *Store) AddTable(name string, cols []Column) error {
	if !ValidName(name) || len(cols) < 1 || len(cols) > 32 {
		return ErrInvalidArgument
	}
	seen := map[string]struct{}{}
	for _, c := range cols {
		if !ValidName(c.Name) || c.Def > mask.LevelDeny {
			return ErrInvalidArgument
		}
		if _, dup := seen[c.Name]; dup {
			return ErrInvalidArgument
		}
		seen[c.Name] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tables[name]; ok {
		return ErrAlreadyExists
	}
	td := &tableData{
		name:     name,
		cols:     append([]Column(nil), cols...),
		colIndex: make(map[string]int, len(cols)),
	}
	for i, c := range cols {
		td.colIndex[c.Name] = i
	}
	s.tables[name] = td
	return nil
}

// Insert appends a row; cells may be NULL.
func (s *Store) Insert(table string, row []mask.Value) error {
	if len(row) < 1 || len(row) > 32 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	td, ok := s.tables[table]
	if !ok {
		return ErrTableNotFound
	}
	if len(row) != len(td.cols) {
		return ErrInvalidArgument
	}
	cp := make([]mask.Value, len(row))
	for i, v := range row {
		if v.Null {
			cp[i] = mask.NullVal(td.cols[i].Type)
			continue
		}
		if v.Type != td.cols[i].Type {
			return ErrInvalidArgument
		}
		cp[i] = copyValue(v)
	}
	td.rows = append(td.rows, cp)
	return nil
}

// AddRowPolicy registers a row policy.
func (s *Store) AddRowPolicy(p RowPolicy) error {
	if !ValidName(p.ID) || !ValidName(p.Table) || !ValidName(p.Role) ||
		(p.Kind != Permissive && p.Kind != Restrictive) ||
		len(p.Pred.Atoms) > 4 {
		return ErrInvalidArgument
	}
	for _, a := range p.Pred.Atoms {
		if !ValidName(a.Col) || a.Const.Null {
			return ErrInvalidArgument
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	td, ok := s.tables[p.Table]
	if !ok {
		return ErrTableNotFound
	}
	for _, a := range p.Pred.Atoms {
		ci, ok := td.colIndex[a.Col]
		if !ok {
			return ErrColumnNotFound
		}
		ct := td.cols[ci].Type
		if a.Const.Type != ct {
			return ErrInvalidArgument
		}
		if ct == mask.TypeStr && (a.Op == OpLt || a.Op == OpLe) {
			return ErrInvalidArgument
		}
		if a.Op < OpEq || a.Op > OpLe {
			return ErrInvalidArgument
		}
	}
	if _, dup := s.policyByID[p.ID]; dup {
		return ErrAlreadyExists
	}
	p.Pred.Atoms = append([]Atom(nil), p.Pred.Atoms...)
	for i := range p.Pred.Atoms {
		p.Pred.Atoms[i].Const = copyValue(p.Pred.Atoms[i].Const)
	}
	stored := p
	s.policyByID[p.ID] = &stored
	td.pols = append(td.pols, &stored)
	s.epoch++
	return nil
}

// DropRowPolicy removes a policy by id.
func (s *Store) DropRowPolicy(id string) error {
	if !ValidName(id) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.policyByID[id]
	if !ok {
		return ErrNotFound
	}
	td := s.tables[p.Table]
	for i, q := range td.pols {
		if q.ID == id {
			td.pols = append(td.pols[:i], td.pols[i+1:]...)
			break
		}
	}
	delete(s.policyByID, id)
	s.epoch++
	return nil
}

// SetMask upserts a column mask rule for one role.
func (s *Store) SetMask(table, col, role string, level mask.Level) error {
	if !ValidName(table) || !ValidName(col) || !ValidName(role) || level > mask.LevelDeny {
		return ErrInvalidArgument
	}
	if role == "*" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	td, ok := s.tables[table]
	if !ok {
		return ErrTableNotFound
	}
	if _, ok := td.colIndex[col]; !ok {
		return ErrColumnNotFound
	}
	k := maskKey{table, col, role}
	s.maskRules[k] = level
	s.epoch++
	return nil
}

// ClearMask removes a column mask rule for one role.
func (s *Store) ClearMask(table, col, role string) error {
	if !ValidName(table) || !ValidName(col) || !ValidName(role) {
		return ErrInvalidArgument
	}
	if role == "*" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	td, ok := s.tables[table]
	if !ok {
		return ErrTableNotFound
	}
	if _, ok := td.colIndex[col]; !ok {
		return ErrColumnNotFound
	}
	k := maskKey{table, col, role}
	if _, ok := s.maskRules[k]; !ok {
		return ErrNotFound
	}
	delete(s.maskRules, k)
	s.epoch++
	return nil
}

// Epoch returns the current epoch.
func (s *Store) Epoch() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.epoch
}

// View runs fn against a consistent read-only snapshot (epoch fixed).
func (s *Store) View(fn func(v *View)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(&View{store: s, epoch: s.epoch})
}

// View is a read-only snapshot bound to one RLock interval.
type View struct {
	store *Store
	epoch uint64
}

// Epoch returns the snapshot epoch.
func (v *View) Epoch() uint64 { return v.epoch }

// Table opens one table snapshot; returns nil when absent.
func (v *View) Table(name string) *TableView {
	td, ok := v.store.tables[name]
	if !ok {
		return nil
	}
	return &TableView{view: v, data: td}
}

// TableView is one table inside a snapshot.
type TableView struct {
	view *View
	data *tableData
}

// Columns returns the column definitions.
func (t *TableView) Columns() []Column { return t.data.cols }

// ColumnIndex resolves a column name to its index.
func (t *TableView) ColumnIndex(name string) (int, bool) {
	i, ok := t.data.colIndex[name]
	return i, ok
}

// Rows returns all inserted rows in insertion order (read-only use).
func (t *TableView) Rows() [][]mask.Value { return t.data.rows }

// ApplicablePolicies returns policies whose role is "*" or in roles.
func (t *TableView) ApplicablePolicies(roles map[string]struct{}) []RowPolicy {
	out := make([]RowPolicy, 0)
	for _, p := range t.data.pols {
		if p.Role == "*" {
			out = append(out, *p)
			continue
		}
		if _, ok := roles[p.Role]; ok {
			out = append(out, *p)
		}
	}
	return out
}

// MaskLevel returns the effective level (min over roles, def for no-rule roles)
// and the number of existing mask rules examined.
func (t *TableView) MaskLevel(colIdx int, roles []string) (mask.Level, int) {
	// Every role contributes: its rule level, or the column default.
	// Start at the strongest level and take the minimum.
	level := mask.LevelDeny
	rules := 0
	for _, role := range roles {
		if lv, ok := t.view.store.maskRules[maskKey{t.data.name, t.data.cols[colIdx].Name, role}]; ok {
			rules++
			if lv < level {
				level = lv
			}
		} else if d := t.data.cols[colIdx].Def; d < level {
			level = d
		}
	}
	return level, rules
}

// EvalAtom evaluates one atom on an already typed cell value.
// Any NULL cell makes every atom (including ≠) false.
func EvalAtom(v mask.Value, a Atom) bool {
	if v.Null {
		return false
	}
	switch v.Type {
	case mask.TypeInt:
		switch a.Op {
		case OpEq:
			return v.Int == a.Const.Int
		case OpNe:
			return v.Int != a.Const.Int
		case OpLt:
			return v.Int < a.Const.Int
		case OpLe:
			return v.Int <= a.Const.Int
		}
	case mask.TypeStr:
		switch a.Op {
		case OpEq:
			return string(v.Str) == string(a.Const.Str)
		case OpNe:
			return string(v.Str) != string(a.Const.Str)
		}
	}
	return false
}

func copyValue(v mask.Value) mask.Value {
	if v.Str != nil {
		v.Str = append([]byte(nil), v.Str...)
	}
	return v
}
