// Package sched implements weighted-fair selection over tenants with a
// binary heap ordered by (virtual time, tenant ID).
package sched

import "ontology/tenant"

// Scheduler picks the tenant with the smallest virtual time; ties are
// broken by tenant ID so schedules are reproducible. Only tenants with
// non-empty queues live in the heap. Not safe for concurrent use.
type Scheduler struct {
	items []*tenant.Tenant
	pos   map[*tenant.Tenant]int
	sysVT float64
	cmp   int // comparisons performed by the most recent Pop
}

// New returns an empty scheduler.
func New() *Scheduler { return &Scheduler{pos: make(map[*tenant.Tenant]int)} }

// Len reports how many tenants currently compete for selection.
func (s *Scheduler) Len() int { return len(s.items) }

// SysVT reports the system virtual time: the pre-service virtual time of
// the most recently popped tenant. It never decreases.
func (s *Scheduler) SysVT() float64 { return s.sysVT }

// Comparisons reports the number of heap comparisons the last Pop did.
func (s *Scheduler) Comparisons() int { return s.cmp }

func (s *Scheduler) less(i, j int) bool {
	s.cmp++
	a, b := s.items[i], s.items[j]
	if a.VT != b.VT {
		return a.VT < b.VT
	}
	return a.ID < b.ID
}

func (s *Scheduler) swap(i, j int) {
	s.items[i], s.items[j] = s.items[j], s.items[i]
	s.pos[s.items[i]] = i
	s.pos[s.items[j]] = j
}

func (s *Scheduler) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !s.less(i, p) {
			return
		}
		s.swap(i, p)
		i = p
	}
}

func (s *Scheduler) down(i int) {
	n := len(s.items)
	for {
		m := i
		if l := 2*i + 1; l < n && s.less(l, m) {
			m = l
		}
		if r := 2*i + 2; r < n && s.less(r, m) {
			m = r
		}
		if m == i {
			return
		}
		s.swap(i, m)
		i = m
	}
}

// Add inserts a tenant that has at least one queued task. Adding a tenant
// that is already present is a no-op.
func (s *Scheduler) Add(t *tenant.Tenant) {
	if _, ok := s.pos[t]; ok {
		return
	}
	s.pos[t] = len(s.items)
	s.items = append(s.items, t)
	s.up(len(s.items) - 1)
}

// Pop removes and returns the tenant with the smallest (VT, ID), or nil
// when no tenant competes. It also advances the system virtual time.
func (s *Scheduler) Pop() *tenant.Tenant {
	s.cmp = 0
	n := len(s.items)
	if n == 0 {
		return nil
	}
	top := s.items[0]
	s.swap(0, n-1)
	s.items = s.items[:n-1]
	delete(s.pos, top)
	if len(s.items) > 0 {
		s.down(0)
	}
	if top.VT > s.sysVT {
		s.sysVT = top.VT
	}
	return top
}
