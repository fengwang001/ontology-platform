package delta

import (
	"errors"

	"ontology/doc"
)

// ErrContradiction 表示同一侧变更集里同一键既删除又新增/修改。
var ErrContradiction = errors.New("contradictory change: key both deleted and added/modified")

// Op 是字段级操作。
type Op int

const (
	OpAdded Op = iota + 1
	OpChanged
	OpRemoved
)

// FieldChange 是单字段变更：祖先值（若有）、新值（若有）与操作类型。
type FieldChange struct {
	Old    doc.Value
	New    doc.Value
	Op     Op
	HadOld bool
}

// Kind 是键级变更类型。
type Kind int

const (
	KindAdded Kind = iota + 1
	KindDeleted
	KindModified
)

// Entry 是单键变更。
type Entry struct {
	Key    string
	Kind   Kind
	Record doc.Record              // 新增后的整记录（KindAdded）
	Fields map[string]FieldChange  // 字段级变更（KindModified）
}

// Set 是单侧相对祖先的变更集。
type Set struct {
	entries map[string]*Entry
	order   []string
}

// Entries 按键字典序返回全部变更。
func (s *Set) Entries() []*Entry {
	out := make([]*Entry, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.entries[k])
	}
	return out
}

// Get 返回某键的变更（若有）。
func (s *Set) Get(key string) (*Entry, bool) {
	if s == nil {
		return nil, false
	}
	e, ok := s.entries[key]
	return e, ok
}

// ContradictionError 指出自相矛盾的键。
type ContradictionError struct {
	Key string
}

func (e *ContradictionError) Error() string {
	return ErrContradiction.Error() + ": " + e.Key
}

func (e *ContradictionError) Is(target error) bool { return target == ErrContradiction }
