package reflog

import "sync"

// System is the facade of the retention subsystem. All mutating
// operations are serialized by a single lock, so any interleaving of
// concurrent calls is equivalent to some serial order. ReadLog uses
// the read lock.
//
// Every call that takes a time validates, in order: invalid
// parameters, clock regression, then operation-specific existence
// checks. A rejected call has no effect: it appends no record,
// deletes no object and does not advance the clock.
type System struct {
	mu    sync.RWMutex
	cfg   Config
	now   int64 // high-water mark of accepted calls
	store *store
	logs  map[string]*refLog // one entry per reference ever created
}

// NewSystem validates cfg and returns an empty System.
func NewSystem(cfg Config) (*System, error) {
	if cfg.ReachableRetention < 0 || cfg.UnreachableRetention < 0 || cfg.FreshnessGrace < 0 {
		return nil, ErrInvalidParam
	}
	return &System{
		cfg:   cfg,
		store: newStore(),
		logs:  make(map[string]*refLog),
	}, nil
}

// clockLocked rejects a time earlier than the last accepted one.
func (s *System) clockLocked(now int64) error {
	if now < s.now {
		return ErrClockRegression
	}
	return nil
}

// WriteCommit registers a commit. The first write wins; rewriting an
// existing commit is a no-op. Parent and content references are not
// validated: dangling references are simply never traversed.
func (s *System) WriteCommit(id CommitID, parents []CommitID, createdAt int64, contents []ObjectID, size int64, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || createdAt < 0 || size < 0 || now < 0 {
		return ErrInvalidParam
	}
	if err := s.clockLocked(now); err != nil {
		return err
	}
	s.store.putCommit(&Commit{
		ID:             id,
		Parents:        append([]CommitID(nil), parents...),
		CreatedAt:      createdAt,
		Contents:       append([]ObjectID(nil), contents...),
		Size:           size,
		FirstWrittenAt: now,
	})
	s.now = now
	return nil
}

// WriteObject registers a content object. The first write wins.
func (s *System) WriteObject(id ObjectID, size int64, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || size < 0 || now < 0 {
		return ErrInvalidParam
	}
	if err := s.clockLocked(now); err != nil {
		return err
	}
	s.store.putObject(&Object{ID: id, Size: size, FirstWrittenAt: now})
	s.now = now
	return nil
}

// CreateRef moves name to commit, creating the reference if it does
// not currently exist (including recreating a previously deleted
// name, whose old records stay in the same log). When the reference
// already exists it behaves exactly like UpdateRef.
func (s *System) CreateRef(name string, commit CommitID, now int64, who string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" || now < 0 {
		return ErrInvalidParam
	}
	if err := s.clockLocked(now); err != nil {
		return err
	}
	if !s.store.hasCommit(commit) {
		return ErrCommitNotFound
	}
	l := s.logs[name]
	if l == nil {
		l = &refLog{}
		s.logs[name] = l
	}
	s.moveRef(l, commit, now, who)
	return nil
}

// UpdateRef moves an existing reference to commit.
func (s *System) UpdateRef(name string, commit CommitID, now int64, who string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" || now < 0 {
		return ErrInvalidParam
	}
	if err := s.clockLocked(now); err != nil {
		return err
	}
	l, ok := s.logs[name]
	if !ok || !l.hasCurrent {
		return ErrRefNotFound
	}
	if !s.store.hasCommit(commit) {
		return ErrCommitNotFound
	}
	s.moveRef(l, commit, now, who)
	return nil
}

// moveRef appends the record for a move to commit and reclassifies
// the whole log against the new head. The caller holds the lock and
// has validated the call.
func (s *System) moveRef(l *refLog, commit CommitID, now int64, who string) {
	ancestors := s.store.ancestorSet(commit)
	old := CommitID("")
	if l.hasCurrent {
		old = l.current
	}
	l.reclassify(ancestors)
	l.appendRecord(old, commit, now, who, ancestors)
	l.hasCurrent = true
	l.current = commit
	s.now = now
}

// DeleteRef deletes a reference. Its records are kept and all fall
// into the unreachable tier until they expire naturally.
func (s *System) DeleteRef(name string, now int64, who string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" || now < 0 {
		return ErrInvalidParam
	}
	if err := s.clockLocked(now); err != nil {
		return err
	}
	l, ok := s.logs[name]
	if !ok || !l.hasCurrent {
		return ErrRefNotFound
	}
	l.appendRecord(l.current, "", now, who, nil)
	l.markAllUnreachable()
	l.hasCurrent = false
	l.current = ""
	s.now = now
	return nil
}

// ReadLog returns the index-th record of name counting from the
// newest (index 1) towards the oldest. A reference that never
// existed yields ErrRefNotFound; a reference whose records have all
// expired (or whose index is out of range) yields ErrRecordNotFound.
func (s *System) ReadLog(name string, index int) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if name == "" || index < 1 {
		return Record{}, ErrInvalidParam
	}
	l, ok := s.logs[name]
	if !ok {
		return Record{}, ErrRefNotFound
	}
	all := l.merged()
	if index > len(all) {
		return Record{}, ErrRecordNotFound
	}
	return *all[len(all)-index], nil
}

// ExpireLogs physically removes expired records from every log and
// returns how many were removed.
func (s *System) ExpireLogs(now int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return 0, ErrInvalidParam
	}
	if err := s.clockLocked(now); err != nil {
		return 0, err
	}
	n := s.expireLocked(now)
	s.now = now
	return n, nil
}

// GC reclaims unreachable objects past their freshness grace.
func (s *System) GC(now int64) (GCStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return GCStats{}, ErrInvalidParam
	}
	if err := s.clockLocked(now); err != nil {
		return GCStats{}, err
	}
	stats := s.gcLocked(now)
	s.now = now
	return stats, nil
}

// ExpireAndGC is exactly ExpireLogs followed by GC as one call.
func (s *System) ExpireAndGC(now int64) (int, GCStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return 0, GCStats{}, ErrInvalidParam
	}
	if err := s.clockLocked(now); err != nil {
		return 0, GCStats{}, err
	}
	expired := s.expireLocked(now)
	stats := s.gcLocked(now)
	s.now = now
	return expired, stats, nil
}

// Head returns the current value of name.
func (s *System) Head(name string) (CommitID, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.logs[name]
	if !ok || !l.hasCurrent {
		return "", false
	}
	return l.current, true
}

// HasCommit reports whether id is present in the store.
func (s *System) HasCommit(id CommitID) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store.hasCommit(id)
}

// HasObject reports whether id is present in the store.
func (s *System) HasObject(id ObjectID) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.store.objects[id]
	return ok
}

// LogLen returns the number of existing records of name.
func (s *System) LogLen(name string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.logs[name]
	if !ok {
		return 0
	}
	return len(l.reachable) + len(l.unreachable)
}
