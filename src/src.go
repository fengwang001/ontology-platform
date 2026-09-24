// Package src is the versioned dimension table with tombstones,
// the CDC event queue, and read-token generation.
// Not goroutine-safe; callers (package api) must serialize.
package src

import "errors"

var (
	ErrEmptyKey = errors.New("src: empty key")
	ErrNotExist = errors.New("src: delete of non-existent key")
)

// Kind of a CDC event.
type Kind int

const (
	Upsert Kind = iota
	Delete
)

// Event is one CDC record: key k reached Version via Kind.
type Event struct {
	Key     string
	Version int64
	Kind    Kind
}

// Token carries one source read (value-or-absent + version); single-use.
type Token struct {
	ID     int64
	Key    string
	Val    string
	Ver    int64
	Exists bool
}

type row struct {
	val  string
	ver  int64
	tomb bool
}

// Source is the dimension table plus its pending CDC queue.
type Source struct {
	rows  map[string]row
	queue []Event
	reads int
	next  int64
}

// New returns an empty source.
func New() *Source { return &Source{rows: map[string]row{}} }

// Update upserts key, bumps its version, enqueues an Upsert event.
func (s *Source) Update(key, val string) error {
	if key == "" {
		return ErrEmptyKey
	}
	r := s.rows[key]
	s.rows[key] = row{val: val, ver: r.ver + 1}
	s.queue = append(s.queue, Event{key, r.ver + 1, Upsert})
	return nil
}

// Delete tombstones key, bumps its version, enqueues a Delete event.
// Deleting a never-existing or already-deleted key is an error.
func (s *Source) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	r, ok := s.rows[key]
	if !ok || r.tomb {
		return ErrNotExist
	}
	s.rows[key] = row{ver: r.ver + 1, tomb: true}
	s.queue = append(s.queue, Event{key, r.ver + 1, Delete})
	return nil
}

// BeginRead snapshots the current state of key into a fresh token.
func (s *Source) BeginRead(key string) (Token, error) {
	if key == "" {
		return Token{}, ErrEmptyKey
	}
	s.reads++
	s.next++
	t := Token{ID: s.next, Key: key}
	if r, ok := s.rows[key]; ok {
		t.Ver, t.Exists, t.Val = r.ver, !r.tomb, r.val
	}
	return t, nil
}

// Peek returns the up-to-n oldest pending events without removing them
// (n <= 0 yields none; n larger than the queue yields all).
func (s *Source) Peek(n int) []Event {
	if n <= 0 {
		return nil
	}
	if n > len(s.queue) {
		n = len(s.queue)
	}
	return append([]Event(nil), s.queue[:n]...)
}

// Drop removes the k oldest events; caller must have Peeked exactly them.
func (s *Source) Drop(k int) { s.queue = append([]Event(nil), s.queue[k:]...) }

// Reads is the cumulative number of source reads (BeginRead calls).
func (s *Source) Reads() int { return s.reads }

// Pending is the number of undelivered CDC events.
func (s *Source) Pending() int { return len(s.queue) }

// State is the raw current state of key: the no-cache reference.
func (s *Source) State(key string) (val string, exists bool) {
	r, ok := s.rows[key]
	return r.val, ok && !r.tomb
}
