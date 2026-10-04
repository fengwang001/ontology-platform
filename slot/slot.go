// Package slot manages the global execution slots and the FIFO waiting queue.
//
// Both Running and Cancelling runs hold one slot; Waiting placeholders wait in
// a strictly FIFO queue ordered by the instant they became placeholders. Queue
// entries carry their container/list element, so a Waiting run cancelled from
// the middle of the queue is removed in O(1) without scanning.
package slot

import "container/list"

// entry is one waiting placeholder in the queue.
type entry struct {
	id int64
	// elem is the list element once enqueued; sched uses it for O(1) removal.
	elem *list.Element
}

// Manager owns C slots and the waiting queue. It is not internally
// synchronized: callers (package sched) hold the scheduler lock.
type Manager struct {
	capacity int
	held     int
	queue    *list.List // values are *entry
}

// New creates a Manager with capacity slots (C >= 1).
func New(capacity int) *Manager {
	return &Manager{capacity: capacity, queue: list.New()}
}

// Free reports how many slots are currently unoccupied.
func (m *Manager) Free() int { return m.capacity - m.held }

// Held reports how many slots are occupied (Running + Cancelling).
func (m *Manager) Held() int { return m.held }

// Capacity reports C.
func (m *Manager) Capacity() int { return m.capacity }

// Queued reports the waiting-queue length.
func (m *Manager) Queued() int { return m.queue.Len() }

// Enqueue appends id to the tail and returns an opaque token that callers keep
// for O(1) cancellation. Pending promotion bypasses the Submit limit, so this
// never rejects.
func (m *Manager) Enqueue(id int64) *list.Element {
	return m.queue.PushBack(&entry{id: id})
}

// Remove takes a waiting placeholder out of the queue by its token.
func (m *Manager) Remove(elem *list.Element) {
	if elem == nil {
		return
	}
	if e, ok := elem.Value.(*entry); ok {
		e.elem = nil
	}
	m.queue.Remove(elem)
}

// Front reports the id at the queue head without changing anything.
func (m *Manager) Front() int64 {
	e := m.queue.Front()
	if e == nil {
		return 0
	}
	return e.Value.(*entry).id
}

// Pop moves the head placeholder out of the queue and grants it one slot.
// It returns 0 when the queue is empty; callers must first ensure a free slot.
func (m *Manager) Pop() int64 {
	e := m.queue.Front()
	if e == nil {
		return 0
	}
	m.queue.Remove(e)
	m.held++
	return e.Value.(*entry).id
}

// Release frees one slot when a Running or Cancelling run reaches a terminal
// state.
func (m *Manager) Release() {
	if m.held > 0 {
		m.held--
	}
}

// CanPop reports whether the allocation step would move the head to Running.
func (m *Manager) CanPop() bool { return m.held < m.capacity && m.queue.Len() > 0 }

// PeekList exposes the underlying list for cross-checking queue order in
// tests. Values are opaque; tests read them via ValueAt.
func (m *Manager) PeekList() *list.List { return m.queue }

// ValueAt returns the run id stored in a queue element.
func ValueAt(e *list.Element) int64 { return e.Value.(*entry).id }
