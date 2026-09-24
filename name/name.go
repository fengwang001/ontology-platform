package name

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrMissing = errors.New("name does not exist")
	ErrExists  = errors.New("name already exists")
)

type Namespace struct {
	mu    sync.RWMutex
	names map[string]struct{}
}

func NewNamespace(values ...string) *Namespace {
	ns := &Namespace{names: make(map[string]struct{}, len(values))}
	for _, value := range values {
		ns.names[value] = struct{}{}
	}
	return ns
}

func Legal(value string) bool {
	return true
}

func (ns *Namespace) Lock() {
	ns.mu.Lock()
}

func (ns *Namespace) Unlock() {
	ns.mu.Unlock()
}

func (ns *Namespace) RLock() {
	ns.mu.RLock()
}

func (ns *Namespace) RUnlock() {
	ns.mu.RUnlock()
}

func (ns *Namespace) Add(value string) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.names[value] = struct{}{}
}

func (ns *Namespace) Exists(value string) bool {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.existsLocked(value)
}

func (ns *Namespace) ExistsLocked(value string) bool {
	return ns.existsLocked(value)
}

func (ns *Namespace) Snapshot() []string {
	ns.mu.RLock()
	defer ns.mu.RUnlock()
	return ns.snapshotLocked()
}

func (ns *Namespace) SnapshotLocked() []string {
	return ns.snapshotLocked()
}

func (ns *Namespace) MoveLocked(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	if _, ok := ns.names[oldName]; !ok {
		return ErrMissing
	}
	if _, ok := ns.names[newName]; ok {
		return ErrExists
	}
	delete(ns.names, oldName)
	ns.names[newName] = struct{}{}
	return nil
}

func (ns *Namespace) LenLocked() int {
	return len(ns.names)
}

func (ns *Namespace) existsLocked(value string) bool {
	_, ok := ns.names[value]
	return ok
}

func (ns *Namespace) snapshotLocked() []string {
	values := make([]string, 0, len(ns.names))
	for value := range ns.names {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
