// Package secindex implements an incrementally maintained single-field
// secondary index over primary-keyed records.
//
// The index entry of every record is the pair (field value, primary key).
// Entries are stored ordered by ascending field value and, within one field
// value, ascending primary key. Writes that replace the field of an existing
// record remove the old entry before inserting the new one; when the new value
// equals the old value nothing happens, so no duplicate entry can appear.
//
// All readers observe a consistent state: individual queries hold a read lock
// for their whole duration, and a write batch becomes visible only after every
// one of its operations has been applied (or not at all when it is rejected).
package secindex

import (
	"fmt"
	"sort"
	"sync"
)

// OpKind enumerates the operations accepted by a batch.
type OpKind int

const (
	// OpUpsert inserts a record or replaces its indexed field.
	OpUpsert OpKind = iota + 1
	// OpDelete removes a record and its index entry.
	OpDelete
)

// String renders an OpKind for logs.
func (k OpKind) String() string {
	switch k {
	case OpUpsert:
		return "UPSERT"
	case OpDelete:
		return "DELETE"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(k))
	}
}

// Op is one element of an atomic write batch. For OpDelete only Key is used.
type Op struct {
	Kind  OpKind
	Key   string
	Value int64
}

// String renders an Op for logs.
func (op Op) String() string {
	if op.Kind == OpDelete {
		return fmt.Sprintf("%s(key=%q)", op.Kind, op.Key)
	}
	return fmt.Sprintf("%s(key=%q, value=%d)", op.Kind, op.Key, op.Value)
}

// Distinguishable rejection reasons. Use errors.Is to classify a BatchError.
var (
	// ErrEmptyKey means an operation carried an empty primary key.
	ErrEmptyKey = indexError{"empty primary key"}
	// ErrKeyNotFound means a delete targeted an absent primary key.
	ErrKeyNotFound = indexError{"key not found"}
	// ErrInvalidOpKind means a batch element has an unknown Kind.
	ErrInvalidOpKind = indexError{"invalid operation kind"}
	// ErrEmptyBatch means Apply was called with no operations.
	ErrEmptyBatch = indexError{"empty batch"}
)

type indexError struct{ msg string }

func (e indexError) Error() string { return "secindex: " + e.msg }

// BatchError reports why a whole batch was rejected. Nothing in the batch is
// applied when a BatchError is returned. errors.Is(err, ErrEmptyKey),
// errors.Is(err, ErrKeyNotFound), errors.Is(err, ErrInvalidOpKind) or
// errors.Is(err, ErrEmptyBatch) classifies the reason.
type BatchError struct {
	// Index is the position of the offending operation inside the batch
	// (-1 when the reason is not tied to one element, e.g. an empty batch).
	Index int
	Op    Op
	Cause error
}

func (e *BatchError) Error() string {
	if e.Index < 0 {
		return "secindex: batch rejected: " + e.Cause.Error()
	}
	return fmt.Sprintf("secindex: batch rejected at ops[%d] %s: %s",
		e.Index, e.Op, e.Cause.Error())
}

// Unwrap exposes the underlying sentinel for errors.Is / errors.As.
func (e *BatchError) Unwrap() error { return e.Cause }

// Maintainer keeps records keyed by primary key and a secondary index on one
// integer field. A Maintainer is safe for concurrent use; the zero value is
// ready to use.
type Maintainer struct {
	mu sync.RWMutex
	// records maps primary key to its current indexed field value.
	records map[string]int64
	// index maps field value to the sorted list of primary keys carrying it.
	index map[int64][]string
}

// New returns an empty maintainer.
func New() *Maintainer {
	return &Maintainer{
		records: make(map[string]int64),
		index:   make(map[int64][]string),
	}
}

// lazyInit must be called with m.mu held and brings a zero-value Maintainer
// into the initialized state.
func (m *Maintainer) lazyInit() {
	if m.records == nil {
		m.records = make(map[string]int64)
		m.index = make(map[int64][]string)
	}
}

// Upsert inserts a record or replaces its indexed field.
//
// When the key already exists its old index entry is removed and the new one
// inserted; equal old and new values are a no-op, so an equal-value rewrite
// never creates a duplicate index entry.
func (m *Maintainer) Upsert(key string, value int64) error {
	if key == "" {
		return &BatchError{Index: 0, Op: Op{Kind: OpUpsert, Key: key, Value: value}, Cause: ErrEmptyKey}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lazyInit()
	m.upsertLocked(key, value)
	return nil
}

// Delete removes a record and its index entry. Deleting an unknown key is
// rejected with ErrKeyNotFound and leaves the maintainer unchanged.
func (m *Maintainer) Delete(key string) error {
	if key == "" {
		return &BatchError{Index: 0, Op: Op{Kind: OpDelete, Key: key}, Cause: ErrEmptyKey}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lazyInit()
	if _, ok := m.records[key]; !ok {
		return &BatchError{Index: 0, Op: Op{Kind: OpDelete, Key: key}, Cause: ErrKeyNotFound}
	}
	m.deleteLocked(key)
	return nil
}

// Apply validates and applies a batch atomically: if any operation is invalid
// (empty key, unknown kind, delete of a missing key) the whole batch is
// rejected and neither the record table nor the index changes. Validation runs
// first against a simulated post-batch table, and only after the whole batch
// passes does mutation begin under a single write lock.
func (m *Maintainer) Apply(ops []Op) error {
	if len(ops) == 0 {
		return &BatchError{Index: -1, Cause: ErrEmptyBatch}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lazyInit()

	// Phase 1: dry-run validation against a simulated record table so that
	// operations earlier in the same batch are visible to later ones.
	sim := make(map[string]int64, len(m.records))
	for k, v := range m.records {
		sim[k] = v
	}
	for i, op := range ops {
		if op.Key == "" {
			return &BatchError{Index: i, Op: op, Cause: ErrEmptyKey}
		}
		switch op.Kind {
		case OpUpsert:
			sim[op.Key] = op.Value
		case OpDelete:
			if _, ok := sim[op.Key]; !ok {
				return &BatchError{Index: i, Op: op, Cause: ErrKeyNotFound}
			}
			delete(sim, op.Key)
		default:
			return &BatchError{Index: i, Op: op, Cause: ErrInvalidOpKind}
		}
	}

	// Phase 2: apply for real. Validation already guaranteed success, so
	// readers either see the pre-batch or post-batch state, never a partial one.
	for _, op := range ops {
		switch op.Kind {
		case OpUpsert:
			m.upsertLocked(op.Key, op.Value)
		case OpDelete:
			m.deleteLocked(op.Key)
		}
	}
	return nil
}

// upsertLocked maintains both tables; m.mu must be held.
func (m *Maintainer) upsertLocked(key string, value int64) {
	old, existed := m.records[key]
	if existed && old == value {
		// Same value: no index movement, no duplicate entry.
		return
	}
	if existed {
		m.removeFromIndexLocked(old, key)
	}
	m.insertIntoIndexLocked(value, key)
	m.records[key] = value
}

// deleteLocked removes one record; m.mu must be held and the key must exist.
func (m *Maintainer) deleteLocked(key string) {
	old := m.records[key]
	m.removeFromIndexLocked(old, key)
	delete(m.records, key)
}

// insertIntoIndexLocked inserts key into the ascending value group.
func (m *Maintainer) insertIntoIndexLocked(value int64, key string) {
	keys := m.index[value]
	pos := sort.SearchStrings(keys, key)
	if pos < len(keys) && keys[pos] == key {
		// Defensive: the uniqueness invariant means this never triggers.
		return
	}
	keys = append(keys, "")
	copy(keys[pos+1:], keys[pos:])
	keys[pos] = key
	m.index[value] = keys
}

// removeFromIndexLocked removes key from a value group and drops the group
// once empty.
func (m *Maintainer) removeFromIndexLocked(value int64, key string) {
	keys := m.index[value]
	pos := sort.SearchStrings(keys, key)
	if pos == len(keys) || keys[pos] != key {
		return
	}
	keys = append(keys[:pos], keys[pos+1:]...)
	if len(keys) == 0 {
		delete(m.index, value)
		return
	}
	m.index[value] = keys
}

// Lookup returns primary keys whose field equals value, in ascending primary
// key order. The returned slice is an independent copy.
func (m *Maintainer) Lookup(value int64) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return copyStrings(m.index[value])
}

// Range returns primary keys with lo <= field < hi (left-closed, right-open),
// in ascending primary key order across the whole interval. An empty or
// inverted interval (lo >= hi) returns an empty list. The returned slice is an
// independent copy.
func (m *Maintainer) Range(lo, hi int64) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return rangeLocked(m, lo, hi)
}

// View is one atomic, point-in-time read of the maintainer. All slices are
// independent copies taken under a single read lock, so a View is internally
// consistent even while writes proceed concurrently.
type View struct {
	Lookup0 []string
	Lookup1 []string
	Range01 []string
	Groups  []IndexGroup
}

// Snapshot returns an atomic read bundling Lookup(0), Lookup(1), Range(0, 1)
// and all index groups. Concurrent callers that receive the same committed
// state get element-wise identical Views. It is provided as the building
// block for reproducible multi-query reads under concurrency.
func (m *Maintainer) Snapshot() View {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v := View{
		Lookup0: copyStrings(m.index[0]),
		Lookup1: copyStrings(m.index[1]),
		Range01: rangeLocked(m, 0, 1),
		Groups:  groupsLocked(m),
	}
	return v
}

// rangeLocked is the lock-free core of Range; caller holds m.mu (read or
// write).
func rangeLocked(m *Maintainer, lo, hi int64) []string {
	if lo >= hi {
		return []string{}
	}
	var out []string
	for value, keys := range m.index {
		if value >= lo && value < hi {
			out = append(out, keys...)
		}
	}
	sort.Strings(out)
	if out == nil {
		return []string{}
	}
	return out
}

// groupsLocked is the lock-free core of IndexGroups; caller holds m.mu.
func groupsLocked(m *Maintainer) []IndexGroup {
	values := make([]int64, 0, len(m.index))
	for v := range m.index {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	groups := make([]IndexGroup, 0, len(values))
	for _, v := range values {
		groups = append(groups, IndexGroup{Value: v, Keys: copyStrings(m.index[v])})
	}
	return groups
}

// Get returns the indexed value of a record and whether it exists.
func (m *Maintainer) Get(key string) (int64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.records[key]
	return v, ok
}

// Len reports the number of stored records.
func (m *Maintainer) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.records)
}

// IndexGroup is one value bucket of the secondary index.
type IndexGroup struct {
	Value int64
	Keys  []string
}

// IndexGroups returns a deep copy of the index: distinct field values in
// ascending order, each paired with its ascending primary-key list.
func (m *Maintainer) IndexGroups() []IndexGroup {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return groupsLocked(m)
}

func copyStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
