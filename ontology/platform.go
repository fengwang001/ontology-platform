package ontology

import "sync"

// Platform is an in-memory ontology store supporting atomic batch updates.
//
// Concurrency control is strict two-phase locking over the *instances named
// by the batch* (locked in a global deterministic order), plus the set of
// instances adjacent to them through touched link types. Decision work is
// therefore proportional to batch size, independent of the total number of
// instances in the store.
type Platform struct {
	mu sync.Mutex

	objectTypes map[TypeName]*ObjectType
	linkTypes   map[TypeName]*LinkType

	// instances, links and adj are the committed state.
	instances map[InstanceID]*instance
	links     map[linkKey]struct{}
	// adj lists, for each instance, the link keys incident to it. It lets
	// cardinality validation touch only batch-related instances instead of
	// scanning the whole link set.
	adj map[InstanceID]map[linkKey]struct{}

	// lockReg holds one mutex per known instance id, plus a sentinel mutex
	// used for not-yet-created ids. Registration of new ids happens under mu.
	lockReg map[InstanceID]*sync.Mutex

	tick    int64
	journal *Journal

	// Internal decision-cost counters: evidence that commit work scales with
	// the batch, never with the total number of stored instances.
	stats Stats
}

// Stats reports internal counters useful for verifying cost bounds.
type Stats struct {
	Submitted         int64
	Committed         int64
	DuplicateRejected int64
	ConflictRejected  int64
	HookRejected      int64
	CardinalityReject int64
	HooksRun          int64
	// InstanceInspections counts individual committed-state records consulted
	// during commit decisions. It is O(batch size), never O(store size).
	InstanceInspections int64
}

// NewPlatform creates an empty platform.
func NewPlatform() *Platform {
	return &Platform{
		objectTypes: map[TypeName]*ObjectType{},
		linkTypes:   map[TypeName]*LinkType{},
		instances:   map[InstanceID]*instance{},
		links:       map[linkKey]struct{}{},
		adj:         map[InstanceID]map[linkKey]struct{}{},
		lockReg:     map[InstanceID]*sync.Mutex{},
	}
}

// SetJournal attaches the append-only decision journal.
func (p *Platform) SetJournal(j *Journal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.journal = j
}

// Stats returns a snapshot of internal counters.
func (p *Platform) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// Tick returns the current global logical clock value. It advances exactly
// once per committed batch and never changes on rollback.
func (p *Platform) Tick() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tick
}

// RegisterObjectType registers an object type with its validation hook.
func (p *Platform) RegisterObjectType(t ObjectType) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ot := t
	p.objectTypes[ot.Name] = &ot
}

// RegisterLinkType registers a link type with per-end cardinality.
func (p *Platform) RegisterLinkType(t LinkType) {
	p.mu.Lock()
	defer p.mu.Unlock()
	lt := t
	p.linkTypes[lt.Name] = &lt
}

// Snapshot is an immutable read view of one instance.
type Snapshot struct {
	ID      InstanceID
	Type    TypeName
	Version int64
	Exists  bool
	Props   Properties
	Tick    int64
}

// Read returns the current committed state of an instance.
func (p *Platform) Read(id InstanceID) Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.readLocked(id)
	s.Tick = p.tick
	return s
}

func (p *Platform) readLocked(id InstanceID) Snapshot {
	if ins, ok := p.instances[id]; ok {
		return Snapshot{ID: ins.id, Type: ins.typ, Version: ins.version, Exists: true, Props: cloneProps(ins.props)}
	}
	return Snapshot{ID: id, Version: 0, Exists: false}
}

func cloneProps(in Properties) Properties {
	if in == nil {
		return nil
	}
	out := make(Properties, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
