// Package semi maintains the incremental SEMI JOIN view (left rows, right
// reference counts, retained ids) and depends only on package key.
package semi

import (
	"errors"
	"sort"
	"sync"

	"ontology/key"
)

// Sentinel errors; decidable via errors.Is and mutually distinct.
var (
	ErrInvalidMaxLeft = errors.New("semi: maxLeft must be positive")
	ErrLeftIDExists   = errors.New("semi: left id already exists")
	ErrLeftTableFull  = errors.New("semi: left table exceeds maxLeft")
	ErrLeftNotFound   = errors.New("semi: left id not found")
	ErrRightUnderflow = errors.New("semi: right ref would become negative")
)

// Semi is the in-process SEMI JOIN state. Use New to create one.
type Semi struct {
	mu      sync.RWMutex
	maxLeft int
	left    map[int64]*string             // id -> key (nil = NULL)
	byKey   map[string]map[int64]struct{} // non-NULL key -> ids under it
	ref     map[string]int                // non-NULL right key -> refcount
	nullRef int                           // NULL right keys' refcount
	lit     map[int64]struct{}            // retained ids: non-NIL key, ref>=1
	// lastScan: left rows inspected while locating rows affected by the
	// latest AddRight/DelRight. Unexported; same-package tests only.
	lastScan int
}

// New creates a Semi that accepts at most maxLeft left rows.
func New(maxLeft int) (*Semi, error) {
	if maxLeft <= 0 {
		return nil, ErrInvalidMaxLeft
	}
	return &Semi{maxLeft: maxLeft, left: map[int64]*string{},
		byKey: map[string]map[int64]struct{}{}, ref: map[string]int{},
		lit: map[int64]struct{}{}}, nil
}

func (s *Semi) AddLeft(id int64, k *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.left[id]; ok {
		return ErrLeftIDExists
	}
	if len(s.left) >= s.maxLeft {
		return ErrLeftTableFull
	}
	s.left[id] = k
	if v, ok := key.Value(k); ok {
		g := s.byKey[v]
		if g == nil {
			g = map[int64]struct{}{}
			s.byKey[v] = g
		}
		g[id] = struct{}{}
		if s.ref[v] >= 1 { // existence: light at insert when right exists
			s.lit[id] = struct{}{}
		}
	}
	return nil
}

func (s *Semi) DelLeft(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.left[id]
	if !ok {
		return ErrLeftNotFound
	}
	delete(s.left, id)
	delete(s.lit, id)
	if v, ok := key.Value(k); ok {
		if g := s.byKey[v]; g != nil {
			delete(g, id)
			if len(g) == 0 {
				delete(s.byKey, v)
			}
		}
	}
	return nil
}

func (s *Semi) AddRight(k *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k == nil {
		s.nullRef++ // NULL right rows never produce a match
		s.lastScan = 0
		return nil
	}
	v := *k
	if s.ref[v] == 0 {
		g := s.byKey[v]
		s.lastScan = len(g)
		for id := range g {
			s.lit[id] = struct{}{}
		}
	} else {
		s.lastScan = 0 // 1->2 changes nothing: semi join does not multiply
	}
	s.ref[v]++
	return nil
}

func (s *Semi) DelRight(k *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k == nil {
		if s.nullRef <= 0 {
			return ErrRightUnderflow
		}
		s.nullRef--
		s.lastScan = 0
		return nil
	}
	v := *k
	if s.ref[v] <= 0 {
		return ErrRightUnderflow
	}
	if s.ref[v] == 1 {
		g := s.byKey[v]
		s.lastScan = len(g)
		for id := range g {
			delete(s.lit, id)
		}
	} else {
		s.lastScan = 0
	}
	s.ref[v]--
	return nil
}

func (s *Semi) View() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]int64, 0, len(s.lit))
	for id := range s.lit {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
