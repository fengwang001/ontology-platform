package ontology

import (
	"sync"
	"sync/atomic"
)

// ChangeKind reports which kind of mutation happened during a traversal.
// The flags are cumulative for the whole session and can be OR-combined.
type ChangeKind uint8

const (
	// ChangeNone means the collection has not changed during traversal.
	ChangeNone ChangeKind = 0
	// ChangeInserted means at least one element was inserted.
	ChangeInserted ChangeKind = 1 << iota
	// ChangeDeleted means at least one snapshot element was deleted.
	ChangeDeleted
)

// Inserted reports whether any insertion happened during the traversal.
func (c ChangeKind) Inserted() bool { return c&ChangeInserted != 0 }

// Deleted reports whether any deletion happened during the traversal.
func (c ChangeKind) Deleted() bool { return c&ChangeDeleted != 0 }

// Page is one Scan result.
type Page struct {
	Items   []Object
	Next    string
	HasMore bool
	Changed ChangeKind
	// Truncated is true exactly when the page was cut short by limit and a
	// continuation exists. It is distinct from Discarded, which counts
	// elements dropped because they were deleted mid-traversal.
	Truncated bool
	// Discarded is the number of snapshot elements skipped by this single
	// Scan because they had been deleted before being reached.
	Discarded int
}

// SkipStats is the cumulative skip accounting for one traversal session.
type SkipStats struct {
	// Total is Deleted + InsertedHidden.
	Total int
	// Deleted counts first-scan elements removed before they were reached.
	Deleted int
	// InsertedHidden counts new elements sorted at or before the scan
	// frontier, which this traversal will never show.
	InsertedHidden int
}

type pageCacheKey struct {
	index int
	limit int
}

// Session is one snapshot traversal.
type Session struct {
	store *Store
	id    uint64

	mu          sync.Mutex
	keys        []string
	alive       map[string]struct{}
	inserted    map[string]struct{}
	changed     ChangeKind
	discarded   int
	hidden      int
	cache       map[pageCacheKey]*Page
	invalidated bool
}

var sessionIDCounter uint64

// BeginTraversal takes a snapshot and starts a new cursor traversal.
func (s *Store) BeginTraversal() *Session {
	sess := &Session{
		store:    s,
		id:       atomic.AddUint64(&sessionIDCounter, 1),
		keys:     s.snapshot(),
		alive:    make(map[string]struct{}),
		inserted: make(map[string]struct{}),
		cache:    make(map[pageCacheKey]*Page),
	}
	for _, k := range sess.keys {
		sess.alive[k] = struct{}{}
	}
	s.sessions.mu.Lock()
	s.sessions.byID[sess.id] = sess
	s.sessions.mu.Unlock()
	return sess
}

// Invalidate permanently closes a traversal session. Its cursors afterwards
// fail with ErrSessionInvalid.
func (sess *Session) Invalidate() {
	sess.mu.Lock()
	sess.invalidated = true
	sess.mu.Unlock()

	s := sess.store
	s.sessions.mu.Lock()
	delete(s.sessions.byID, sess.id)
	s.sessions.mu.Unlock()
}

// Stats returns cumulative skip accounting for the session.
func (sess *Session) Stats() SkipStats {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return SkipStats{
		Total:          sess.discarded + sess.hidden,
		Deleted:        sess.discarded,
		InsertedHidden: sess.hidden,
	}
}

// notifyInsert records an insertion committed after the snapshot.
func (sess *Session) notifyInsert(key string) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.invalidated {
		return
	}
	sess.inserted[key] = struct{}{}
	sess.changed |= ChangeInserted
}

// notifyDelete records deletion of a snapshot element.
func (sess *Session) notifyDelete(key string) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.invalidated {
		return
	}
	if _, ok := sess.alive[key]; ok {
		delete(sess.alive, key)
		sess.changed |= ChangeDeleted
	}
}

// notifyInsert fans a write out to every active session after the store lock
// has been released, so scans never block writers.
func (s *Store) notifyInsert(key string) {
	s.sessions.mu.Lock()
	list := make([]*Session, 0, len(s.sessions.byID))
	for _, sess := range s.sessions.byID {
		list = append(list, sess)
	}
	s.sessions.mu.Unlock()
	for _, sess := range list {
		sess.notifyInsert(key)
	}
}

func (s *Store) notifyDelete(key string) {
	s.sessions.mu.Lock()
	list := make([]*Session, 0, len(s.sessions.byID))
	for _, sess := range s.sessions.byID {
		list = append(list, sess)
	}
	s.sessions.mu.Unlock()
	for _, sess := range list {
		sess.notifyDelete(key)
	}
}
