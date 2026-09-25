// Package ses keeps session state (writeVer), follower catch-up (Sync)
// and read routing (sticky follower vs primary). Depends only on kv.
package ses

import (
	"errors"
	"sync"

	"ontology/kv"
)

// Sentinel errors: distinguishable via errors.Is.
var (
	ErrEmptyKey = errors.New("ses: empty key")
	ErrBadIndex = errors.New("ses: replica index out of range")
	ErrClosed   = errors.New("ses: session closed")
)

// Cluster is primary R0 plus followers R1/R2 (fol[0]=R1, fol[1]=R2).
type Cluster struct {
	mu  sync.RWMutex
	pri *kv.Primary
	fol [2]*kv.Replica
}

// NewCluster returns an empty three-replica cluster.
func NewCluster() *Cluster {
	return &Cluster{
		pri: kv.NewPrimary(),
		fol: [2]*kv.Replica{kv.NewReplica(), kv.NewReplica()},
	}
}

// Open creates a session with its own independent writeVer.
func (c *Cluster) Open() *Session {
	return &Session{c: c, writeVer: make(map[string]int64)}
}

// Sync copies the primary's whole state onto follower idx (1 or 2).
// Versions on the follower never regress: primary versions only grow.
func (c *Cluster) Sync(idx int) error {
	if idx != 1 && idx != 2 {
		return ErrBadIndex
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	dst := c.fol[idx-1]
	for k, e := range c.pri.Replica().Snapshot() {
		dst.Put(k, e)
	}
	return nil
}

// Snapshot returns copies of all three tables: [0]=R0, [1]=R1, [2]=R2.
func (c *Cluster) Snapshot() [3]map[string]kv.Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return [3]map[string]kv.Entry{
		c.pri.Replica().Snapshot(), c.fol[0].Snapshot(), c.fol[1].Snapshot(),
	}
}

// Session is one client's read/write context. Sticky follower: R1.
type Session struct {
	c        *Cluster
	mu       sync.Mutex
	writeVer map[string]int64
	closed   bool
	checked  int // (replica, key) entries inspected by the latest Read routing
}

// Write appends to the primary and records writeVer[key] = v.
// A rejected write changes nothing.
func (s *Session) Write(key, val string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrClosed
	}
	s.c.mu.Lock()
	e := s.c.pri.Write(key, val)
	s.c.mu.Unlock()
	s.writeVer[key] = e.Ver
	return e.Ver, nil
}

// Read routes to sticky follower R1 when its version of key covers this
// session's write (ver >= writeVer[key]); otherwise it falls back to R0,
// guaranteeing read-your-writes. A rejected read changes nothing.
func (s *Session) Read(key string) (kv.Entry, error) {
	if key == "" {
		return kv.Entry{}, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return kv.Entry{}, ErrClosed
	}
	wv := s.writeVer[key]
	s.c.mu.RLock()
	defer s.c.mu.RUnlock()
	s.checked = 1 // routing inspects exactly one (replica, key) entry: R1's
	if s.c.fol[0].CaughtUp(key, wv) {
		return s.c.fol[0].Get(key), nil
	}
	return s.c.pri.Replica().Get(key), nil
}

// Close shuts the session; later Write/Read are rejected with ErrClosed.
func (s *Session) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}
