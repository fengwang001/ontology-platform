// Package bucket holds signature-to-vector-ID bucket tables.
package bucket

// ID is a vector identifier assigned by the index.
type ID uint32

// Signature is the b-bit bucket key (bits beyond b are zero).
type Signature uint64

// Entry is one persisted bucket: a signature and its member IDs.
type Entry struct {
	Sig Signature
	IDs []ID
}

// Table maps bucket signatures to member vector IDs.
type Table struct {
	m map[Signature][]ID
}

// NewTable returns an empty bucket table.
func NewTable() *Table { return &Table{m: make(map[Signature][]ID)} }

// Add inserts id into the bucket keyed by sig (no intra-bucket duplicates).
func (t *Table) Add(sig Signature, id ID) {
	for _, ex := range t.m[sig] {
		if ex == id {
			return
		}
	}
	t.m[sig] = append(t.m[sig], id)
}

// Get returns the IDs in bucket sig.
func (t *Table) Get(sig Signature) []ID { return t.m[sig] }

// Len returns the number of non-empty buckets.
func (t *Table) Len() int { return len(t.m) }

// Entries returns all non-empty buckets in a stable (sorted-signature) order.
func (t *Table) Entries() []Entry {
	out := make([]Entry, 0, len(t.m))
	for sig, ids := range t.m {
		cp := make([]ID, len(ids))
		copy(cp, ids)
		out = append(out, Entry{Sig: sig, IDs: cp})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Sig > out[j].Sig; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// FromEntries rebuilds a table from persisted entries.
func FromEntries(entries []Entry) *Table {
	t := NewTable()
	for _, e := range entries {
		t.m[e.Sig] = append(t.m[e.Sig], e.IDs...)
	}
	return t
}

// MultiTable is several independent bucket tables (one per LSH table).
type MultiTable struct{ Tables []*Table }

// NewMultiTable creates n empty tables.
func NewMultiTable(n int) *MultiTable {
	ts := &MultiTable{Tables: make([]*Table, n)}
	for i := range ts.Tables {
		ts.Tables[i] = NewTable()
	}
	return ts
}

// Add inserts id with its per-table signatures.
func (m *MultiTable) Add(sigs []Signature, id ID) {
	for t, s := range sigs {
		m.Tables[t].Add(s, id)
	}
}

// Candidates returns the deduplicated union of bucket members across all tables.
func (m *MultiTable) Candidates(sigs []Signature) []ID {
	seen := make(map[ID]struct{})
	for t, s := range sigs {
		for _, id := range m.Tables[t].Get(s) {
			seen[id] = struct{}{}
		}
	}
	out := make([]ID, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}
