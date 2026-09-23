// Package mutex implements a single shared mutex with an ordered wait queue.
// Effective priorities are supplied by the caller (package inherit); this
// package owns no priority semantics beyond queue ordering.
package mutex

// Waiter is one task blocked on a mutex.
type Waiter struct {
	Task    string
	Eff     int // current effective priority, refreshed via Reorder
	Since   int // step at which waiting started (FIFO tie-break)
	Timeout int // 0 = wait forever; otherwise remaining wait steps
}

// Mutex is a named lock with owner and ordered wait queue.
type Mutex struct {
	Name  string
	owner string
	q     []*Waiter
}

// New creates an unowned mutex.
func New(name string) *Mutex { return &Mutex{Name: name} }

// Owner returns the current holder ("" if free).
func (m *Mutex) Owner() string { return m.owner }

// Free reports whether the mutex has no owner.
func (m *Mutex) Free() bool { return m.owner == "" }

// Len returns the waiter count.
func (m *Mutex) Len() int { return len(m.q) }

// Acquire makes owner the holder; legal only when free.
func (m *Mutex) Acquire(owner string) { m.owner = owner }

// Enqueue appends a waiter; the queue is kept ordered.
func (m *Mutex) Enqueue(w *Waiter) {
	m.q = append(m.q, w)
	m.reorder()
}

// Remove drops a waiter from the queue (timeout / abort).
func (m *Mutex) Remove(task string) {
	for i, w := range m.q {
		if w.Task == task {
			m.q = append(m.q[:i], m.q[i+1:]...)
			return
		}
	}
}

// WaiterFor returns the waiter record for a task, or nil.
func (m *Mutex) WaiterFor(task string) *Waiter {
	for _, w := range m.q {
		if w.Task == task {
			return w
		}
	}
	return nil
}

// Reorder updates a waiter's effective priority and restores order.
func (m *Mutex) Reorder(task string, eff int) {
	if w := m.WaiterFor(task); w != nil {
		w.Eff = eff
		m.reorder()
	}
}

// Head returns the highest-priority waiter without mutating the queue.
func (m *Mutex) Head() *Waiter {
	if len(m.q) == 0 {
		return nil
	}
	return m.q[0]
}

// Handoff pops the head waiter, gives it ownership, and returns it. Returns
// nil if there is no waiter, leaving the mutex unowned.
func (m *Mutex) Handoff() *Waiter {
	if len(m.q) == 0 {
		m.owner = ""
		return nil
	}
	w := m.q[0]
	m.q = m.q[1:]
	m.owner = w.Task
	return w
}

// Release forcibly makes the mutex free (abort of owner with no handoff).
func (m *Mutex) Release() { m.owner = "" }

// Queue exposes waiters in priority order (caller must not mutate).
func (m *Mutex) Queue() []*Waiter { return m.q }

// reorder sorts by Eff desc, then Since asc (stable start order).
func (m *Mutex) reorder() {
	for i := 1; i < len(m.q); i++ {
		for j := i; j > 0 && less(m.q[j], m.q[j-1]); j-- {
			m.q[j], m.q[j-1] = m.q[j-1], m.q[j]
		}
	}
}

func less(a, b *Waiter) bool {
	if a.Eff != b.Eff {
		return a.Eff > b.Eff
	}
	return a.Since < b.Since
}
