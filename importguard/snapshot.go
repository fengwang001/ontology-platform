package importguard

import "fmt"

// permKey identifies one (subject, type, action, field) authorization cell.
type permKey struct {
	subject  string
	typeName string
	action   string
	field    string
}

// Snapshot is an immutable read view of the store captured at the moment a
// batch is initiated. It contains:

//   - deep copies of object types, subjects and objects (so concurrent
//     mutations cannot change a batch's inputs), and
//   - a permission INDEX that folds the entire append-only permission history
//     down to the effective decision per (subject,type,action,field): the
//     entry with the greatest RevSeq wins.
//
// As a consequence a per-record field decision is exactly ONE map lookup: it
// does not grow with batch size nor with the total (historical) number of
// permission entries of the type. The fold happens once per batch, at O(H)
// where H is the history length, and is independent of record count.
type Snapshot struct {
	types    map[string]*ObjectType
	subjects map[string]bool
	objects  map[string]*Object
	perm     map[permKey]bool

	// historyCount is H: total permission entries folded into perm. Exposed
	// for the scale-independent performance proof in tests.
	historyCount int

	// probes counts individual permission-cell lookups; the gatekeeper passes
	// a per-record counter to assert the scale-independent bound without a
	// shared mutable field (records are judged concurrently).
}

// takeSnapshotLocked deep-copies the current store state and builds the
// effective-permission index. The caller must hold store.mu.
func takeSnapshotLocked(s *Store) *Snapshot {
	snap := &Snapshot{
		types:        make(map[string]*ObjectType, len(s.types)),
		subjects:     make(map[string]bool, len(s.subjects)),
		objects:      make(map[string]*Object, len(s.objects)),
		perm:         make(map[permKey]bool, len(s.permissions)),
		historyCount: len(s.permissions),
	}
	for id, t := range s.types {
		req := make(map[string]bool, len(t.Required))
		for k, v := range t.Required {
			req[k] = v
		}
		snap.types[id] = &ObjectType{Name: t.Name, Required: req}
	}
	for id := range s.subjects {
		snap.subjects[id] = true
	}
	for id, o := range s.objects {
		props := make(map[string]PropertyValue, len(o.Properties))
		for k, v := range o.Properties {
			props[k] = v
		}
		snap.objects[id] = &Object{ID: o.ID, Type: o.Type, Properties: props, Version: o.Version}
	}
	// Fold history in revision order; later (higher RevSeq) entries win.
	for _, e := range s.permissions {
		snap.perm[permKey{e.Subject, e.TypeName, e.Action, e.Field}] = e.Allow
	}
	return snap
}

// subjectExists reports whether the subject existed at snapshot time.
func (s *Snapshot) subjectExists(id string) bool { return s.subjects[id] }

// objectType returns the type and existence at snapshot time.
func (s *Snapshot) objectType(name string) (*ObjectType, bool) {
	t, ok := s.types[name]
	return t, ok
}

// object returns a shallow-copied object as seen at snapshot time.
func (s *Snapshot) object(id string) (Object, bool) {
	o, ok := s.objects[id]
	if !ok {
		return Object{}, false
	}
	props := make(map[string]PropertyValue, len(o.Properties))
	for k, v := range o.Properties {
		props[k] = v
	}
	return Object{ID: o.ID, Type: o.Type, Properties: props, Version: o.Version}, true
}

// canWrite answers one field-write authorization question with a single map
// lookup. Missing cells default to deny. Each call increments probe so tests
// can verify the scale-independent lookup bound.
func (s *Snapshot) canWrite(subject, typeName, field string, probes *int) (allowed bool, basis string) {
	*probes++
	key := permKey{subject, typeName, ActionWrite, field}
	allow, found := s.perm[key]
	if !found {
		return false, fmt.Sprintf("no permission entry for %s/%s/%s/%s (default deny)",
			subject, typeName, ActionWrite, field)
	}
	return allow, fmt.Sprintf("effective permission cell %s/%s/%s/%s=%v (latest revision wins)",
		subject, typeName, ActionWrite, field, allow)
}

// ActionWrite is the action constant guarding property writes.
const ActionWrite = "write"
