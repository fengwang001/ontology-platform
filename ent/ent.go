// Package ent defines the entity type and per-entity validation.
// It depends on no other package in this module.
package ent

import "errors"

// Sentinel errors give callers decidable, mutually distinct failure cases.
var (
	ErrInvalidID     = errors.New("ent: id must be >= 1")
	ErrDuplicateID   = errors.New("ent: id already exists")
	ErrInvalidParent = errors.New("ent: parent must be 0 or an existing entity id")
)

// Entity is a node with one parent pointer; Parent == 0 means a root.
type Entity struct {
	ID     int
	Parent int
}

// Validate checks e against the current set, represented by the exists
// predicate: id must be positive and unused, and parent must be 0 or
// reference an existing entity. It mutates nothing.
func (e Entity) Validate(exists func(int) bool) error {
	if e.ID < 1 {
		return ErrInvalidID
	}
	if exists(e.ID) {
		return ErrDuplicateID
	}
	if e.Parent != 0 && !exists(e.Parent) {
		return ErrInvalidParent
	}
	return nil
}

// Orphan is the single-entity orphan test: Parent != 0 and the parent is
// absent from the set. A root (Parent == 0) is never an orphan.
func (e Entity) Orphan(exists func(int) bool) bool {
	return e.Parent != 0 && !exists(e.Parent)
}
