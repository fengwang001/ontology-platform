// Package ceil tracks registered tasks and resources, records which
// tasks declared use of each resource, and derives every resource's
// priority ceiling: the highest base priority among its users.
// It has no dependencies. Not safe for concurrent use; callers synchronize.
package ceil

import "errors"

// Sentinel errors for unknown registrations.
var (
	ErrUnknownTask     = errors.New("ceil: unknown task")
	ErrUnknownResource = errors.New("ceil: unknown resource")
)

// Table records tasks, resources and resource users, and computes ceilings.
type Table struct {
	prio map[string]int // task -> base priority
	ceil map[string]int // resource -> ceiling (max user priority)
}

// New returns an empty Table.
func New() *Table {
	return &Table{prio: map[string]int{}, ceil: map[string]int{}}
}

// AddTask registers a task with its base priority (first write wins).
func (t *Table) AddTask(task string, priority int) {
	if _, ok := t.prio[task]; !ok {
		t.prio[task] = priority
	}
}

// AddResource registers a resource with no users yet (ceiling 0).
func (t *Table) AddResource(res string) {
	if _, ok := t.ceil[res]; !ok {
		t.ceil[res] = 0
	}
}

// Priority returns the task's base priority.
func (t *Table) Priority(task string) (int, bool) {
	p, ok := t.prio[task]
	return p, ok
}

// Has reports whether res is a registered resource.
func (t *Table) Has(res string) bool {
	_, ok := t.ceil[res]
	return ok
}

// Use declares that task uses res. Users are only ever added,
// so the ceiling is a running max over user priorities.
func (t *Table) Use(task, res string) error {
	p, ok := t.prio[task]
	if !ok {
		return ErrUnknownTask
	}
	c, ok := t.ceil[res]
	if !ok {
		return ErrUnknownResource
	}
	if p > c {
		t.ceil[res] = p
	}
	return nil
}

// Ceiling returns the ceiling of res: the highest priority among
// its declared users, or 0 if it has none.
func (t *Table) Ceiling(res string) int { return t.ceil[res] }
