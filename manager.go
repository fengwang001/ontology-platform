package ontology

import (
	"sync"
)

// versionState is the immutable content of one config version: field
// values plus the version that last wrote each field.
type versionState struct {
	values  map[string]any
	sources map[string]int64
}

func (vs *versionState) clone() *versionState {
	values := make(map[string]any, len(vs.values))
	for k, v := range vs.values {
		values[k] = v
	}
	sources := make(map[string]int64, len(vs.sources))
	for k, v := range vs.sources {
		sources[k] = v
	}
	return &versionState{values: values, sources: sources}
}

// LeakReport describes snapshots that have not been released yet.
type LeakReport struct {
	Total     int
	ByVersion map[int64]int
}

// Manager owns config versions, snapshots and reference counts.
// All methods are safe for concurrent use.
type Manager struct {
	mu          sync.Mutex
	decls       map[string]FieldDecl
	versions    map[int64]*versionState
	refs        map[int64]int
	outstanding map[*Snapshot]struct{}
	current     int64
	next        int64
}

// NewManager creates a Manager. initial must provide a valid value for
// every declared field and becomes version 1.
func NewManager(decls []FieldDecl, initial map[string]any) (*Manager, error) {
	m := &Manager{
		decls:       make(map[string]FieldDecl, len(decls)),
		versions:    make(map[int64]*versionState),
		refs:        make(map[int64]int),
		outstanding: make(map[*Snapshot]struct{}),
		next:        1,
	}
	for _, d := range decls {
		m.decls[d.Name] = d
	}
	vs := &versionState{values: make(map[string]any), sources: make(map[string]int64)}
	for name := range initial {
		if _, ok := m.decls[name]; !ok {
			return nil, &ValidationError{Field: name, Kind: KindUnknownField, Value: initial[name]}
		}
	}
	for name, d := range m.decls {
		raw, ok := initial[name]
		if !ok {
			return nil, &ValidationError{Field: name, Kind: KindTypeMismatch, Value: nil}
		}
		v, err := d.validate(raw)
		if err != nil {
			return nil, err
		}
		vs.values[name] = v
		vs.sources[name] = 1
	}
	m.versions[1] = vs
	m.current = 1
	m.next = 2
	return m, nil
}

// CurrentVersion returns the current version number.
func (m *Manager) CurrentVersion() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// Update applies changes atomically and returns the new version number.
// If any field fails validation nothing changes and the version does not
// advance. A field's source version advances only when its value actually
// changes; writing the same value again keeps the old source version.
func (m *Manager) Update(changes map[string]any) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(changes) == 0 {
		return m.current, nil
	}
	normalized := make(map[string]any, len(changes))
	for name, raw := range changes {
		d, ok := m.decls[name]
		if !ok {
			return 0, &ValidationError{Field: name, Kind: KindUnknownField, Value: raw}
		}
		v, err := d.validate(raw)
		if err != nil {
			return 0, err
		}
		normalized[name] = v
	}
	nv := m.next
	m.next++
	vs := m.versions[m.current].clone()
	for name, v := range normalized {
		if vs.values[name] == v {
			continue
		}
		vs.values[name] = v
		vs.sources[name] = nv
	}
	m.versions[nv] = vs
	m.current = nv
	return nv, nil
}

// Acquire returns a snapshot pinned to the current version.
func (m *Manager) Acquire() *Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refs[m.current]++
	s := &Snapshot{m: m, version: m.current}
	m.outstanding[s] = struct{}{}
	return s
}

// release detaches a snapshot; called by Snapshot.Release.
func (m *Manager) release(s *Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.released {
		return
	}
	s.released = true
	m.refs[s.version]--
	delete(m.outstanding, s)
}
