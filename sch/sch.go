// Package sch 定义列类型、类型转换表与「版本 → 有序列布局」的 registry。不依赖其他包。
package sch

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
)

// 可判定哨兵错误，互不相同。
var (
	ErrBadType             = errors.New("sch: unknown column type")
	ErrEmptyName           = errors.New("sch: empty column name")
	ErrDuplicateColumn     = errors.New("sch: duplicate column name")
	ErrNoSuchColumn        = errors.New("sch: no such column")
	ErrVersionUnregistered = errors.New("sch: event version not registered")
	ErrBadValue            = errors.New("sch: value not coercible")
	ErrMissingColumn       = errors.New("sch: required column missing")
)

type Type int

const (
	Int Type = iota
	Str
)

var types = map[string]Type{"int": Int, "str": Str}

func ParseType(s string) (Type, error) {
	if t, ok := types[s]; ok {
		return t, nil
	}
	return 0, fmt.Errorf("%w: %q", ErrBadType, s)
}

// Coerce 总可判定：同类型不转换；int→str 恒成功；str→int 仅合法十进制整数成功。
func Coerce(v any, from, to Type) (any, error) {
	n, iok := v.(int64)
	s, sok := v.(string)
	switch {
	case from == to && from == Int && iok:
		return n, nil
	case from == to && from == Str && sok:
		return s, nil
	case from == Int && to == Str && iok:
		return strconv.FormatInt(n, 10), nil
	case from == Str && to == Int && sok:
		if p, err := strconv.ParseInt(s, 10, 64); err == nil {
			return p, nil
		}
	}
	return nil, ErrBadValue
}

// Column 是某版本布局里的一列。
type Column struct {
	Name     string
	Typ      Type
	Required bool
}

// Registry 记录版本 → 有序列布局，版本从 1 起，每次演进 bump 一新版本。
// 所有写操作先整体校验再提交：校验失败零写入，状态不变。
type Registry struct {
	mu       sync.RWMutex
	versions [][]Column // versions[i] 是版本 i+1 的布局
}

func NewRegistry(init ...Column) *Registry {
	r := &Registry{}
	if len(init) > 0 {
		r.versions = [][]Column{append([]Column{}, init...)}
	}
	return r
}

func (r *Registry) set(name string, exist bool, fn func(cur []Column, i int) []Column) error {
	if name == "" {
		return ErrEmptyName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := r.cur()
	i := -1
	for j, c := range cur {
		if c.Name == name {
			i = j
		}
	}
	if exist != (i >= 0) {
		if exist {
			return fmt.Errorf("%w: %q", ErrNoSuchColumn, name)
		}
		return fmt.Errorf("%w: %q", ErrDuplicateColumn, name)
	}
	r.versions = append(r.versions, fn(cur, i))
	return nil
}

func (r *Registry) AddColumn(name, typ string, required bool) error {
	t, err := ParseType(typ)
	if err != nil {
		return err
	}
	return r.set(name, false, func(cur []Column, _ int) []Column {
		return append(append([]Column{}, cur...), Column{name, t, required})
	})
}

func (r *Registry) DropColumn(name string) error {
	return r.set(name, true, func(cur []Column, i int) []Column {
		return append(append([]Column{}, cur[:i]...), cur[i+1:]...)
	})
}

func (r *Registry) ChangeType(name, newType string) error {
	t, err := ParseType(newType)
	if err != nil {
		return err
	}
	return r.set(name, true, func(cur []Column, i int) []Column {
		next := append([]Column{}, cur...)
		next[i].Typ = t
		return next
	})
}

func (r *Registry) Layout(version int) ([]Column, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if version < 1 || version > len(r.versions) {
		return nil, fmt.Errorf("%w: %d", ErrVersionUnregistered, version)
	}
	return append([]Column{}, r.versions[version-1]...), nil
}

func (r *Registry) Active() []Column {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Column{}, r.cur()...)
}

func (r *Registry) cur() []Column {
	if len(r.versions) == 0 {
		return nil
	}
	return r.versions[len(r.versions)-1]
}
