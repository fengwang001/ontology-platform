package ontology

import (
	"sort"
	"sync"
)

// Row is one table record keyed by an integer primary key.
type Row struct {
	ID     int64
	Values map[string]int64
}

// Schema declares the table columns, the primary key column and the
// secondary index used as the access path.
type Schema struct {
	Columns     []string
	PrimaryKey  string
	IndexColumn string
	UniqueIndex bool
}

// Table is a table with an integer primary key and one ordered
// secondary index over an integer column.
type Table struct {
	mu     sync.RWMutex
	schema Schema
	rows   map[int64]map[string]int64
	index  []indexEntry
}

type indexEntry struct {
	key int64
	id  int64
}

func NewTable(schema Schema) *Table {
	return &Table{
		schema: schema,
		rows:   make(map[int64]map[string]int64),
	}
}

// Insert adds a new row. It fails on duplicate primary keys or, when the
// index is unique, duplicate indexed values.
func (t *Table) Insert(id int64, values map[string]int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.rows[id]; ok {
		return ErrDuplicateKey
	}
	row := make(map[string]int64, len(t.schema.Columns))
	for _, col := range t.schema.Columns {
		row[col] = values[col]
	}
	if t.schema.UniqueIndex {
		key := row[t.schema.IndexColumn]
		if t.lookupIndex(key, id) {
			return ErrUniqueViolation
		}
	}
	t.rows[id] = row
	t.index = append(t.index, indexEntry{row[t.schema.IndexColumn], id})
	sort.Slice(t.index, func(i, j int) bool {
		if t.index[i].key != t.index[j].key {
			return t.index[i].key < t.index[j].key
		}
		return t.index[i].id < t.index[j].id
	})
	return nil
}

func (t *Table) lookupIndex(key, excludeID int64) bool {
	i := sort.Search(len(t.index), func(i int) bool {
		return t.index[i].key >= key
	})
	for ; i < len(t.index) && t.index[i].key == key; i++ {
		if t.index[i].id != excludeID {
			return true
		}
	}
	return false
}

func (t *Table) removeIndexEntry(key, id int64) {
	i := sort.Search(len(t.index), func(i int) bool {
		if t.index[i].key != key {
			return t.index[i].key > key
		}
		return t.index[i].id >= id
	})
	if i < len(t.index) && t.index[i].key == key && t.index[i].id == id {
		t.index = append(t.index[:i], t.index[i+1:]...)
	}
}

func (t *Table) addIndexEntry(key, id int64) {
	pos := sort.Search(len(t.index), func(i int) bool {
		if t.index[i].key != key {
			return t.index[i].key > key
		}
		return t.index[i].id >= id
	})
	t.index = append(t.index, indexEntry{})
	copy(t.index[pos+1:], t.index[pos:])
	t.index[pos] = indexEntry{key, id}
}

// rangeIDs returns the primary keys whose indexed value is in [lo, hi),
// ordered by (indexed value, primary key). Caller must hold t.mu.
func (t *Table) rangeIDs(lo, hi int64) []int64 {
	start := sort.Search(len(t.index), func(i int) bool { return t.index[i].key >= lo })
	end := sort.Search(len(t.index), func(i int) bool { return t.index[i].key >= hi })
	ids := make([]int64, 0, end-start)
	for _, e := range t.index[start:end] {
		ids = append(ids, e.id)
	}
	return ids
}

// applyTarget updates one selected row and keeps its single index entry
// consistent with the new indexed value. Caller must hold t.mu.
func (t *Table) applyTarget(id int64, values map[string]int64) {
	oldKey := t.rows[id][t.schema.IndexColumn]
	for col, val := range values {
		t.rows[id][col] = val
	}
	newKey := t.rows[id][t.schema.IndexColumn]
	if newKey != oldKey {
		t.removeIndexEntry(oldKey, id)
		t.addIndexEntry(newKey, id)
	}
}

// Snapshot returns a deep copy of every row, safe for concurrent readers.
func (t *Table) Snapshot() []Row {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Row, 0, len(t.rows))
	for id, values := range t.rows {
		row := Row{ID: id, Values: make(map[string]int64, len(values))}
		for col, val := range values {
			row.Values[col] = val
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// lock acquires the table lock and returns an unlock function.
func (t *Table) lock() func() {
	t.mu.Lock()
	return t.mu.Unlock
}
