// Package resume records and restores checkpoints of the write pipeline:
// confirmed output position plus everything needed to regenerate the rest.
package resume

import (
	"sync"

	"ontology/chunker"
)

// Checkpoint captures enough state to rebuild a pipeline that continues the
// exact same output stream at the first unconfirmed byte.
type Checkpoint struct {
	Accepted   int64         // output bytes generated so far
	Confirmed  int64         // output bytes confirmed by downstream
	Chunks     int64         // data chunks produced so far
	Closed     bool          // terminating chunk already emitted
	Chunker    chunker.State // pending-bytes window state
	Backlog    [][]byte      // encoded but unconfirmed output pieces, in order
	RawPending []byte        // raw payload of the still-open chunk
	Sizes      []int         // payload sizes of data chunks produced so far
}

// Pending returns the number of buffered, unconfirmed output bytes.
func (c Checkpoint) Pending() int64 {
	n := int64(len(c.RawPending))
	for _, p := range c.Backlog {
		n += int64(len(p))
	}
	return n
}

// Store is an in-memory checkpoint holder, safe for concurrent use.
type Store struct {
	mu  sync.Mutex
	cp  Checkpoint
	has bool
}

// Save replaces the stored checkpoint.
func (s *Store) Save(cp Checkpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cp = cp
	s.has = true
}

// Load returns the stored checkpoint and whether one exists.
func (s *Store) Load() (Checkpoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cp, s.has
}
