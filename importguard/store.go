package importguard

import "sync"

// PermissionEntry is one authorization rule: subject may (or may not, when
// Allow is false) perform Action on Field of object type TypeName. RevSeq is
// the monotonically increasing revision sequence; entries accumulate and are
// never rewritten, which is why the permission history of a type can grow
// without bound. Lookups must therefore use the latest entry per
// (subject,type,field) rather than scanning the history.
type PermissionEntry struct {
	RevSeq   int64
	Subject  string
	TypeName string
	Field    string
	Action   string
	Allow    bool
}

// Store is the mutable backing state: object types, subjects, objects and the
// append-only permission history. All access is serialized by mu; the
// gatekeeper holds mu across a whole batch so that concurrent batches are
// globally serializable (each batch is one critical section).
type Store struct {
	mu sync.Mutex

	types       map[string]*ObjectType
	subjects    map[string]bool
	objects     map[string]*Object
	permissions []PermissionEntry
	nextRev     int64
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{
		types:    map[string]*ObjectType{},
		subjects: map[string]bool{},
		objects:  map[string]*Object{},
		nextRev:  1,
	}
}

// AddType registers an object type.
func (s *Store) AddType(t ObjectType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req := make(map[string]bool, len(t.Required))
	for k, v := range t.Required {
		req[k] = v
	}
	s.types[t.Name] = &ObjectType{Name: t.Name, Required: req}
}

// AddSubject registers a principal that may initiate imports.
func (s *Store) AddSubject(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subjects[id] = true
}

// PutObject inserts or replaces an object (used for test setup).
func (s *Store) PutObject(o Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	props := make(map[string]PropertyValue, len(o.Properties))
	for k, v := range o.Properties {
		props[k] = v
	}
	s.objects[o.ID] = &Object{ID: o.ID, Type: o.Type, Properties: props, Version: o.Version}
}

// Grant appends an allowing permission entry; later entries override earlier
// ones for the same (subject,type,field,action).
func (s *Store) Grant(subject, typeName, field, action string) {
	s.appendPermission(subject, typeName, field, action, true)
}

// Deny appends an explicit denying permission entry.
func (s *Store) Deny(subject, typeName, field, action string) {
	s.appendPermission(subject, typeName, field, action, false)
}

func (s *Store) appendPermission(subject, typeName, field, action string, allow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.permissions = append(s.permissions, PermissionEntry{
		RevSeq:   s.nextRev,
		Subject:  subject,
		TypeName: typeName,
		Field:    field,
		Action:   action,
		Allow:    allow,
	})
	s.nextRev++
}

// GetObject returns a copy of the object and true, or zero/false if absent.
func (s *Store) GetObject(id string) (Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getObjectLocked(id)
}

func (s *Store) getObjectLocked(id string) (Object, bool) {
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

// insertObjectLocked / patchObjectLocked mutate store state while mu is held.
// They are used by the gatekeeper inside its serialized commit phase.
func (s *Store) insertObjectLocked(id, typeName string, fields map[string]PropertyValue) {
	props := make(map[string]PropertyValue, len(fields))
	for k, v := range fields {
		props[k] = v
	}
	s.objects[id] = &Object{ID: id, Type: typeName, Properties: props, Version: 1}
}

func (s *Store) patchObjectLocked(id string, fields map[string]PropertyValue) {
	o := s.objects[id]
	for k, v := range fields {
		o.Properties[k] = v
	}
	o.Version++
}
