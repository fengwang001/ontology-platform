package mutex

import "errors"

var ErrQueueLimit = errors.New("mutex waiter limit exceeded")

type Waiter struct {
	ID       string
	Priority int
	Since    int
}

type lock struct {
	owner string
	queue []Waiter
}

type Manager struct {
	locks     map[string]*lock
	held      map[string][]string
	maxWaiter int
}

func NewManager(maxWaiter int) *Manager {
	return &Manager{locks: map[string]*lock{}, held: map[string][]string{}, maxWaiter: maxWaiter}
}

func (m *Manager) Acquire(id, name string, priority, step int) (waited bool, err error) {
	l := m.ensure(name)
	if l.owner == "" {
		l.owner = id
		m.held[id] = append(m.held[id], name)
		return false, nil
	}
	if l.owner == id {
		return false, nil
	}
	if m.maxWaiter > 0 && len(l.queue) >= m.maxWaiter {
		return false, ErrQueueLimit
	}
	w := Waiter{id, priority, step}
	l.queue = append(l.queue, w)
	m.sort(l)
	return true, nil
}

func (m *Manager) Holder(name string) string {
	if l := m.locks[name]; l != nil {
		return l.owner
	}
	return ""
}

func (m *Manager) Queue(name string) []Waiter {
	if l := m.locks[name]; l != nil {
		return append([]Waiter(nil), l.queue...)
	}
	return nil
}

func (m *Manager) RemoveWaiter(id, name string) bool {
	l := m.locks[name]
	if l == nil {
		return false
	}
	for i, w := range l.queue {
		if w.ID == id {
			l.queue = append(l.queue[:i], l.queue[i+1:]...)
			return true
		}
	}
	return false
}

func (m *Manager) SetPriority(id, name string, priority int) {
	if l := m.locks[name]; l != nil {
		for i := range l.queue {
			if l.queue[i].ID == id {
				l.queue[i].Priority = priority
			}
		}
		m.sort(l)
	}
}

func (m *Manager) Release(id, name string) (string, bool) {
	l := m.locks[name]
	if l == nil || l.owner != id {
		return "", false
	}
	return m.finish(id, l, name)
}

func (m *Manager) Held(id string) []string {
	return append([]string(nil), m.held[id]...)
}

func (m *Manager) ensure(name string) *lock {
	if m.locks[name] == nil {
		m.locks[name] = &lock{}
	}
	return m.locks[name]
}

func (m *Manager) finish(id string, l *lock, name string) (string, bool) {
	m.removeHeld(id, name)
	if len(l.queue) == 0 {
		l.owner = ""
		return "", true
	}
	next := l.queue[0]
	l.queue = l.queue[1:]
	l.owner = next.ID
	m.held[next.ID] = append(m.held[next.ID], name)
	return next.ID, true
}

func (m *Manager) removeHeld(id, name string) {
	got := m.held[id]
	for i, n := range got {
		if n == name {
			m.held[id] = append(got[:i], got[i+1:]...)
			return
		}
	}
}

func (m *Manager) sort(l *lock) {
	for i := 1; i < len(l.queue); i++ {
		for j := i; j > 0 && m.less(l.queue[j], l.queue[j-1]); j-- {
			l.queue[j], l.queue[j-1] = l.queue[j-1], l.queue[j]
		}
	}
}

func (m *Manager) less(a, b Waiter) bool {
	return a.Priority > b.Priority || a.Priority == b.Priority && a.Since < b.Since
}
