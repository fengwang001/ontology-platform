// Package keytable keeps one bounded LRU table of sampling entries per
// tenant. It is not goroutine-safe; callers must serialize access.
package keytable

import (
	"container/list"
	"errors"
	"sort"

	"sampler/window"
)

// ErrTenantLimit is returned when a record arrives for a new tenant while
// the tenant count already reached Tmax.
var ErrTenantLimit = errors.New("keytable: tenant limit reached")

// Evicted describes an entry removed to make room for a new key.
type Evicted struct {
	Key   string
	Entry window.Entry // snapshot at eviction time
}

// Ref points at a live entry inside the table, for Flush iteration.
type Ref struct {
	Tenant string
	Key    string
	Entry  *window.Entry
}

type item struct {
	key   string
	entry window.Entry
}

type tenant struct {
	items map[string]*list.Element
	lru   *list.List // of *item; front is least recently used
}

// Table maps tenants to bounded LRU key tables.
type Table struct {
	kt       int
	tmax     int
	tenants  map[string]*tenant
	examined uint64 // entries inspected by Upsert; must stay O(1) per call
}

// New builds a table with per-tenant capacity kt and tenant limit tmax.
func New(kt, tmax int) *Table {
	return &Table{kt: kt, tmax: tmax, tenants: make(map[string]*tenant)}
}

// Upsert returns the live entry for (tenant, key) and marks it the most
// recently used key of that tenant. A missing key is inserted as a fresh
// entry for window cur; when the tenant's table is full, its least recently
// used key is evicted first and reported via evicted. existed reports
// whether the key was already present. Fails with ErrTenantLimit for a new
// tenant beyond tmax, leaving all state untouched.
func (t *Table) Upsert(tn, key string, cur uint64) (entry *window.Entry, existed bool, evicted *Evicted, err error) {
	tt, ok := t.tenants[tn]
	if !ok {
		if len(t.tenants) >= t.tmax {
			return nil, false, nil, ErrTenantLimit
		}
		tt = &tenant{items: make(map[string]*list.Element), lru: list.New()}
		t.tenants[tn] = tt
	}
	if el, ok := tt.items[key]; ok {
		t.examined++
		tt.lru.MoveToBack(el)
		it := el.Value.(*item)
		return &it.entry, true, nil, nil
	}
	if tt.lru.Len() >= t.kt {
		t.examined++
		front := tt.lru.Front()
		fit := front.Value.(*item)
		evicted = &Evicted{Key: fit.key, Entry: fit.entry}
		delete(tt.items, fit.key)
		tt.lru.Remove(front)
	}
	it := &item{key: key, entry: window.Entry{Win: cur}}
	tt.items[key] = tt.lru.PushBack(it)
	return &it.entry, false, evicted, nil
}

// Peek returns a copy of the entry for (tenant, key) without changing any
// access order, and whether it exists.
func (t *Table) Peek(tn, key string) (window.Entry, bool) {
	tt, ok := t.tenants[tn]
	if !ok {
		return window.Entry{}, false
	}
	el, ok := tt.items[key]
	if !ok {
		return window.Entry{}, false
	}
	return el.Value.(*item).entry, true
}

// Sorted returns references to all live entries ordered by (tenant, key)
// in byte order. The referenced entries stay live in the table.
func (t *Table) Sorted() []Ref {
	refs := make([]Ref, 0)
	for tn, tt := range t.tenants {
		for key, el := range tt.items {
			refs = append(refs, Ref{Tenant: tn, Key: key, Entry: &el.Value.(*item).entry})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Tenant != refs[j].Tenant {
			return refs[i].Tenant < refs[j].Tenant
		}
		return refs[i].Key < refs[j].Key
	})
	return refs
}
