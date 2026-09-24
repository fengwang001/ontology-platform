// Package bucket holds signature-to-vector-ID maps across multiple LSH tables.
package bucket

import "sort"

// Table maps a signature to the list of vector IDs sharing that bucket.
type Table map[uint64][]int

// Set is the collection of all tables.
type Set struct {
	Tables []Table
}

// NewSet creates L empty tables.
func NewSet(tables int) *Set {
	s := &Set{Tables: make([]Table, tables)}
	for i := range s.Tables {
		s.Tables[i] = Table{}
	}
	return s
}

// Add places id into the bucket keyed by sig in the given table.
func (s *Set) Add(table int, sig uint64, id int) {
	s.Tables[table][sig] = append(s.Tables[table][sig], id)
}

// Candidates returns the sorted, de-duplicated union of IDs in every table's
// bucket matching the query signatures.
func (s *Set) Candidates(sigs []uint64) []int {
	seen := map[int]struct{}{}
	for t, sig := range sigs {
		for _, id := range s.Tables[t][sig] {
			seen[id] = struct{}{}
		}
	}
	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// BucketCount returns the number of non-empty buckets across all tables.
func (s *Set) BucketCount() int {
	n := 0
	for _, t := range s.Tables {
		n += len(t)
	}
	return n
}

// DropMissing removes every ID not present in valid and returns the count
// of removed references. Empty buckets are pruned.
func (s *Set) DropMissing(valid map[int]bool) int {
	removed := 0
	for ti, t := range s.Tables {
		for sig, ids := range t {
			kept := ids[:0]
			for _, id := range ids {
				if valid[id] {
					kept = append(kept, id)
				} else {
					removed++
				}
			}
			if len(kept) == 0 {
				delete(t, sig)
			} else {
				t[sig] = kept
			}
		}
		s.Tables[ti] = t
	}
	return removed
}

// AllIDs returns the sorted set of every ID referenced by any bucket.
func (s *Set) AllIDs() []int {
	seen := map[int]struct{}{}
	for _, t := range s.Tables {
		for _, ids := range t {
			for _, id := range ids {
				seen[id] = struct{}{}
			}
		}
	}
	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}
