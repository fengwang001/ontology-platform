// Package check provides a naive ordered reference used to validate skip.
package check

import "sort"

// Ref is a naive sorted-slice index mirroring skip.List semantics.
type Ref struct {
	keys []int
	vals []int
}

func (r *Ref) Insert(key, val int) bool {
	i := sort.SearchInts(r.keys, key)
	if i < len(r.keys) && r.keys[i] == key {
		return false
	}
	r.keys = append(r.keys, 0)
	r.vals = append(r.vals, 0)
	copy(r.keys[i+1:], r.keys[i:])
	copy(r.vals[i+1:], r.vals[i:])
	r.keys[i], r.vals[i] = key, val
	return true
}

func (r *Ref) Find(key int) (int, bool) {
	i := sort.SearchInts(r.keys, key)
	if i < len(r.keys) && r.keys[i] == key {
		return r.vals[i], true
	}
	return 0, false
}

func (r *Ref) Delete(key int) bool {
	i := sort.SearchInts(r.keys, key)
	if i == len(r.keys) || r.keys[i] != key {
		return false
	}
	r.keys = append(r.keys[:i], r.keys[i+1:]...)
	r.vals = append(r.vals[:i], r.vals[i+1:]...)
	return true
}

func (r *Ref) Range(lo, hi int) []int {
	i := sort.SearchInts(r.keys, lo)
	j := sort.SearchInts(r.keys, hi)
	return append([]int(nil), r.keys[i:j]...)
}

func (r *Ref) Len() int { return len(r.keys) }
