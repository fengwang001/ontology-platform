package org

import "sync"

const maxEmployees = 100000

type employee struct {
	manager string
	limit   int64
}

type Org struct {
	mu     sync.RWMutex
	people map[string]*employee
}

// New creates an empty organization. Unknown employees are treated as
// having no manager and a zero limit.
func New() *Org {
	return &Org{people: make(map[string]*employee)}
}

func validName(s string) bool { return s != "" }

// SetManager sets m as e's manager; m=="" means e has no manager.
// It rejects e==m and any link that would create a manager cycle.
func (o *Org) SetManager(e, m string) error {
	if !validName(e) || m != "" && !validName(m) {
		return ErrInvalid
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if m != "" {
		if m == e {
			return ErrInvalid
		}
		// Cycle iff e is already reachable by walking upward from m.
		cur := m
		for cur != "" {
			if cur == e {
				return ErrCycle
			}
			p, ok := o.people[cur]
			if !ok || p.manager == "" {
				break
			}
			cur = p.manager
		}
	}

	p := o.people[e]
	if p == nil {
		if len(o.people) >= maxEmployees {
			return ErrInvalid
		}
		p = &employee{}
		o.people[e] = p
	}
	if m != "" {
		if _, ok := o.people[m]; !ok {
			if len(o.people) >= maxEmployees {
				return ErrInvalid
			}
			o.people[m] = &employee{}
		}
	}
	p.manager = m
	return nil
}

// SetLimit sets e's approval limit (0..1e12); default limit is 0.
func (o *Org) SetLimit(e string, x int64) error {
	if !validName(e) || x < 0 || x > 1_000_000_000_000 {
		return ErrInvalid
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	p := o.people[e]
	if p == nil {
		if len(o.people) >= maxEmployees {
			return ErrInvalid
		}
		p = &employee{}
		o.people[e] = p
	}
	p.limit = x
	return nil
}

// Manager returns e's manager and whether one is set.
func (o *Org) Manager(e string) (string, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	p := o.people[e]
	if p == nil || p.manager == "" {
		return "", false
	}
	return p.manager, true
}

// Limit returns e's current approval limit.
func (o *Org) Limit(e string) int64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	p := o.people[e]
	if p == nil {
		return 0
	}
	return p.limit
}

// Count returns the number of known employees (test support).
func (o *Org) Count() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return len(o.people)
}
