// Package pq manages queue state on top of ipq: registration sequence
// assignment, active-id bookkeeping and validation of every operation.
// All validation happens before any state mutation, so a rejected
// operation leaves no trace.
package pq

import (
	"errors"

	"ontology/ipq"
)

// Sentinel errors; each failure mode is individually decidable.
var (
	ErrEmptyID        = errors.New("pq: empty id")
	ErrDuplicateID    = errors.New("pq: duplicate active id")
	ErrUpdateNotFound = errors.New("pq: update of unknown id")
	ErrDeleteNotFound = errors.New("pq: delete of unknown id")
	errUnreachable    = errors.New("pq: internal state mismatch")
)

// PQ is an indexed priority queue of (id, priority) elements.
type PQ struct {
	h   *ipq.Heap
	seq int64
}

// New returns an empty queue.
func New() *PQ { return &PQ{h: ipq.New()} }

// Len reports the number of live elements.
func (p *PQ) Len() int { return p.h.Len() }

// Push adds id with priority pri and assigns it the next registration
// sequence number. Fails on empty id or an id that is still active.
func (p *PQ) Push(id string, pri int) error {
	if id == "" {
		return ErrEmptyID
	}
	if p.h.Contains(id) {
		return ErrDuplicateID
	}
	p.seq++
	p.h.Push(ipq.Item{ID: id, Pri: pri, Seq: p.seq})
	return nil
}

// Pop removes and returns the (priority, seq)-minimum element.
func (p *PQ) Pop() (id string, pri int, ok bool) {
	it, ok := p.h.Pop()
	if !ok {
		return "", 0, false
	}
	return it.ID, it.Pri, true
}

// Peek returns the minimum element without removing it.
func (p *PQ) Peek() (id string, pri int, ok bool) {
	it, ok := p.h.Peek()
	if !ok {
		return "", 0, false
	}
	return it.ID, it.Pri, true
}

// UpdatePriority changes the priority of a live id; the registration
// sequence is untouched. Fails when id is not present.
func (p *PQ) UpdatePriority(id string, pri int) error {
	if !p.h.Contains(id) {
		return ErrUpdateNotFound
	}
	if !p.h.Update(id, pri) {
		return errUnreachable
	}
	return nil
}

// Delete removes a live id entirely; the id may be Pushed again
// afterwards as a new element. Fails when id is not present.
func (p *PQ) Delete(id string) error {
	if !p.h.Contains(id) {
		return ErrDeleteNotFound
	}
	if !p.h.Delete(id) {
		return errUnreachable
	}
	return nil
}
