package ontology

import "sync"

// snapshot is an immutable read view used inside one determination.
// Writers never mutate a published snapshot: they clone the mutable
// fields they touch and atomically swap the pointer. Determinations
// therefore operate on a frozen prefix and serializability reduces to a
// single finalization guard.
type snapshot struct {
	events          []Event
	byObject        map[string][]Event
	objectType      map[string]string
	firstAppearance map[string]OrderKey
	linkTypes       map[string]struct{}
	linkDeclaredAt  map[string]OrderKey
	ruleVersions    map[string]*RuleVersion
	ruleOrder       []*RuleVersion
	headVersion     *RuleVersion
	eventSeqHigh    uint64
}

// Store is the concurrency-safe, event-sourced ontology store.
//
// All mutators (event append, stream import, rule adjustment) take a
// single writer lock, so the committed state is always equivalent to
// some global serial order. Readers work against immutable snapshots.
type Store struct {
	mu sync.RWMutex
	s  *snapshot

	auditMu sync.Mutex
	audits  []AuditRecord
}

// New returns an empty store.
func New() *Store {
	return &Store{
		s: &snapshot{
			byObject:        map[string][]Event{},
			objectType:      map[string]string{},
			firstAppearance: map[string]OrderKey{},
			linkTypes:       map[string]struct{}{},
			linkDeclaredAt:  map[string]OrderKey{},
			ruleVersions:    map[string]*RuleVersion{},
		},
	}
}

// cloneForWrite produces a private copy of the snapshot maps the mutator
// will change. Read-only maps are shared because they stay immutable.
func (s *snapshot) cloneForWrite() *snapshot {
	cp := *s
	cp.byObject = make(map[string][]Event, len(s.byObject))
	for k, v := range s.byObject {
		cp.byObject[k] = v
	}
	cp.objectType = make(map[string]string, len(s.objectType))
	for k, v := range s.objectType {
		cp.objectType[k] = v
	}
	cp.firstAppearance = make(map[string]OrderKey, len(s.firstAppearance))
	for k, v := range s.firstAppearance {
		cp.firstAppearance[k] = v
	}
	cp.linkTypes = make(map[string]struct{}, len(s.linkTypes))
	for k, v := range s.linkTypes {
		cp.linkTypes[k] = v
	}
	cp.linkDeclaredAt = make(map[string]OrderKey, len(s.linkDeclaredAt))
	for k, v := range s.linkDeclaredAt {
		cp.linkDeclaredAt[k] = v
	}
	return &cp
}

func cloneRules(src map[string]*RuleVersion) map[string]*RuleVersion {
	dst := make(map[string]*RuleVersion, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// indexEvent appends e to every per-object slice it can affect.
func indexEvent(by map[string][]Event, e Event) {
	add := func(id string) { by[id] = append(by[id], e) }
	switch e.Kind {
	case EvLinkEstablished, EvLinkRevoked:
		add(e.ObjectID)
		add(e.PeerID)
	default:
		add(e.ObjectID)
	}
}

// Append commits one event, assigning the next global sequence number.
// Appends are intentionally permissive about references: a link may
// precede a create in the raw stream and surface as ErrDanglingReference
// at adjudication time instead of being rejected here.
func (st *Store) Append(in EventInput) Event {
	st.mu.Lock()
	cp := st.s.cloneForWrite()
	e := Event{
		Kind:          in.Kind,
		Time:          in.Time,
		Seq:           cp.eventSeqHigh + 1,
		ObjectID:      in.ObjectID,
		TypeID:        in.TypeID,
		PeerID:        in.PeerID,
		PropertyKey:   in.PropertyKey,
		PropertyValue: in.PropertyValue,
	}
	cp.eventSeqHigh = e.Seq
	cp.events = append(cp.events, e)
	indexEvent(cp.byObject, e)
	switch e.Kind {
	case EvLinkTypeDeclared:
		cp.linkTypes[e.TypeID] = struct{}{}
		if at, ok := cp.linkDeclaredAt[e.TypeID]; !ok || e.Key().Before(at) {
			cp.linkDeclaredAt[e.TypeID] = e.Key()
		}
	case EvObjectCreated:
		if _, ok := cp.objectType[e.ObjectID]; !ok {
			cp.objectType[e.ObjectID] = e.TypeID
		}
		if _, ok := cp.firstAppearance[e.ObjectID]; !ok {
			cp.firstAppearance[e.ObjectID] = e.Key()
		}
	}
	st.s = cp
	st.mu.Unlock()
	return e
}

// ImportStream installs an external, already-sequenced log, replacing
// the current stream. Duplicated order keys are preserved so they can be
// reported as ErrAmbiguousOrder by determinations that observe them.
func (st *Store) ImportStream(events []Event) {
	st.mu.Lock()
	cp := st.s.cloneForWrite()
	cp.events = append(cp.events[:0:0], events...)
	cp.byObject = map[string][]Event{}
	cp.objectType = map[string]string{}
	cp.firstAppearance = map[string]OrderKey{}
	cp.linkTypes = map[string]struct{}{}
	cp.linkDeclaredAt = map[string]OrderKey{}
	cp.eventSeqHigh = 0
	for _, e := range cp.events {
		indexEvent(cp.byObject, e)
		if e.Seq > cp.eventSeqHigh {
			cp.eventSeqHigh = e.Seq
		}
		switch e.Kind {
		case EvLinkTypeDeclared:
			cp.linkTypes[e.TypeID] = struct{}{}
			if at, ok := cp.linkDeclaredAt[e.TypeID]; !ok || e.Key().Before(at) {
				cp.linkDeclaredAt[e.TypeID] = e.Key()
			}
		case EvObjectCreated:
			if _, ok := cp.objectType[e.ObjectID]; !ok {
				cp.objectType[e.ObjectID] = e.TypeID
			}
			if _, ok := cp.firstAppearance[e.ObjectID]; !ok {
				cp.firstAppearance[e.ObjectID] = e.Key()
			}
		}
	}
	st.s = cp
	st.mu.Unlock()
}

// AdjustRule installs a new current rule version. Versions are
// immutable and never backdate: the new version must not order before
// the current head, otherwise an error is returned and nothing changes.
func (st *Store) AdjustRule(rv RuleVersion) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	rvCopy := rv
	requirement := make(map[string][]string, len(rv.Requirement))
	for k, v := range rv.Requirement {
		requirement[k] = append([]string(nil), v...)
	}
	rvCopy.Requirement = requirement
	if st.s.headVersion != nil {
		head := st.s.headVersion
		if rvCopy.EffectiveFrom < head.EffectiveFrom ||
			(rvCopy.EffectiveFrom == head.EffectiveFrom && rvCopy.EffectiveFromSeq <= head.EffectiveFromSeq) {
			return ruleSupersededf("rule version %s does not order after current head %s", rvCopy.ID, head.ID)
		}
	}
	if _, exists := st.s.ruleVersions[rvCopy.ID]; exists {
		return ruleSupersededf("rule version id %s already exists", rvCopy.ID)
	}
	cp := *st.s
	cp.ruleVersions = cloneRules(st.s.ruleVersions)
	cp.ruleOrder = append(append([]*RuleVersion(nil), st.s.ruleOrder...), &rvCopy)
	cp.ruleVersions[rvCopy.ID] = &rvCopy
	cp.headVersion = &rvCopy
	st.s = &cp
	return nil
}

// RuleVersion returns an installed immutable version by ID.
func (st *Store) RuleVersion(id string) (*RuleVersion, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	rv, ok := st.s.ruleVersions[id]
	return rv, ok
}

// HeadRuleVersion returns the current rule version (nil before any
// adjustment).
func (st *Store) HeadRuleVersion() *RuleVersion {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s.headVersion
}

// snapshotRLock returns the current snapshot; caller must RUnlock.
func (st *Store) current() *snapshot {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s
}

// DetermineRequest asks for one object's orphan adjudication at time T.
type DetermineRequest struct {
	ObjectID string
	AtTime   int64
	// Basis: zero value means HEAD at the time of finalization.
	Basis Basis
	// SkipCrossCheck disables the independent naive whole-stream replay
	// comparison for this single call (used by cost benchmarks; normal
	// adjudication always records the cross check).
	SkipCrossCheck bool
}

// DetermineResult is the adjudication outcome.
type DetermineResult struct {
	ObjectID             string
	AtTime               int64
	RuleVersion          RuleVersion
	BasisResolved        Basis
	Status               ObjectStatus
	VirtualOrphanAt      OrderKey
	HasVirtualPoint      bool
	MarkedOrphanAt       OrderKey
	HasMarkedRecord      bool
	ActivityAfterVirtual []SubsequentActivity
	State                *ObjectState
	// CrossCheck is the independent replay verdict recorded for audit.
	CrossCheck *NaiveCrossCheck
	// EventsScanned counts indexed records inspected (bounded by the
	// object's own per-object slice, never by global stream size).
	EventsScanned int
}

// AuditRecord is the post-hoc verification trail of one determination.
type AuditRecord struct {
	ObjectID         string
	AtTime           int64
	BasisResolved    Basis
	RuleVersionID    string
	RuleEffectiveSeq uint64
	RuleRetroactive  bool
	Status           ObjectStatus
	EventsScanned    int
	StreamSeqHigh    uint64
	CrossCheck       *NaiveCrossCheck
	Outcome          string
	ErrorKind        DeterminationErrorKind
}

// AuditLog returns a copy of all recorded audits.
func (st *Store) AuditLog() []AuditRecord {
	st.auditMu.Lock()
	defer st.auditMu.Unlock()
	return append([]AuditRecord(nil), st.audits...)
}

func (st *Store) appendAudit(a AuditRecord) {
	st.auditMu.Lock()
	st.audits = append(st.audits, a)
	st.auditMu.Unlock()
}

// StreamLength returns the number of committed events (verification only).
func (st *Store) StreamLength() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.s.events)
}
