// Package bucket maps LSH signatures to vector IDs in hash tables.
package bucket

import (
	"ontology/hyper"
	"ontology/vec"
)

// Table is one LSH hash table: signature -> list of vector IDs.
type Table struct {
	fam     *hyper.Family
	buckets map[uint64][]int
}

// NewTable creates an empty table using the given hyperplane family.
func NewTable(fam *hyper.Family) *Table {
	return &Table{fam: fam, buckets: make(map[uint64][]int)}
}

// FromBuckets rebuilds a table from persisted buckets and hyperplanes.
func FromBuckets(fam *hyper.Family, buckets map[uint64][]int) *Table {
	return &Table{fam: fam, buckets: buckets}
}

// Add inserts vector id into the bucket selected by its signature.
func (t *Table) Add(id int, v vec.Vec) {
	sig := t.fam.Signature(v)
	t.buckets[sig] = append(t.buckets[sig], id)
}

// Candidates returns the IDs sharing the query's bucket in this table.
func (t *Table) Candidates(q vec.Vec) []int {
	return t.buckets[t.fam.Signature(q)]
}

// Family exposes the hyperplane family for persistence.
func (t *Table) Family() *hyper.Family { return t.fam }

// Buckets exposes the raw signature -> IDs map for persistence.
func (t *Table) Buckets() map[uint64][]int { return t.buckets }
