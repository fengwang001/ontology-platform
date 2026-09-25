// Package id provides a typed element-index wrapper around uf.
package id

import (
	"errors"

	"ontology/uf"
)

var (
	// ErrNegative reports an index below zero.
	ErrNegative = errors.New("id: element index must not be negative")
	// ErrTooLarge reports an index at or beyond the number of elements.
	ErrTooLarge = errors.New("id: element index exceeds element count")
)

// Set is a union-find keyed by typed element indices.
type Set struct {
	set *uf.UF
}

// New creates n singleton elements.
func New(n int) *Set { return &Set{set: uf.New(n)} }

func (s *Set) guard(x int) error {
	switch {
	case x < 0:
		return ErrNegative
	case x >= s.set.Len():
		return ErrTooLarge
	default:
		return nil
	}
}

// Find returns the representative root of element x.
func (s *Set) Find(x int) (int, error) {
	if err := s.guard(x); err != nil {
		return 0, err
	}
	return s.set.Find(x)
}

// Union merges the components of x and y.
func (s *Set) Union(x, y int) (bool, error) {
	if err := s.guard(x); err != nil {
		return false, err
	}
	if err := s.guard(y); err != nil {
		return false, err
	}
	return s.set.Union(x, y)
}

// Connected reports whether x and y share a component.
func (s *Set) Connected(x, y int) (bool, error) {
	if err := s.guard(x); err != nil {
		return false, err
	}
	if err := s.guard(y); err != nil {
		return false, err
	}
	return s.set.Connected(x, y)
}

// Count returns the number of components.
func (s *Set) Count() int { return s.set.Count() }
