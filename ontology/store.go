package ontology

import (
	"sort"
	"sync"
)

// edgeKey is the storage key of one directed edge.
type edgeKey struct {
	link   LinkTypeName
	source InstanceID
	target InstanceID
}

// Store is the ontology object store. Its zero value is not usable; create
// stores with NewStore.
type Store struct {
	registry *Registry

	// locks holds one mutex per instance ID ever seen; entries are never
	// deleted. All multi-lock acquisitions sort IDs first, which makes the
	// lock graph acyclic.
	locksMu sync.Mutex
	locks   map[InstanceID]*sync.Mutex

	// tabMu guards instances and edges. Every mutation, and every read that
	// needs a consistent snapshot of several keys, is performed while holding
	// the per-instance locks AND tabMu in that order.
	tabMu sync.RWMutex
	// instances holds the sole live image of every object instance.
	instances map[InstanceID]*Instance
	// edges is the global edge set.
	edges map[edgeKey]struct{}
	// outAdj and inAdj are per-instance adjacency indexes. They let
	// cardinality evaluation touch only the batch's own endpoints instead of
	// scanning the global edge set, which keeps the decision cost independent
	// of the total number of instances.
	outAdj map[InstanceID]map[LinkTypeName]map[InstanceID]struct{}
	inAdj  map[InstanceID]map[LinkTypeName]map[InstanceID]struct{}

	journalMu sync.Mutex
	journal   []*JournalRecord

	commitClock int64
	attemptSeq  int64
}

// NewStore creates an empty store backed by the given registry.
func NewStore(registry *Registry) *Store {
	return &Store{
		registry:  registry,
		locks:     map[InstanceID]*sync.Mutex{},
		instances: map[InstanceID]*Instance{},
		edges:     map[edgeKey]struct{}{},
		outAdj:    map[InstanceID]map[LinkTypeName]map[InstanceID]struct{}{},
		inAdj:     map[InstanceID]map[LinkTypeName]map[InstanceID]struct{}{},
	}
}

// lockFor returns the mutex governing one instance ID, creating it on first
// use.
func (s *Store) lockFor(id InstanceID) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	mu, ok := s.locks[id]
	if !ok {
		mu = &sync.Mutex{}
		s.locks[id] = mu
	}
	return mu
}

// lockAll acquires the mutex of every unique ID in ids, in sorted order, and
// returns the ordered list together with the unlock function.
func (s *Store) lockAll(ids []InstanceID) ([]InstanceID, func()) {
	unique := uniqueSortedIDs(ids)
	for _, id := range unique {
		s.lockFor(id).Lock()
	}
	return unique, func() {
		for i := len(unique) - 1; i >= 0; i-- {
			s.lockFor(unique[i]).Unlock()
		}
	}
}

func uniqueSortedIDs(ids []InstanceID) []InstanceID {
	seen := map[InstanceID]struct{}{}
	out := make([]InstanceID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// GetOne returns a deep copy of one instance, or nil if it does not exist.
// The read is linearized at a single point while the instance lock and the
// table read lock are held, so it can never observe a partially published
// batch.
func (s *Store) GetOne(id InstanceID) *Instance {
	return s.Snapshot([]InstanceID{id})[id]
}

// Snapshot returns deep copies of the given instances taken at one common
// linearization point: callers can never observe a mix of pre-batch and
// post-batch instances while a concurrent batch is publishing.
func (s *Store) Snapshot(ids []InstanceID) map[InstanceID]*Instance {
	ordered, unlock := s.lockAll(ids)
	defer unlock()
	s.tabMu.RLock()
	defer s.tabMu.RUnlock()
	out := make(map[InstanceID]*Instance, len(ordered))
	for _, id := range ordered {
		if inst, ok := s.instances[id]; ok {
			out[id] = inst.clone()
		}
	}
	return out
}

// HasEdge reports whether an edge exists at the read linearization point.
func (s *Store) HasEdge(edge Edge) bool {
	srcLock := s.lockFor(edge.Source)
	dstLock := s.lockFor(edge.Target)
	if edge.Source < edge.Target {
		srcLock.Lock()
		defer srcLock.Unlock()
		dstLock.Lock()
		defer dstLock.Unlock()
	} else {
		dstLock.Lock()
		defer dstLock.Unlock()
		if edge.Source != edge.Target {
			srcLock.Lock()
			defer srcLock.Unlock()
		}
	}
	s.tabMu.RLock()
	defer s.tabMu.RUnlock()
	_, ok := s.edges[edgeKey{edge.Link, edge.Source, edge.Target}]
	return ok
}

// CommitSeq returns the current value of the store-wide commit clock. It
// advances by exactly one for each committed batch and never changes for a
// rejected batch.
func (s *Store) CommitSeq() int64 {
	s.tabMu.RLock()
	defer s.tabMu.RUnlock()
	return s.commitClock
}

// Journal returns deep-copied journal records in attempt order. The journal
// is the replay evidence: it contains inputs, every check observation and
// the verdict of every batch attempt.
func (s *Store) Journal() []*JournalRecord {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	out := make([]*JournalRecord, len(s.journal))
	for i, rec := range s.journal {
		out[i] = cloneRecord(rec)
	}
	return out
}

func cloneRecord(rec *JournalRecord) *JournalRecord {
	cp := *rec
	cp.Input.Items = append([]BatchItem(nil), rec.Input.Items...)
	for i := range cp.Input.Items {
		cp.Input.Items[i].Properties = cloneProperties(rec.Input.Items[i].Properties)
		cp.Input.Items[i].LinkDeltas = append([]EdgeDelta(nil), rec.Input.Items[i].LinkDeltas...)
	}
	if rec.Failure != nil {
		f := *rec.Failure
		cp.Failure = &f
	}
	cp.LockOrder = append([]InstanceID(nil), rec.LockOrder...)
	cp.Baselines = append([]BaselineObservation(nil), rec.Baselines...)
	cp.Hooks = append([]HookObservation(nil), rec.Hooks...)
	cp.CardinalityChecks = append([]CardinalityObservation(nil), rec.CardinalityChecks...)
	cp.InstanceTouches = map[InstanceID]int{}
	for k, v := range rec.InstanceTouches {
		cp.InstanceTouches[k] = v
	}
	cp.FinalVersions = map[InstanceID]Version{}
	for k, v := range rec.FinalVersions {
		cp.FinalVersions[k] = v
	}
	return &cp
}
