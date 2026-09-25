// Package lock implements the priority ceiling protocol (PCP). Depends only on ceil.
package lock

import (
	"errors"
	"sync"

	"ontology/ceil"
)

// Sentinel errors; each rejected operation maps to exactly one.
var (
	ErrUnknownTask     = ceil.ErrUnknownTask
	ErrUnknownResource = ceil.ErrUnknownResource
	ErrAlreadyHeld     = errors.New("lock: resource already held")
	ErrNotHeld         = errors.New("lock: resource not held by task")
)

// Manager holds all PCP state. Safe for concurrent use.
type Manager struct {
	mu        sync.Mutex
	ct        *ceil.Table                // tasks, resources, ceilings
	holder    map[string]string          // resource -> holding task
	heldBy    map[string]map[string]bool // task -> held resource set
	ceilCount map[int]int                // multiset: ceiling -> held count
	maxCeil   int                        // max ceiling over held resources
	checked   int                        // held resources examined by last Acquire
}

func New() *Manager {
	return &Manager{ct: ceil.New(), holder: map[string]string{}, heldBy: map[string]map[string]bool{}, ceilCount: map[int]int{}}
}

// AddTask registers a task with its base priority (first write wins).
func (m *Manager) AddTask(task string, priority int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ct.AddTask(task, priority)
	if m.heldBy[task] == nil {
		m.heldBy[task] = map[string]bool{}
	}
}

func (m *Manager) AddResource(res string) { m.mu.Lock(); defer m.mu.Unlock(); m.ct.AddResource(res) }

func (m *Manager) Use(task, res string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ct.Use(task, res)
}

// check validates task and res; caller holds mu. Rejection precedes any write.
func (m *Manager) check(task, res string) (int, error) {
	p, ok := m.ct.Priority(task)
	if !ok {
		return 0, ErrUnknownTask
	}
	if !m.ct.Has(res) {
		return 0, ErrUnknownResource
	}
	return p, nil
}
func (m *Manager) inc(c int) { m.ceilCount[c]++; m.maxCeil = max(m.maxCeil, c) }
func (m *Manager) dec(c int) {
	if m.ceilCount[c]--; m.ceilCount[c] == 0 {
		delete(m.ceilCount, c)
	}
	if c != m.maxCeil || m.ceilCount[c] > 0 {
		return
	}
	m.maxCeil = 0
	for k := range m.ceilCount {
		m.maxCeil = max(m.maxCeil, k)
	}
}

// sysCeilingLocked returns the max ceiling over resources held by tasks
// other than task. Only the caller's own holdings are examined (temporarily
// removed, then restored), so checked never grows with m.
func (m *Manager) sysCeilingLocked(task string) int {
	own := m.heldBy[task]
	m.checked = len(own)
	if len(own) == 0 {
		return m.maxCeil
	}
	for res := range own {
		m.dec(m.ct.Ceiling(res))
	}
	top := m.maxCeil
	for res := range own {
		m.inc(m.ct.Ceiling(res))
	}
	return top
}

// Acquire grants res to task iff task.priority > system ceiling; block/error leave state unchanged.
// Re-acquiring one's own resource is ErrAlreadyHeld; a resource held by
// another task blocks (under declared Use the ceiling test already implies this).
func (m *Manager) Acquire(task, res string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.check(task, res)
	if err != nil {
		return false, err
	}
	h, held := m.holder[res]
	if held && h == task {
		return false, ErrAlreadyHeld
	}
	if held || p <= m.sysCeilingLocked(task) {
		return false, nil
	}
	m.holder[res] = task
	m.heldBy[task][res] = true
	m.inc(m.ct.Ceiling(res))
	return true, nil
}

// Release drops task's hold on res and recomputes the system ceiling.
func (m *Manager) Release(task, res string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.check(task, res); err != nil {
		return err
	}
	if m.holder[res] != task {
		return ErrNotHeld
	}
	delete(m.holder, res)
	delete(m.heldBy[task], res)
	m.dec(m.ct.Ceiling(res))
	return nil
}

// SystemCeiling is the max ceiling over all currently held resources.
func (m *Manager) SystemCeiling() int { m.mu.Lock(); defer m.mu.Unlock(); return m.maxCeil }

// EffectivePriority is max(base priority, ceilings of held resources).
func (m *Manager) EffectivePriority(task string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.ct.Priority(task)
	if !ok {
		return 0, ErrUnknownTask
	}
	for res := range m.heldBy[task] {
		p = max(p, m.ct.Ceiling(res))
	}
	return p, nil
}
