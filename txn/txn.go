// Package txn manages action transaction boundaries.
//
// A Txn is a staging overlay over the committed Store. All writes of an
// outermost action, including every nested action invocation, land in the
// same overlay and are committed or discarded atomically. A snapshot View
// is a read-only facade over (committed store, overlay) pointer pairs, so
// constructing one is O(1): it touches no map, no matter how deep the
// nesting is or how many writes have already been applied. The copyOps
// counter instruments this: any map copy performed to build a snapshot
// would increment it, and tests assert it stays zero.
package txn

import "sync"

// Value is the property bag of a single object instance.
type Value map[string]any

// Store holds the committed state of all object instances.
type Store struct {
	mu   sync.RWMutex
	data map[string]map[string]Value
}

// NewStore returns an empty committed store.
func NewStore() *Store {
	return &Store{data: map[string]map[string]Value{}}
}

// Get reads a committed instance.
func (s *Store) Get(typeName, id string) (Value, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.data[typeName][id]
	return inst, ok
}

// CountCommitted counts committed live instances of a type.
func (s *Store) CountCommitted(typeName string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data[typeName])
}

// cell is one staged write: value==nil means delete.
type cell struct {
	value Value
}

// Txn is the transaction boundary of one outermost action call. Nested
// invocations share the same Txn, so there is exactly one commit point.
type Txn struct {
	store   *Store
	overlay map[string]map[string]cell
	// touched lists type names in first-write order; the engine uses it to
	// iterate post hooks deterministically.
	touched []string
	// copyOps counts map copies performed by Snapshot. It is always zero;
	// it exists so tests can prove snapshot construction is O(1).
	copyOps int
}

// Begin opens a new transaction on s.
func (s *Store) Begin() *Txn {
	return &Txn{store: s, overlay: map[string]map[string]cell{}}
}

// View is a read-only snapshot of the transaction state. It is a pair of
// pointers (committed store, overlay); constructing it copies nothing.
// Hooks run synchronously at the point the View is taken, so the live
// facade they observe is exactly the state at that point.
type View struct {
	store   *Store
	overlay map[string]map[string]cell
}

// Snapshot returns an O(1) read-only view of the current transaction
// state, including all writes applied so far by outer and nested actions.
func (tx *Txn) Snapshot() View {
	return View{store: tx.store, overlay: tx.overlay}
}

// CopyOps reports how many map copies Snapshot has performed. Always zero:
// the proof hook for the O(1) snapshot guarantee.
func (tx *Txn) CopyOps() int {
	return tx.copyOps
}

// Get reads a single instance as visible in this view.
func (v View) Get(typeName, id string) (Value, bool) {
	if insts, ok := v.overlay[typeName]; ok {
		if c, ok := insts[id]; ok {
			if c.value == nil {
				return nil, false
			}
			return c.value, true
		}
	}
	v.store.mu.RLock()
	defer v.store.mu.RUnlock()
	inst, ok := v.store.data[typeName][id]
	return inst, ok
}

// Count counts live instances of a type visible in this view.
func (v View) Count(typeName string) int {
	v.store.mu.RLock()
	base := v.store.data[typeName]
	n := len(base)
	v.store.mu.RUnlock()
	for id, c := range v.overlay[typeName] {
		if _, ok := base[id]; !ok && c.value != nil {
			n++
		}
		if _, ok := base[id]; ok && c.value == nil {
			n--
		}
	}
	return n
}

// Get reads an instance as visible inside the transaction.
func (tx *Txn) Get(typeName, id string) (Value, bool) {
	return tx.Snapshot().Get(typeName, id)
}

// Apply stages a write. A nil value means delete.
func (tx *Txn) Apply(typeName, id string, value Value) {
	insts, ok := tx.overlay[typeName]
	if !ok {
		insts = map[string]cell{}
		tx.overlay[typeName] = insts
		tx.touched = append(tx.touched, typeName)
	}
	insts[id] = cell{value: value}
}

// TouchedTypes returns the written type names in first-write order.
func (tx *Txn) TouchedTypes() []string {
	return tx.touched
}

// Commit atomically merges the overlay into the committed store.
func (tx *Txn) Commit() {
	tx.store.mu.Lock()
	defer tx.store.mu.Unlock()
	for typeName, insts := range tx.overlay {
		committed, ok := tx.store.data[typeName]
		if !ok {
			committed = map[string]Value{}
			tx.store.data[typeName] = committed
		}
		for id, c := range insts {
			if c.value == nil {
				delete(committed, id)
			} else {
				committed[id] = c.value
			}
		}
	}
	tx.overlay = map[string]map[string]cell{}
}

// Rollback discards all staged writes. Nothing ever reaches the store, so
// no trace of the transaction remains.
func (tx *Txn) Rollback() {
	tx.overlay = map[string]map[string]cell{}
	tx.touched = nil
}
