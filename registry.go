package ontology

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// BreakingChangeKind classifies a backwards-incompatible schema change.
type BreakingChangeKind int

const (
	// PropertyRemoved is an existing property being deleted.
	PropertyRemoved BreakingChangeKind = iota
	// PropertyBecameRequired is an optional property being tightened to required.
	PropertyBecameRequired
	// PropertyTypeChanged is an existing property changing its DataType.
	PropertyTypeChanged
	// PrimaryKeyChanged is the primary key of an ObjectType being moved
	// to a different property.
	PrimaryKeyChanged
)

func (k BreakingChangeKind) String() string {
	switch k {
	case PropertyRemoved:
		return "property removed"
	case PropertyBecameRequired:
		return "property became required"
	case PropertyTypeChanged:
		return "property type changed"
	case PrimaryKeyChanged:
		return "primary key changed"
	default:
		return fmt.Sprintf("unknown breaking change kind %d", int(k))
	}
}

// BreakingChange pinpoints a single backwards-incompatible change: which
// ObjectType, which property, and which kind of breakage it is.
type BreakingChange struct {
	ObjectType string
	Property   string
	Kind       BreakingChangeKind
}

func (c BreakingChange) String() string {
	return fmt.Sprintf("%s: object type %q, property %q", c.Kind, c.ObjectType, c.Property)
}

// BreakingChangesError is returned by Evolve when the requested evolution
// contains backwards-incompatible changes and no migration function was
// provided for every affected ObjectType. It carries the full list of
// breaking changes so callers can inspect each one.
type BreakingChangesError struct {
	Changes []BreakingChange
}

func (e *BreakingChangesError) Error() string {
	parts := make([]string, len(e.Changes))
	for i, c := range e.Changes {
		parts[i] = c.String()
	}
	return "ontology: evolution rejected, breaking changes require migration: " + strings.Join(parts, "; ")
}

// MigrationError is returned by Evolve when a migration function fails
// (returns an error or panics) for some instance. The whole evolution is
// rolled back when this happens.
type MigrationError struct {
	ObjectType string
	// Index is the position of the failing instance in the instance list.
	Index int
	// Panic reports whether the failure was a panic rather than an error.
	Panic bool
	Err   error
}

func (e *MigrationError) Error() string {
	cause := "error"
	if e.Panic {
		cause = "panic"
	}
	return fmt.Sprintf("ontology: migration of object type %q failed at instance %d (%s): %v; evolution rolled back",
		e.ObjectType, e.Index, cause, e.Err)
}

func (e *MigrationError) Unwrap() error { return e.Err }

// Instance is a stored object instance: a map from property name to value.
type Instance map[string]any

// MigrationFunc transforms one existing instance of an ObjectType so that
// it conforms to the evolved schema. It is applied to every stored
// instance of the ObjectType; any failure aborts and rolls back the whole
// evolution.
type MigrationFunc func(Instance) (Instance, error)

// Evolution describes one schema evolution step. ObjectTypes holds the new
// full definitions of the ObjectTypes being added or changed; ObjectTypes
// not mentioned keep their current definitions. Migrations maps an
// ObjectType name to the migration function that authorizes and performs
// its breaking changes.
type Evolution struct {
	ObjectTypes []ObjectType
	Migrations  map[string]MigrationFunc
}

// Registry stores the current schema, every historical schema version, and
// the object instances. It is safe for concurrent use.
type Registry struct {
	mu        sync.Mutex
	version   int
	schemas   map[int]*Schema
	instances map[string][]Instance
}

// NewRegistry creates an empty registry at schema version 1.
func NewRegistry() *Registry {
	return &Registry{
		version:   1,
		schemas:   map[int]*Schema{1: {Version: 1, ObjectTypes: map[string]ObjectType{}}},
		instances: map[string][]Instance{},
	}
}

// Version returns the current schema version.
func (r *Registry) Version() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.version
}

// Get returns a deep copy of the schema at the given version. Historical
// versions are never affected by later evolutions.
func (r *Registry) Get(version int) (*Schema, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.schemas[version]
	if !ok {
		return nil, fmt.Errorf("ontology: schema version %d not found", version)
	}
	return s.clone(), nil
}

// Current returns a deep copy of the current schema.
func (r *Registry) Current() *Schema {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.schemas[r.version].clone()
}

// AddInstance stores an instance of an existing ObjectType.
func (r *Registry) AddInstance(objectType string, data Instance) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.schemas[r.version].ObjectTypes[objectType]; !ok {
		return fmt.Errorf("ontology: object type %q not found in current schema", objectType)
	}
	r.instances[objectType] = append(r.instances[objectType], cloneInstance(data))
	return nil
}

// Instances returns deep copies of all stored instances of an ObjectType.
func (r *Registry) Instances(objectType string) []Instance {
	r.mu.Lock()
	defer r.mu.Unlock()
	src := r.instances[objectType]
	out := make([]Instance, len(src))
	for i, inst := range src {
		out[i] = cloneInstance(inst)
	}
	return out
}

// Evolve applies one evolution step. On success it returns the new schema
// version. The evolution is atomic: if any part of it is invalid,
// incompatible without a migration, or a migration fails, the registry is
// left exactly as before and the version does not advance.
func (r *Registry) Evolve(ev Evolution) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Validate all incoming ObjectType definitions first.
	seen := make(map[string]bool, len(ev.ObjectTypes))
	for _, ot := range ev.ObjectTypes {
		if err := ot.validate(); err != nil {
			return 0, err
		}
		if seen[ot.Name] {
			return 0, &InvalidObjectTypeError{ObjectType: ot.Name, Reason: "duplicate object type in one evolution"}
		}
		seen[ot.Name] = true
	}

	current := r.schemas[r.version]
	next := current.clone()

	var breaking []BreakingChange
	needsMigration := map[string]bool{}

	for _, ot := range ev.ObjectTypes {
		old, exists := current.ObjectTypes[ot.Name]
		if exists {
			changes := diffObjectTypes(old, ot)
			if len(changes) > 0 {
				breaking = append(breaking, changes...)
				needsMigration[ot.Name] = true
			}
		}
		next.ObjectTypes[ot.Name] = ot.clone()
	}

	// Breaking changes are only allowed when the caller provided a
	// migration function for every affected ObjectType.
	if len(breaking) > 0 {
		allCovered := true
		for name := range needsMigration {
			if ev.Migrations[name] == nil {
				allCovered = false
				break
			}
		}
		if !allCovered {
			return 0, &BreakingChangesError{Changes: breaking}
		}
	}

	// Run migrations against copies; commit only if every instance of
	// every affected ObjectType migrates successfully.
	affected := make([]string, 0, len(needsMigration))
	for name := range needsMigration {
		affected = append(affected, name)
	}
	sort.Strings(affected)
	migrated := make(map[string][]Instance, len(affected))
	for _, name := range affected {
		mig := ev.Migrations[name]
		src := r.instances[name]
		out := make([]Instance, len(src))
		for i, inst := range src {
			res, err := runMigration(mig, cloneInstance(inst))
			if err != nil {
				var me *MigrationError
				if errors.As(err, &me) {
					// Panic inside the migration function.
					me.ObjectType = name
					me.Index = i
					return 0, me
				}
				return 0, &MigrationError{ObjectType: name, Index: i, Err: err}
			}
			out[i] = res
		}
		migrated[name] = out
	}

	// Commit.
	r.version++
	next.Version = r.version
	r.schemas[r.version] = next
	for name, insts := range migrated {
		r.instances[name] = insts
	}
	return r.version, nil
}

// diffObjectTypes returns the breaking changes needed to go from old to
// next. Compatible changes (added nullable property, required relaxed to
// nullable) produce no entries.
func diffObjectTypes(old, next ObjectType) []BreakingChange {
	var changes []BreakingChange
	for _, op := range old.Properties {
		np, ok := next.property(op.Name)
		if !ok {
			changes = append(changes, BreakingChange{ObjectType: next.Name, Property: op.Name, Kind: PropertyRemoved})
			continue
		}
		if np.Type != op.Type {
			changes = append(changes, BreakingChange{ObjectType: next.Name, Property: op.Name, Kind: PropertyTypeChanged})
		}
		if !op.Required && np.Required {
			changes = append(changes, BreakingChange{ObjectType: next.Name, Property: op.Name, Kind: PropertyBecameRequired})
		}
	}
	if old.primaryKey() != next.primaryKey() {
		changes = append(changes, BreakingChange{ObjectType: next.Name, Property: old.primaryKey(), Kind: PrimaryKeyChanged})
	}
	return changes
}

// runMigration invokes fn, converting a panic into a *MigrationError.
func runMigration(fn MigrationFunc, inst Instance) (result Instance, err error) {
	defer func() {
		if p := recover(); p != nil {
			result = nil
			err = &MigrationError{Panic: true, Err: fmt.Errorf("%v", p)}
		}
	}()
	result, err = fn(inst)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func cloneInstance(inst Instance) Instance {
	out := make(Instance, len(inst))
	for k, v := range inst {
		out[k] = v
	}
	return out
}
