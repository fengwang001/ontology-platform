package temporal

import "sync"

// StagingError describes an invalid staged write (for example creating a
// link that violates the cardinality rule in force at commit time).
type StagingError struct{ msg string }

func (e *StagingError) Error() string { return "temporal staging error: " + e.msg }

// objectRecord is one versioned object state.
type objectRecord struct {
	typeID TypeID
	props  PropertyValues
}

// edgeKey identifies a directed link instance.
type edgeKey struct {
	linkType LinkTypeID
	src, dst ObjectID
}

// edgeRecord is the lifecycle bit of one link instance; presence of an alive
// entry on its timeline means the link exists.
type edgeRecord struct{}

// adjRoot pairs a committed instant with the adjacency treap root visible
// from that instant onward (until the next entry in the bucket).
type adjRoot struct {
	at   Instant
	root *treapNode
}

// Store is the append-only temporal ontology store. All mutating operations
// are staged on a transaction and become visible atomically at one new
// instant when Commit is called. Reads never block writers: each timeline is
// a copy-on-write slice read through an atomic pointer, and each adjacency
// bucket is a persistent treap whose historical roots remain readable.
type Store struct {
	commitMu sync.Mutex // serializes commits only

	head    atomicInstant
	horizon atomicInstant

	objects     *versioned[ObjectID, objectRecord]
	objectTypes *versioned[TypeID, []Property]
	linkTypes   *versioned[LinkTypeID, Cardinality]
	edges       *versioned[edgeKey, edgeRecord]

	// adjOut/adjIn hold, per object, the sequence of committed treap roots.
	adjOut *adjacency
	adjIn  *adjacency

	// commitLog is the canonical global serialization: one ordered entry per
	// commit. The naive oracle ingests the same entries and shares no code.
	logMu sync.Mutex
	log   []CommitEntry

	gapsMu sync.RWMutex
	gaps   []Gap

	nextTraversalID atomicUint64
}

// NewStore creates an empty store whose replay horizon starts at instant 0.
func NewStore() *Store {
	return &Store{
		objects:     newVersioned[ObjectID, objectRecord](),
		objectTypes: newVersioned[TypeID, []Property](),
		linkTypes:   newVersioned[LinkTypeID, Cardinality](),
		edges:       newVersioned[edgeKey, edgeRecord](),
		adjOut:      newAdjacency(),
		adjIn:       newAdjacency(),
	}
}

// CommitEntry is one committed change set in the global serialization order.
// It is the single interface between the production store and the naive
// oracle; the oracle shares no index or lookup code with the store.
type CommitEntry struct {
	At Instant

	CreateObjectTypes map[TypeID][]Property
	MigrateTypes      map[TypeID][]Property
	CreateLinkTypes   map[LinkTypeID]Cardinality
	AdjustCards       map[LinkTypeID]Cardinality

	CreateObjects map[ObjectID]objectRecord
	SetProps      map[ObjectID]PropertyValues
	DeleteObjects []ObjectID

	CreateLinks []edgeKey
	RevokeLinks []edgeKey
}

// Tx stages writes for one atomic commit.
type Tx struct {
	store *Store

	createObjectTypes map[TypeID][]Property
	migrateTypes      map[TypeID][]Property
	createLinkTypes   map[LinkTypeID]Cardinality
	adjustCards       map[LinkTypeID]Cardinality

	createObjects map[ObjectID]objectRecord
	setProps      map[ObjectID]PropertyValues
	deleteObjects map[ObjectID]struct{}

	createLinks map[edgeKey]struct{}
	revokeLinks map[edgeKey]struct{}
}

// Begin opens a write transaction.
func (s *Store) Begin() *Tx {
	return &Tx{
		store:             s,
		createObjectTypes: map[TypeID][]Property{},
		migrateTypes:      map[TypeID][]Property{},
		createLinkTypes:   map[LinkTypeID]Cardinality{},
		adjustCards:       map[LinkTypeID]Cardinality{},
		createObjects:     map[ObjectID]objectRecord{},
		setProps:          map[ObjectID]PropertyValues{},
		deleteObjects:     map[ObjectID]struct{}{},
		createLinks:       map[edgeKey]struct{}{},
		revokeLinks:       map[edgeKey]struct{}{},
	}
}

// Rollback discards staged changes.
func (tx *Tx) Rollback() {}

// CreateObjectType stages the first property-definition version of a type.
func (tx *Tx) CreateObjectType(id TypeID, props []Property) {
	tx.createObjectTypes[id] = append([]Property(nil), props...)
}

// MigrateObjectType stages a new property-definition version. The new version
// becomes visible at commit instant; snapshots at older instants keep the
// old definition.
func (tx *Tx) MigrateObjectType(id TypeID, props []Property) {
	tx.migrateTypes[id] = append([]Property(nil), props...)
}

// CreateLinkType stages the first cardinality version of a link type.
func (tx *Tx) CreateLinkType(id LinkTypeID, card Cardinality) {
	tx.createLinkTypes[id] = card
}

// AdjustCardinality stages a new cardinality version. Existing links that
// violate the new rule are never removed; traversal at an older instant uses
// the rule version covering that instant.
func (tx *Tx) AdjustCardinality(id LinkTypeID, card Cardinality) {
	tx.adjustCards[id] = card
}

// CreateObject stages object creation.
func (tx *Tx) CreateObject(id ObjectID, typ TypeID, props PropertyValues) {
	tx.createObjects[id] = objectRecord{typeID: typ, props: cloneProps(props)}
}

// SetProperties stages a full property replacement at commit time.
func (tx *Tx) SetProperties(id ObjectID, props PropertyValues) {
	tx.setProps[id] = cloneProps(props)
}

// DeleteObject stages object deletion; incident links are cascade-revoked.
func (tx *Tx) DeleteObject(id ObjectID) { tx.deleteObjects[id] = struct{}{} }

// CreateLink stages one link creation. Cardinality is enforced at commit.
func (tx *Tx) CreateLink(linkType LinkTypeID, src, dst ObjectID) error {
	if linkType == "" || src == "" || dst == "" {
		return &StagingError{msg: "link type, src and dst are required"}
	}
	tx.createLinks[edgeKey{linkType: linkType, src: src, dst: dst}] = struct{}{}
	return nil
}

// RevokeLink stages one link revocation.
func (tx *Tx) RevokeLink(linkType LinkTypeID, src, dst ObjectID) {
	tx.revokeLinks[edgeKey{linkType: linkType, src: src, dst: dst}] = struct{}{}
}

func cloneProps(p PropertyValues) PropertyValues {
	out := make(PropertyValues, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// SetRetainHorizon declares that instants older than h can no longer be
// replayed. Snapshot requests at t < h fail with ErrBeforeHorizon. The
// horizon only moves forward.
func (s *Store) SetRetainHorizon(h Instant) {
	for {
		cur := s.horizon.get()
		if h <= cur {
			return
		}
		if s.horizon.v.CompareAndSwap(int64(cur), int64(h)) {
			return
		}
	}
}

// AddHistoryGap records an unrecoverable slab of history. Lookups needing
// the slab fail with ErrMissingHistory instead of guessing.
func (s *Store) AddHistoryGap(g Gap) {
	s.gapsMu.Lock()
	s.gaps = append(s.gaps, g)
	s.gapsMu.Unlock()
}

// Head returns the latest committed instant (0 if nothing committed).
func (s *Store) Head() Instant { return s.head.get() }

// Horizon returns the oldest replayable instant.
func (s *Store) Horizon() Instant { return s.horizon.get() }

// snapshotGaps returns a copy of the current gap list.
func (s *Store) snapshotGaps() []Gap {
	s.gapsMu.RLock()
	out := append([]Gap(nil), s.gaps...)
	s.gapsMu.RUnlock()
	return out
}

// logLocked appends a commit entry to the canonical serialization log.
func (s *Store) appendLogLocked(e CommitEntry) {
	s.logMu.Lock()
	s.log = append(s.log, e)
	s.logMu.Unlock()
}

// LogEntries returns a copy of the full commit log (used by the oracle and
// by audit/verification tooling).
func (s *Store) LogEntries() []CommitEntry {
	s.logMu.Lock()
	out := append([]CommitEntry(nil), s.log...)
	s.logMu.Unlock()
	return out
}

// Gap describes an unrecoverable interval of historical records. A lookup
// whose answer depends on a record covered by a gap fails with
// ErrMissingHistory rather than returning a guessed state.
type Gap struct {
	From Instant
	To   Instant
	Kind GapKind
	Key  string // object id / type id / link type id depending on Kind
}

// GapKind selects which timeline family a gap belongs to.
type GapKind int

const (
	// GapAny matches every lookup in [From, To].
	GapAny GapKind = iota
	// GapObject matches object timelines whose ObjectID equals Key.
	GapObject
	// GapObjectType matches object-type timelines whose TypeID equals Key.
	GapObjectType
	// GapLinkType matches link-type timelines whose LinkTypeID equals Key.
	GapLinkType
	// GapEdge matches edge timelines; Key is informational.
	GapEdge
	// GapAdjacency matches adjacency root lookups for object Key.
	GapAdjacency
)

// covers reports whether a gap affects the lookup at instant t for the given
// kind and key.
func (g Gap) covers(t Instant, kind GapKind, key string) bool {
	if t < g.From || t > g.To {
		return false
	}
	if g.Kind != GapAny && g.Kind != kind {
		return false
	}
	if g.Key != "" && g.Key != key {
		return false
	}
	return true
}
