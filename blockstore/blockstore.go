package blockstore

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"sort"
	"sync"
)

// State of a block. The state of every block is always exactly one of
// Normal, Pending or Deleted.
type State uint8

const (
	// Normal blocks are readable and are either referenced or freshly stored.
	Normal State = iota
	// Pending blocks were marked unreachable by the first GC phase; they
	// remain physically present and readable until the second phase.
	Pending
	// Deleted blocks have been physically removed.
	Deleted
)

func (s State) String() string {
	switch s {
	case Normal:
		return "normal"
	case Pending:
		return "pending"
	default:
		return "deleted"
	}
}

// block is the in-memory record for one content digest.
type block struct {
	data  []byte
	state State
}

// session is one backup write session: blocks may be uploaded while the
// snapshot is being built, then the manifest is atomically committed.
type session struct {
	id     string
	open   bool // false after Commit/End
	commit bool // true once Commit succeeded (guards duplicate commits)
}

// gcCycle captures the two-phase GC bookkeeping.
type gcCycle struct {
	pending     map[string]struct{} // blocks marked at Mark time
	registrants map[string]struct{} // sessions in-flight at Mark time
	swept       bool
}

// Store is a concurrent-safe, content-addressed, deduplicating block store
// with capacity enforcement and two-phase garbage collection.
type Store struct {
	mu       sync.Mutex
	blocks   map[string]*block
	manifest map[string][]string // snapshot id -> referenced digests
	sessions map[string]*session
	used     int64
	capacity int64 // 0 means unlimited
	gc       *gcCycle
	log      *slog.Logger
}

// Config configures a new Store.
type Config struct {
	Capacity int64
	Log      io.Writer
}

// New creates an empty store.
func New(cfg Config) *Store {
	return &Store{
		blocks:   make(map[string]*block),
		manifest: make(map[string][]string),
		sessions: make(map[string]*session),
		capacity: cfg.Capacity,
		log:      newLogger(cfg.Log),
	}
}

// Digest returns the content digest (SHA-256 hex) of data.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// BeginSession opens a backup write session. Reusing a live id is rejected.
func (s *Store) BeginSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; ok {
		s.log.Info("BeginSession", "session", id, "result", "rejected",
			"reason", "session id already exists")
		return ErrSessionNotFound
	}
	s.sessions[id] = &session{id: id, open: true}
	s.log.Info("BeginSession", "session", id, "result", "started")
	return nil
}

// openSessionLocked returns the session iff it is still accepting writes.
func (s *Store) openSessionLocked(sessionID string) (*session, error) {
	sess := s.sessions[sessionID]
	switch {
	case sess == nil || (!sess.open && !sess.commit):
		return nil, ErrSessionNotFound
	case sess.commit:
		return nil, ErrDuplicateCommit
	default:
		return sess, nil
	}
}

// restorePendingLocked moves a Pending block back to Normal and removes it
// from the active GC cycle: reusing a pending block revives it.
func (s *Store) restorePendingLocked(digest string) {
	if b := s.blocks[digest]; b != nil && b.state == Pending {
		b.state = Normal
		if s.gc != nil {
			delete(s.gc.pending, digest)
		}
	}
}

// Upload stores data for an open session, deduplicating against existing
// blocks (and restoring Pending blocks to Normal on reuse).
func (s *Store) Upload(sessionID string, data []byte) (digest string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.openSessionLocked(sessionID)
	if err != nil {
		s.log.Info("Upload", "session", sessionID, "size", len(data),
			"result", "rejected", "reason", err.Error())
		return "", err
	}
	_ = sess

	digest = Digest(data)
	if b, ok := s.blocks[digest]; ok {
		decision := "deduplicated"
		if b.state == Pending {
			s.restorePendingLocked(digest)
			decision = "restored-pending-to-normal"
		}
		s.log.Info("Upload", "session", sessionID, "digest", digest,
			"size", len(data), "result", "reused", "decision", decision)
		return digest, nil
	}

	if s.capacity > 0 && s.used+int64(len(data)) > s.capacity {
		s.log.Info("Upload", "session", sessionID, "digest", digest,
			"size", len(data), "used", s.used, "capacity", s.capacity,
			"result", "rejected", "reason", ErrCapacityFull.Error())
		return "", ErrCapacityFull
	}

	s.blocks[digest] = &block{data: append([]byte(nil), data...), state: Normal}
	s.used += int64(len(data))
	s.log.Info("Upload", "session", sessionID, "digest", digest,
		"size", len(data), "used", s.used, "result", "stored",
		"decision", "new-normal-block")
	return digest, nil
}

// Commit atomically publishes a snapshot manifest.
func (s *Store) Commit(sessionID, snapshotID string, digests []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess := s.sessions[sessionID]
	switch {
	case sess == nil || (!sess.open && !sess.commit):
		s.log.Info("Commit", "session", sessionID, "snapshot", snapshotID,
			"refs", len(digests), "result", "rejected",
			"reason", ErrSessionNotFound.Error())
		return ErrSessionNotFound
	case sess.commit:
		s.log.Info("Commit", "session", sessionID, "snapshot", snapshotID,
			"refs", len(digests), "result", "rejected",
			"reason", ErrDuplicateCommit.Error())
		return ErrDuplicateCommit
	}

	// Validate the whole manifest before any mutation: every referenced
	// digest must exist physically (Normal or still-readable Pending).
	seen := make(map[string]struct{}, len(digests))
	for _, digest := range digests {
		if _, dup := seen[digest]; dup {
			continue
		}
		seen[digest] = struct{}{}
		if _, ok := s.blocks[digest]; !ok {
			s.log.Info("Commit", "session", sessionID, "snapshot", snapshotID,
				"missing", digest, "result", "rejected",
				"reason", ErrBlockMissing.Error())
			return ErrBlockMissing
		}
	}

	// Publish atomically: manifest first, then revive every Pending block
	// the new manifest references, then close the session.
	refs := append([]string(nil), digests...)
	s.manifest[snapshotID] = refs
	restored := 0
	for digest := range seen {
		if b := s.blocks[digest]; b.state == Pending {
			s.restorePendingLocked(digest)
			restored++
		}
	}
	sess.open = false
	sess.commit = true

	s.log.Info("Commit", "session", sessionID, "snapshot", snapshotID,
		"refs", len(refs), "restored", restored, "result", "committed",
		"decision", "manifest published atomically")
	return nil
}

// End closes a session without committing.
func (s *Store) End(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[sessionID]
	if sess == nil {
		s.log.Info("End", "session", sessionID, "result", "noop",
			"reason", "session does not exist")
		return
	}
	wasOpen := sess.open
	sess.open = false
	s.log.Info("End", "session", sessionID, "wasOpen", wasOpen,
		"committed", sess.commit, "result", "ended")
}

// Read returns the data of any Normal or Pending block.
func (s *Store) Read(digest string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.blocks[digest]
	if b == nil {
		s.log.Info("Read", "digest", digest, "result", "rejected",
			"reason", ErrBlockDeleted.Error())
		return nil, ErrBlockDeleted
	}
	s.log.Info("Read", "digest", digest, "state", b.state.String(),
		"size", len(b.data), "result", "ok")
	return append([]byte(nil), b.data...), nil
}

// referencedLocked computes the union of digests referenced by any committed
// manifest.
func (s *Store) referencedLocked() map[string]struct{} {
	ref := make(map[string]struct{})
	for _, digests := range s.manifest {
		for _, digest := range digests {
			ref[digest] = struct{}{}
		}
	}
	return ref
}

// Mark is GC phase one: marks unreferenced blocks Pending and records the
// sessions currently in-flight.
func (s *Store) Mark() (marked int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	referenced := s.referencedLocked()
	pending := make(map[string]struct{})
	for digest, b := range s.blocks {
		if _, reachable := referenced[digest]; reachable {
			if b.state == Pending {
				b.state = Normal
			}
			continue
		}
		if b.state == Normal {
			b.state = Pending
			marked++
		}
		pending[digest] = struct{}{}
	}

	registrants := make(map[string]struct{})
	for id, sess := range s.sessions {
		if sess.open {
			registrants[id] = struct{}{}
		}
	}
	s.gc = &gcCycle{pending: pending, registrants: registrants}

	s.log.Info("Mark", "marked", marked, "pendingTotal", len(pending),
		"snapshots", len(s.manifest), "registrants", len(registrants),
		"result", "phase1-done",
		"decision", "unreferenced blocks kept readable as pending")
	return marked
}

// Sweep is GC phase two: deletes Pending blocks still unreferenced only when
// every session in-flight at Mark time has ended; otherwise deletes nothing.
func (s *Store) Sweep() (deleted int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.gc == nil || s.gc.swept {
		s.log.Info("Sweep", "result", "rejected",
			"reason", ErrNoGCCycle.Error())
		return 0, ErrNoGCCycle
	}

	var open []string
	for id := range s.gc.registrants {
		if sess := s.sessions[id]; sess != nil && sess.open {
			open = append(open, id)
		}
	}
	if len(open) > 0 {
		sort.Strings(open)
		s.log.Info("Sweep", "blockedBy", open, "result", "blocked",
			"decision", "registrant sessions still in flight; delete nothing")
		// The second phase does not complete while registrants remain open;
		// the cycle stays active so Sweep can be retried later.
		return 0, nil
	}

	referenced := s.referencedLocked()
	candidates := make([]string, 0, len(s.gc.pending))
	for digest := range s.gc.pending {
		candidates = append(candidates, digest)
	}
	sort.Strings(candidates)

	var removed, revived []string
	for _, digest := range candidates {
		b := s.blocks[digest]
		if b == nil {
			continue
		}
		if _, reachable := referenced[digest]; reachable {
			if b.state == Pending {
				b.state = Normal
				revived = append(revived, digest)
			}
			continue
		}
		s.used -= int64(len(b.data))
		delete(s.blocks, digest)
		removed = append(removed, digest)
		deleted++
	}
	s.gc.swept = true

	s.log.Info("Sweep", "deleted", deleted, "revived", revived,
		"removedDigests", removed, "result", "phase2-done",
		"decision", "all registrants ended; unreferenced pending blocks deleted")
	return deleted, nil
}

// StateOf reports the current state of a digest.
func (s *Store) StateOf(digest string) State {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.blocks[digest]; ok {
		return b.state
	}
	return Deleted
}

// SnapshotIDs returns the committed snapshot ids.
func (s *Store) SnapshotIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.manifest))
	for id := range s.manifest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
