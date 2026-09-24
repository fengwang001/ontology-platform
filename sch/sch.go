// Package sch: column types, coercion table, and version -> layout registry.
package sch

import (
	"errors"
	"strconv"
	"sync"
)

var (
	ErrBadType        = errors.New("sch: invalid column type")
	ErrEmptyName      = errors.New("sch: empty column name")
	ErrDuplicate      = errors.New("sch: duplicate column name")
	ErrUnknownColumn  = errors.New("sch: no such column")
	ErrUnknownVersion = errors.New("sch: version not registered")
	ErrBadValue       = errors.New("sch: value cannot be coerced")
	ErrMissingColumn  = errors.New("sch: required column missing")
)

type Type int

const (
	Int Type = iota // int64
	Str             // string
)

func ParseType(s string) (Type, error) {
	if t, ok := map[string]Type{"int": Int, "str": Str}[s]; ok {
		return t, nil
	}
	return 0, ErrBadType
}
func Zero(t Type) any { return map[Type]any{Int: int64(0), Str: ""}[t] } // int->0, str->""

// Coerce converts v deterministically: int->str always, str->int only for decimal integers.
func Coerce(v any, from, to Type) (any, error) {
	if from == to {
		return v, nil
	}
	if from == Int { // int -> str
		if i, ok := v.(int64); ok {
			return strconv.FormatInt(i, 10), nil
		}
		return nil, ErrBadValue
	}
	if s, ok := v.(string); ok { // str -> int
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, nil
		}
	}
	return nil, ErrBadValue
}

type Column struct { // one column in an ordered layout
	Name     string
	Typ      Type
	Required bool
}

// Registry: version -> ordered column layout (1-based); each op appends one version.
type Registry struct {
	mu       sync.RWMutex
	versions [][]Column
}

func NewRegistry(seed ...Column) *Registry { // seed = version 1, if given
	if len(seed) == 0 {
		return &Registry{}
	}
	return &Registry{versions: [][]Column{append([]Column{}, seed...)}}
}
func find(layout []Column, name string) int {
	for i, c := range layout {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// mutate validates via fn and appends a new version; on error no trace is left.
func (r *Registry) mutate(fn func(cur []Column) ([]Column, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var cur []Column
	if len(r.versions) > 0 {
		cur = r.versions[len(r.versions)-1]
	}
	next, err := fn(cur)
	if err != nil {
		return err
	}
	r.versions = append(r.versions, next)
	return nil
}
func (r *Registry) AddColumn(name string, t Type, required bool) error {
	if name == "" {
		return ErrEmptyName
	}
	return r.mutate(func(cur []Column) ([]Column, error) {
		if find(cur, name) >= 0 {
			return nil, ErrDuplicate
		}
		return append(append([]Column{}, cur...), Column{Name: name, Typ: t, Required: required}), nil
	})
}
func (r *Registry) DropColumn(name string) error {
	return r.mutate(func(cur []Column) ([]Column, error) {
		i := find(cur, name)
		if i < 0 {
			return nil, ErrUnknownColumn
		}
		next := append([]Column{}, cur[:i]...)
		return append(next, cur[i+1:]...), nil
	})
}
func (r *Registry) ChangeType(name string, t Type) error {
	return r.mutate(func(cur []Column) ([]Column, error) {
		i := find(cur, name)
		if i < 0 {
			return nil, ErrUnknownColumn
		}
		next := append([]Column{}, cur...)
		next[i].Typ = t
		return next, nil
	})
}

// Snapshot atomically returns the given version's layout and the active layout.
func (r *Registry) Snapshot(version int) (event, active []Column, err error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if version < 1 || version > len(r.versions) {
		return nil, nil, ErrUnknownVersion
	}
	return append([]Column{}, r.versions[version-1]...),
		append([]Column{}, r.versions[len(r.versions)-1]...), nil
}
func (r *Registry) Active() []Column { // copy of the active layout
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.versions) == 0 {
		return nil
	}
	return append([]Column{}, r.versions[len(r.versions)-1]...)
}
