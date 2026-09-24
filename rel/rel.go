// Package rel implements a single multiset table R(K, V) with an index on K.
package rel

// Row is one signed change: Sign=+1 inserts one occurrence, Sign=-1 removes one.
type Row struct {
	K    int64
	V    string
	Sign int
}

// Entry is a distinct value V under some key K with its multiplicity.
type Entry struct {
	V    string
	Mult int
}

// Tuple is one distinct (K, V) pair with its multiplicity.
type Tuple struct {
	K    int64
	V    string
	Mult int
}

// Table is a multiset of (K, V) rows: idx[k][v] = multiplicity.
// Zero multiplicity pairs are never stored. The type is not goroutine safe;
// callers (package djoin) serialize access.
type Table struct {
	idx map[int64]map[string]int
}

// New returns an empty table.
func New() *Table {
	return &Table{idx: make(map[int64]map[string]int)}
}

// Get returns the multiplicity of (k, v); 0 means absent.
func (t *Table) Get(k int64, v string) int {
	return t.idx[k][v]
}

// Add applies a signed multiplicity change. A result of 0 deletes the row.
// Callers must guarantee the resulting multiplicity is non-negative.
func (t *Table) Add(k int64, v string, delta int) {
	m := t.idx[k]
	if m == nil {
		m = make(map[string]int)
		t.idx[k] = m
	}
	next := m[v] + delta
	if next == 0 {
		delete(m, v)
		if len(m) == 0 {
			delete(t.idx, k)
		}
		return
	}
	m[v] = next
}

// Lookup returns all distinct values with multiplicity under key k.
// It uses the K index; it never scans rows stored under other keys.
func (t *Table) Lookup(k int64) []Entry {
	m := t.idx[k]
	if len(m) == 0 {
		return nil
	}
	out := make([]Entry, 0, len(m))
	for v, mult := range m {
		out = append(out, Entry{V: v, Mult: mult})
	}
	return out
}

// All returns every distinct (K, V) tuple with its multiplicity.
func (t *Table) All() []Tuple {
	out := make([]Tuple, 0, t.Len())
	for k, m := range t.idx {
		for v, mult := range m {
			out = append(out, Tuple{K: k, V: v, Mult: mult})
		}
	}
	return out
}

// Len returns the number of distinct (K, V) rows currently stored.
func (t *Table) Len() int {
	n := 0
	for _, m := range t.idx {
		n += len(m)
	}
	return n
}
