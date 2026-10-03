package script

import (
	"errors"
	"sort"
)

var (
	ErrInvalid    = errors.New("invalid script")
	ErrPermission = errors.New("permission denied")
)

type Script struct {
	Ver     int64
	Sum     uint64
	HasUndo bool
}

type Registry struct {
	entries map[int64]Script
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[int64]Script)}
}

func (r *Registry) Register(role int, s Script) error {
	if s.Ver < 1 || s.Ver > 1_000_000 {
		return ErrInvalid
	}
	if role < 1 || role > 2 {
		return ErrPermission
	}
	r.entries[s.Ver] = s
	return nil
}

func (r *Registry) Get(ver int64) (Script, bool) {
	s, ok := r.entries[ver]
	return s, ok
}

func (r *Registry) List() []Script {
	result := make([]Script, 0, len(r.entries))
	for _, s := range r.entries {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Ver < result[j].Ver
	})
	return result
}
