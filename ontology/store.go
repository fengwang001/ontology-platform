package ontology

import (
	"sync"
)

// Instance is one stored version of an object instance (or its tombstone).
type Instance struct {
	TypeName string
	Key      string
	Attrs    map[string]any
	Version  int64
	Deleted  bool
}

// WriteRequest carries an optimistic write.
type WriteRequest struct {
	Type     string
	Key      string
	Attrs    map[string]any
	Expected int64 // 0 = create; >0 = expected prior version
}

// WriteResult reports a committed write.
type WriteResult struct {
	Type     string
	Key      string
	Version  int64
	Sequence int64
}

// DeleteRequest carries an optimistic delete.
type DeleteRequest struct {
	Type     string
	Key      string
	Expected int64 // 0 = any live version; >0 = exact live version
}

// DeleteResult reports a committed delete.
type DeleteResult struct {
	Type     string
	Key      string
	Sequence int64
}

// JournalEntry is one accepted commit; replaying the prefix is deterministic.
type JournalEntry struct {
	Sequence int64
	Kind     string
	Type     string
	Key      string
	Attrs    map[string]any
	Version  int64
}

// SourceReader exposes the committed source instances for verification.
type SourceReader interface {
	Snapshot() []Instance
	Journal() []JournalEntry
}

// Store is the composed instance + aggregate subsystem.
type Store struct {
	mu         sync.RWMutex
	types      map[string]ObjectType
	arbiter    *Arbiter
	maintainer *Maintainer
	validator  *Validator
	instances  map[string]map[string]Instance
	seq        int64
	journal    []JournalEntry
}

// NewStore constructs a Store from type and view declarations.
func NewStore(types []ObjectType, views []ViewSpec) *Store {
	tm := make(map[string]ObjectType)
	for _, t := range types {
		tm[t.Name] = t
	}
	return &Store{
		types:      tm,
		arbiter:    NewArbiter(),
		maintainer: NewMaintainer(views),
		validator:  NewValidator(types, views),
		instances:  make(map[string]map[string]Instance),
	}
}

func cloneAttrs(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// recordState reads a record's arbitration state under the caller's lock.
func (s *Store) recordState(typeName, key string) (cur int64, deleted, ever bool) {
	rec, ok := s.instances[typeName][key]
	if !ok {
		return 0, false, false
	}
	return rec.Version, rec.Deleted, true
}

// Write commits one instance version or returns a normalized error.
// Rejections follow the priority: invalid argument -> version conflict.
// A rejected credential does not consume a version number and touches no view.
func (s *Store) Write(req WriteRequest) (WriteResult, error) {
	if err := s.validator.ValidateWrite(req); err != nil {
		return WriteResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, deleted, ever := s.recordState(req.Type, req.Key)
	d := s.arbiter.CheckWrite(cur, deleted, ever, req.Expected)
	if !d.Accept {
		return WriteResult{}, conflict("write", req.Type, req.Key, d.Reason, cur, req.Expected,
			"expected "+versionText(req.Expected)+" current "+versionText(cur)+" state "+stateText(ever, deleted))
	}

	var prev *Instance
	if ever {
		p := s.instances[req.Type][req.Key]
		prev = &p
	}
	newVersion := cur + 1
	inst := Instance{
		TypeName: req.Type,
		Key:      req.Key,
		Attrs:    cloneAttrs(req.Attrs),
		Version:  newVersion,
		Deleted:  false,
	}
	if s.instances[req.Type] == nil {
		s.instances[req.Type] = make(map[string]Instance)
	}
	s.seq++
	s.instances[req.Type][req.Key] = inst
	// All affected views flip to the new contribution inside the same critical
	// section: no query can observe a partial multi-view update.
	s.maintainer.ApplyWrite(inst, prev)
	s.journal = append(s.journal, JournalEntry{
		Sequence: s.seq, Kind: "write", Type: req.Type, Key: req.Key,
		Attrs: cloneAttrs(req.Attrs), Version: newVersion,
	})
	return WriteResult{Type: req.Type, Key: req.Key, Version: newVersion, Sequence: s.seq}, nil
}

// Delete tombstones an instance and withdraws its aggregate contributions.
// Priority: invalid argument -> version conflict (incl. already-deleted) ->
// not-found (primary key never committed).
func (s *Store) Delete(req DeleteRequest) (DeleteResult, error) {
	if err := s.validator.ValidateDelete(req); err != nil {
		return DeleteResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, deleted, ever := s.recordState(req.Type, req.Key)
	d := s.arbiter.CheckDelete(cur, deleted, ever, req.Expected)
	if !ever {
		return DeleteResult{}, notFound("delete", req.Type, req.Key, "instance never committed")
	}
	if !d.Accept {
		return DeleteResult{}, conflict("delete", req.Type, req.Key, d.Reason, cur, req.Expected,
			"expected "+versionText(req.Expected)+" current "+versionText(cur)+" state "+stateText(ever, deleted))
	}

	prev := s.instances[req.Type][req.Key]
	tomb := prev
	tomb.Deleted = true
	s.seq++
	s.instances[req.Type][req.Key] = tomb
	// Contribution withdrawal across all views is indivisible from the delete.
	s.maintainer.ApplyDelete(prev)
	s.journal = append(s.journal, JournalEntry{
		Sequence: s.seq, Kind: "delete", Type: req.Type, Key: req.Key,
		Version: cur,
	})
	return DeleteResult{Type: req.Type, Key: req.Key, Sequence: s.seq}, nil
}

// Get returns the current live instance (deleted/absent => ok=false).
func (s *Store) Get(typeName, key string) (Instance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.instances[typeName][key]
	if !ok || rec.Deleted {
		return Instance{}, false
	}
	rec.Attrs = cloneAttrs(rec.Attrs)
	return rec, true
}

// Query reads an aggregate group.
func (s *Store) Query(view, group string) GroupResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, known := s.maintainer.Query(view, group)
	if !known {
		return GroupResult{View: view, Group: group, Exists: false}
	}
	return r
}

// Verify recomputes all aggregates from the source snapshot and asserts the
// maintained indexes are identical to a fresh full re-derivation.
func (s *Store) Verify() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if oe := s.validator.Verify(lockedReader{s}, s.maintainer); oe != nil {
		return oe
	}
	return nil
}

// Replay builds an empty store from declarations and applies the journal,
// proving the journal is a complete deterministic description of final state.
func Replay(types []ObjectType, views []ViewSpec, j []JournalEntry) *Store {
	s := NewStore(types, views)
	for _, e := range j {
		if e.Kind == "write" {
			_, _ = s.Write(WriteRequest{Type: e.Type, Key: e.Key, Attrs: e.Attrs, Expected: e.Version - 1})
		} else {
			_, _ = s.Delete(DeleteRequest{Type: e.Type, Key: e.Key, Expected: 0})
		}
	}
	return s
}

// lockedReader adapts a locked store to SourceReader without re-locking.
type lockedReader struct{ s *Store }

func (l lockedReader) Snapshot() []Instance { return l.s.rawSnapshot() }
func (l lockedReader) Journal() []JournalEntry {
	out := make([]JournalEntry, len(l.s.journal))
	copy(out, l.s.journal)
	return out
}

// Snapshot returns all live+deleted source records.
func (s *Store) Snapshot() []Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rawSnapshot()
}

func (s *Store) rawSnapshot() []Instance {
	var out []Instance
	for _, bucket := range s.instances {
		for _, rec := range bucket {
			rec.Attrs = cloneAttrs(rec.Attrs)
			out = append(out, rec)
		}
	}
	return out
}

// Journal returns the accepted-commit journal.
func (s *Store) Journal() []JournalEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]JournalEntry, len(s.journal))
	copy(out, s.journal)
	return out
}

// memberCount exposes a group's current membership size (complexity proofs).
func (s *Store) memberCount(view, group string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maintainer.memberCount(view, group)
}

func versionText(v int64) string {
	if v == 0 {
		return "create"
	}
	return "v" + itoa(v)
}

func stateText(ever, deleted bool) string {
	switch {
	case !ever:
		return "never-committed"
	case deleted:
		return "deleted"
	default:
		return "live"
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
