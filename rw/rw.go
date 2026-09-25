// Package rw holds the lock state (held readers, held writer,
// waiting-writer flag) and the grant conditions of a
// writer-preference readers-writer lock. It depends on nothing.
package rw

import "sort"

// State is the mutable core. It is not goroutine-safe; the caller
// (package lock) serializes all access.
type State struct {
	readers        map[string]struct{}
	writer         string
	waitingWriters int
	checked        int // waiter entries inspected by the last grant decision
}

// NewState returns an empty state: no readers, no writer, no waiters.
func NewState() *State { return &State{readers: make(map[string]struct{})} }

// CanRead reports whether a new reader may enter: no writer held and
// no writer waiting (writer preference). The decision reads one
// aggregate flag; it never scans the waiting queue, so checked is 1
// regardless of how many writers are queued.
func (s *State) CanRead() bool {
	s.checked = 1
	return s.writer == "" && s.waitingWriters == 0
}

// CanWrite reports whether a new writer may enter: no writer held and
// no readers held.
func (s *State) CanWrite() bool {
	s.checked = 1
	return s.writer == "" && len(s.readers) == 0
}

// AddReader records id as a held reader.
func (s *State) AddReader(id string) { s.readers[id] = struct{}{} }

// RemoveReader drops id from the held readers.
func (s *State) RemoveReader(id string) { delete(s.readers, id) }

// SetWriter records id as the held writer.
func (s *State) SetWriter(id string) { s.writer = id }

// ClearWriter releases the held writer.
func (s *State) ClearWriter() { s.writer = "" }

// WriterEnqueued notes one more waiting writer.
func (s *State) WriterEnqueued() { s.waitingWriters++ }

// WriterDequeued notes one less waiting writer.
func (s *State) WriterDequeued() { s.waitingWriters-- }

// WriterWaiting reports whether any writer is queued.
func (s *State) WriterWaiting() bool { return s.waitingWriters > 0 }

// ReaderCount returns how many readers currently hold the lock.
func (s *State) ReaderCount() int { return len(s.readers) }

// Writer returns the held writer's id, or "" if none.
func (s *State) Writer() string { return s.writer }

// HoldsRead reports whether id currently holds a read lock.
func (s *State) HoldsRead(id string) bool {
	_, ok := s.readers[id]
	return ok
}

// HoldsWrite reports whether id currently holds the write lock.
func (s *State) HoldsWrite(id string) bool { return s.writer != "" && s.writer == id }

// Readers returns the held reader ids in sorted (deterministic) order.
func (s *State) Readers() []string {
	out := make([]string, 0, len(s.readers))
	for id := range s.readers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
