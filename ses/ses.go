// Package ses tracks session write versions, follower Sync and read routing.
package ses

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/kv"
)

// Decidable sentinel errors; the three fault-injection errors are distinct.
var (
	ErrEmptyKey       = errors.New("ses: empty key")
	ErrSyncIndex      = errors.New("ses: follower index out of range (want 1 or 2)")
	ErrSessionClosed  = errors.New("ses: session is closed")
	ErrRouteCheckCost = errors.New("ses: routing examined more than one (replica,key) entry")
)

const sticky = 1 // every session's fixed sticky follower: R1

// Session is a client's private write-version table; sessions are independent.
type Session struct {
	mu       sync.Mutex
	closed   bool
	writeVer map[string]int64
}

// Cluster is master R0, two followers, and the unexported routing counter.
type Cluster struct {
	master   *kv.Master
	follower [2]*kv.Replica
	checks   atomic.Int64 // (replica,key) entries checked by the latest Read
}

// NewCluster returns R0 and two empty followers.
func NewCluster() *Cluster {
	return &Cluster{master: kv.NewMaster(), follower: [2]*kv.Replica{kv.NewReplica(), kv.NewReplica()}}
}

// NewSession opens a session pinned to sticky follower R1.
func NewSession() *Session { return &Session{writeVer: map[string]int64{}} }

// WriteVersion returns this session's last written version for key (0 if it
// never wrote it); session state, never the check counter.
func (s *Session) WriteVersion(key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeVer[key]
}

// Close marks the session closed; later Write/Read are rejected. Idempotent.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

// precheck rejects empty key and closed session before any state, version or
// writeVer change, and returns the session write version.
func (s *Session) precheck(key string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrSessionClosed
	}
	return s.writeVer[key], nil
}

// Write advances the master version, stores on R0 only and records the
// session write version. Followers are not touched.
func (c *Cluster) Write(s *Session, key, val string) (int64, error) {
	if _, err := s.precheck(key); err != nil {
		return 0, err
	}
	v := c.master.Advance(key, val)
	s.mu.Lock()
	s.writeVer[key] = v
	s.mu.Unlock()
	return v, nil
}

// entryVisible performs one routing check of a single (replica,key) entry.
func (c *Cluster) entryVisible(r *kv.Replica, key string, wv int64) (kv.Entry, bool) {
	c.checks.Add(1)
	e, ok := r.Get(key)
	return e, ok && e.AtLeast(wv)
}

// Read serves the sticky follower when its entry is at least this session's
// write version, otherwise routes to master R0.
func (c *Cluster) Read(s *Session, key string) (val string, ver int64, err error) {
	wv, err := s.precheck(key)
	if err != nil {
		return "", 0, err
	}
	c.checks.Store(0)
	if e, ok := c.entryVisible(c.follower[sticky-1], key, wv); ok {
		return e.Val, e.Ver, nil
	}
	if e, ok := c.master.Get(key); ok {
		return e.Val, e.Ver, nil
	}
	return "", 0, nil
}

// Sync copies the whole master state onto follower idx (1 or 2).
func (c *Cluster) Sync(idx int) error {
	if idx < 1 || idx > 2 {
		return ErrSyncIndex // rejected before touching any replica
	}
	c.follower[idx-1].Load(c.master.Snapshot())
	return nil
}

// View returns the authoritative (naive-reference) state: R0's snapshot.
func (c *Cluster) View() map[string]kv.Entry { return c.master.Snapshot() }

// Snapshots returns deep copies of R0, R1, R2 (diagnostics/demo only).
func (c *Cluster) Snapshots() (r0, r1, r2 map[string]kv.Entry) {
	return c.master.Snapshot(), c.follower[0].Snapshot(), c.follower[1].Snapshot()
}

// VerifyRouteCheckCost asserts the routing check count is a small constant
// independent of m (100..10000); returns only the sentinel, never the value.
func VerifyRouteCheckCost() error {
	c, s := NewCluster(), NewSession()
	var prev int64 = -1
	for _, m := range []int{100, 1000, 10000} {
		for i := 0; i < m; i++ {
			c.master.Advance(fmt.Sprintf("k%05d", i), "x")
		}
		if _, err := c.Write(s, "k00000", "v"); err != nil {
			return err
		}
		if _, _, err := c.Read(s, "k00000"); err != nil {
			return err
		}
		got := c.checks.Load()
		if got > 1 || (prev >= 0 && got != prev) {
			return ErrRouteCheckCost
		}
		prev = got
	}
	return nil
}
