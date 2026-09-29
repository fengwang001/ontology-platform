package evolution

import "sync"

// ChangeOp is a single schema-evolution operation.
type ChangeOp struct {
	Op     string
	Column ColumnDef
}

// Registry stores versioned schemas and evolves them atomically.
// All reads take RLock and return immutable *Snapshot values; evolution takes
// Lock and validates the complete new version before publishing it, so a
// failed Evolve never mutates the registry and concurrent Project calls
// always observe one complete version.
type Registry struct {
	mu       sync.RWMutex
	versions map[int]*Snapshot
	order    []int
}

func NewRegistry() *Registry { return &Registry{} }

// RegisterInitial registers the first complete version.
func (r *Registry) RegisterInitial(version int, cols []ColumnDef) (*Snapshot, error) {
	if err := validateColumnSet(cols); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.versions == nil {
		r.versions = make(map[int]*Snapshot)
	}
	if _, exists := r.versions[version]; exists {
		return nil, reject(ReasonVersionExists, "version %d is already registered", version)
	}
	snap := newSnapshot(version, cols)
	r.versions[version] = snap
	r.order = append(r.order, version)
	return snap, nil
}

// Evolve applies a batch of changes to the newest version and publishes the
// result as `version`. Every op is validated against a private working copy;
// the registry changes only after the whole batch succeeds.
func (r *Registry) Evolve(version int, ops []ChangeOp) (*Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.versions == nil {
		r.versions = make(map[int]*Snapshot)
	}
	if _, exists := r.versions[version]; exists {
		return nil, reject(ReasonVersionExists, "version %d is already registered", version)
	}
	if len(r.order) == 0 {
		return nil, reject(ReasonNoCurrentVersion, "cannot evolve: no initial schema registered")
	}
	base := r.versions[r.order[len(r.order)-1]]

	// Private working copy: failure leaves the registry untouched.
	working := cloneColumns(base.schema.Columns)
	index := base.idx
	namePos := make(map[string]int, len(index))
	for k, v := range index {
		namePos[k] = v
	}

	for _, op := range ops {
		pos, known := namePos[op.Column.Name]
		switch op.Op {
		case OpAdd:
			if known {
				return nil, reject(ReasonColumnExists,
					"add: column %q already exists", op.Column.Name)
			}
			if op.Column.Name == "" {
				return nil, reject(ReasonEmptyColumnName, "add: column name must not be empty")
			}
			if !op.Column.Type.valid() {
				return nil, reject(ReasonInvalidType,
					"add: column %q has unknown type %q", op.Column.Name, op.Column.Type)
			}
			namePos[op.Column.Name] = len(working)
			working = append(working, op.Column)
		case OpUpdate:
			if !known {
				return nil, reject(ReasonUnknownColumn,
					"update: column %q does not exist", op.Column.Name)
			}
			if !op.Column.Type.valid() {
				return nil, reject(ReasonInvalidType,
					"update: column %q has unknown type %q", op.Column.Name, op.Column.Type)
			}
			working[pos] = op.Column
		case OpDelete:
			if !known {
				return nil, reject(ReasonUnknownColumn,
					"delete: column %q does not exist", op.Column.Name)
			}
			working = append(working[:pos], working[pos+1:]...)
			delete(namePos, op.Column.Name)
			for name, p := range namePos {
				if p > pos {
					namePos[name] = p - 1
				}
			}
		default:
			return nil, reject(ReasonInvalidChangeOp,
				"unknown change op %q for column %q", op.Op, op.Column.Name)
		}
	}

	if err := validateColumnSet(working); err != nil {
		return nil, err
	}
	snap := newSnapshot(version, working)
	r.versions[version] = snap
	r.order = append(r.order, version)
	return snap, nil
}

// Snapshot returns the immutable snapshot of any registered version.
func (r *Registry) Snapshot(version int) (*Snapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snap, ok := r.versions[version]
	if !ok {
		return nil, reject(ReasonVersionNotRegistered, "version %d is not registered", version)
	}
	return snap, nil
}

// Latest returns the newest snapshot or nil before registration.
func (r *Registry) Latest() *Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.order) == 0 {
		return nil
	}
	return r.versions[r.order[len(r.order)-1]]
}

// ProjectEvent projects an old-version event onto the current (newest)
// schema. The event's own version must be registered. The target snapshot is
// pinned atomically at call time under a single read lock, so even while the
// registry keeps evolving every projection is based on one complete version
// and can never mix columns from two schemas. The lock is released before
// projection work begins; the immutable snapshot makes that safe.
func (r *Registry) ProjectEvent(ev Event) (*ProjectedRow, *ProjectionReport, *Snapshot, error) {
	r.mu.RLock()
	if _, ok := r.versions[ev.Version]; !ok {
		r.mu.RUnlock()
		return nil, nil, nil, reject(ReasonVersionNotRegistered,
			"event version %d is not registered", ev.Version)
	}
	var snap *Snapshot
	if len(r.order) == 0 {
		r.mu.RUnlock()
		return nil, nil, nil, reject(ReasonNoCurrentVersion, "no schema registered")
	}
	snap = r.versions[r.order[len(r.order)-1]]
	r.mu.RUnlock()
	row, report, err := snap.Project(ev)
	return row, report, snap, err
}
