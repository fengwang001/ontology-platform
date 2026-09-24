// Package fk validates and executes the four changelog operations against a
// sch.State. Every rejection is decided before any state mutation, so a
// rejected operation leaves no trace.
package fk

import (
	"errors"

	"ontology/sch"
)

// Sentinel errors: the four rejections are pairwise distinct and decidable
// with errors.Is.
var (
	// ErrOrphanChild: C.ins whose parent is currently absent.
	ErrOrphanChild = errors.New("fk: child insert rejected, parent does not exist")
	// ErrParentReferenced: P.del while at least one child references it.
	ErrParentReferenced = errors.New("fk: parent delete rejected, still referenced by a child")
	// ErrParentNotFound: P.del on an absent parent.
	ErrParentNotFound = errors.New("fk: parent delete rejected, parent does not exist")
	// ErrChildNotFound: C.del on an absent child.
	ErrChildNotFound = errors.New("fk: child delete rejected, child does not exist")
)

// Engine executes operations on one state.
type Engine struct {
	st *sch.State
}

// New wraps a state.
func New(st *sch.State) *Engine { return &Engine{st: st} }

// PIns inserts a parent; an existing one is an idempotent no-op success.
func (e *Engine) PIns(pk string) error {
	if e.st.HasParent(pk) {
		return nil // idempotent: nothing changes
	}
	e.st.AddParent(pk)
	return nil
}

// PDel rejects a referenced parent (ErrParentReferenced) and a missing parent
// (ErrParentNotFound); otherwise deletes. Reference check comes from the
// reference count, never from scanning children.
func (e *Engine) PDel(pk string) error {
	exists, refs := e.st.ProbeParentDelete(pk)
	if !exists {
		return ErrParentNotFound
	}
	if refs > 0 {
		return ErrParentReferenced
	}
	e.st.RemoveParent(pk)
	return nil
}

// CIns inserts a child only if the parent exists at this instant; there is no
// wait queue, a rejected insert is not remembered. An already-present ck is
// an idempotent no-op (the rules define exactly four rejections, none for a
// duplicate child).
func (e *Engine) CIns(ck, parent string) error {
	if !e.st.HasParent(parent) {
		return ErrOrphanChild
	}
	if e.st.HasChild(ck) {
		return nil
	}
	e.st.AddChild(ck, parent)
	return nil
}

// CDel deletes an existing child; a missing one is ErrChildNotFound.
func (e *Engine) CDel(ck string) error {
	if !e.st.HasChild(ck) {
		return ErrChildNotFound
	}
	e.st.RemoveChild(ck)
	return nil
}
