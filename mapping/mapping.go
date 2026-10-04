package mapping

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology/coerce"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrStrict          = errors.New("strict dynamic mapping rejection")
	ErrFieldLimit      = errors.New("field limit exceeded")
)

type Mode int

const (
	DynamicTrue Mode = iota
	DynamicFalse
	DynamicStrict
)

type node struct {
	typ      coerce.Type
	children map[string]*node
	removed  bool
}

// Level is one nesting level of the field tree. nil denotes the root.
// It is a traversal cursor handed out by Tx; callers never mutate it.
type Level map[string]*node

type Mapping struct {
	mu    sync.RWMutex
	mode  Mode
	fmax  int
	root  map[string]*node
	count int
	mv    int
}

func New(mode Mode, fmax int) (*Mapping, error) {
	if mode < DynamicTrue || mode > DynamicStrict {
		return nil, fmt.Errorf("%w: invalid dynamic mode", ErrInvalidArgument)
	}
	if fmax < 1 || fmax > 100000 {
		return nil, fmt.Errorf("%w: Fmax out of range", ErrInvalidArgument)
	}
	return &Mapping{mode: mode, fmax: fmax, root: map[string]*node{}}, nil
}

func (m *Mapping) Mode() Mode { return m.mode }

// Begin opens a write transaction. Commit/Rollback release the write lock.
// Exactly one of them must be called.
func (m *Mapping) Begin() *Tx {
	m.mu.Lock()
	return &Tx{m: m}
}

func (m *Mapping) PutMapping(path []string, typ coerce.Type) error {
	if err := validatePath(path); err != nil {
		return err
	}
	if !validType(typ) {
		return fmt.Errorf("%w: invalid field type %q", ErrInvalidArgument, typ)
	}
	tx := m.Begin()
	if err := tx.putMapping(path, typ); err != nil {
		tx.Rollback()
		return err
	}
	tx.Commit()
	return nil
}

func (m *Mapping) MV() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mv
}

func (m *Mapping) FieldCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.count
}

func (m *Mapping) Snapshot() map[string]coerce.Type {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]coerce.Type, m.count)
	collect(m.root, nil, out)
	return out
}

// Tx is a write transaction holding m's write lock.
type Tx struct {
	m       *Mapping
	created []*node
	touched int
}

func (t *Tx) Root() Level { return Level(t.m.root) }

// Lookup walks one key deeper from level (nil means the root); the visited
// node is counted in Touched. For object fields the child level is returned.
func (t *Tx) Lookup(level Level, key string) (coerce.Type, Level, bool) {
	if level == nil {
		level = t.m.root
	}
	n, ok := level[key]
	if !ok {
		return "", nil, false
	}
	t.touched++
	if n.typ == coerce.Object {
		return n.typ, Level(n.children), true
	}
	return n.typ, nil, true
}

// Create appends a new field under cur (nil means the root).
func (t *Tx) Create(level Level, key string, typ coerce.Type) (Level, error) {
	if level == nil {
		level = t.m.root
	}
	if t.m.count >= t.m.fmax {
		return nil, ErrFieldLimit
	}
	n := &node{typ: typ}
	if typ == coerce.Object {
		n.children = map[string]*node{}
	}
	level[key] = n
	t.m.count++
	t.created = append(t.created, n)
	t.touched++
	return Level(n.children), nil
}

func (t *Tx) Mode() Mode { return t.m.mode }

func (t *Tx) Touched() int { return t.touched }

// Count returns the tentative field count (caller holds the write lock).
func (t *Tx) Count() int { return t.m.count }

// Commit atomically publishes every node created during the transaction and
// bumps mv exactly once. Returns the new mapping version.
func (t *Tx) Commit() int {
	if len(t.created) > 0 {
		t.m.mv++
	}
	mv := t.m.mv
	t.created = nil
	t.m.mu.Unlock()
	return mv
}

// Rollback removes every node tentatively created during the transaction.
func (t *Tx) Rollback() {
	for _, n := range t.created {
		t.m.count--
		n.removed = true
	}
	pruneRemoved(t.m.root)
	t.created = nil
	t.m.mu.Unlock()
}

func (t *Tx) putMapping(path []string, typ coerce.Type) error {
	cur := Level(t.m.root)
	for i, key := range path {
		last := i == len(path)-1
		n, ok := cur[key]
		if !ok {
			nt := typ
			if !last {
				nt = coerce.Object
			}
			child, err := t.Create(cur, key, nt)
			if err != nil {
				return fmt.Errorf("%w: %s", err, strings.Join(path[:i+1], "."))
			}
			if last {
				return nil
			}
			cur = child
			continue
		}
		t.touched++
		if last {
			if n.typ != typ {
				return conflict(path)
			}
			return nil
		}
		if n.typ != coerce.Object {
			return conflict(path[:i+1])
		}
		cur = Level(n.children)
	}
	return nil
}

func conflict(path []string) error {
	return fmt.Errorf("%w: %s", coerce.ErrTypeConflict, strings.Join(path, "."))
}

func validatePath(path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("%w: empty field path", ErrInvalidArgument)
	}
	for _, key := range path {
		if err := ValidateKey(key); err != nil {
			return err
		}
	}
	return nil
}

// ValidateKey checks the 1..64 byte, no-dot key rule.
func ValidateKey(key string) error {
	n := len(key)
	if n < 1 || n > 64 || strings.ContainsRune(key, '.') {
		return fmt.Errorf("%w: invalid field key %q", ErrInvalidArgument, key)
	}
	return nil
}

func validType(t coerce.Type) bool {
	switch t {
	case coerce.Long, coerce.Double, coerce.Keyword, coerce.Bool, coerce.Object:
		return true
	}
	return false
}

func collect(level Level, prefix []string, out map[string]coerce.Type) {
	keys := make([]string, 0, len(level))
	for k := range level {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n := level[k]
		p := append(append([]string{}, prefix...), k)
		out[strings.Join(p, ".")] = n.typ
		if n.typ == coerce.Object {
			collect(n.children, p, out)
		}
	}
}

func pruneRemoved(level Level) {
	for k, n := range level {
		if n.removed {
			delete(level, k)
			continue
		}
		if n.typ == coerce.Object {
			pruneRemoved(n.children)
		}
	}
}
