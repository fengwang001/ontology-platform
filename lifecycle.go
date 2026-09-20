package ontology

import (
	"fmt"
	"sort"
)

// SwitchTo makes the content of an existing historical version the current
// config. It always produces a brand-new, larger version number; version
// numbers never go backwards and are never reused.
func (m *Manager) SwitchTo(version int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src, ok := m.versions[version]
	if !ok {
		return 0, fmt.Errorf("%w: %d", ErrVersionNotFound, version)
	}
	nv := m.next
	m.next++
	m.versions[nv] = src.clone()
	m.current = nv
	return nv, nil
}

// ContentAt returns a copy of the field values of the given version.
func (m *Manager) ContentAt(version int64) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vs, ok := m.versions[version]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrVersionNotFound, version)
	}
	out := make(map[string]any, len(vs.values))
	for k, v := range vs.values {
		out[k] = v
	}
	return out, nil
}

// GC reclaims every old version with no outstanding snapshots. The current
// version is never reclaimed. It returns the reclaimed version numbers in
// ascending order.
func (m *Manager) GC() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var reclaimed []int64
	for v := range m.versions {
		if v == m.current || m.refs[v] > 0 {
			continue
		}
		delete(m.versions, v)
		delete(m.refs, v)
		reclaimed = append(reclaimed, v)
	}
	sort.Slice(reclaimed, func(i, j int) bool { return reclaimed[i] < reclaimed[j] })
	return reclaimed
}

// Leaks reports snapshots that have not been released, grouped by the
// version they point to.
func (m *Manager) Leaks() LeakReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	rep := LeakReport{ByVersion: make(map[int64]int)}
	for s := range m.outstanding {
		rep.Total++
		rep.ByVersion[s.version]++
	}
	return rep
}
