// Package part defines a single key-range partition [Lo, Hi) and the
// pure split/merge predicates. It depends on nothing else.
package part

// Part is a half-open key range plus the exact set of keys it holds.
type Part struct {
	Lo, Hi int64
	Keys   map[int64]struct{}
}

// New returns an empty partition covering [lo, hi).
func New(lo, hi int64) *Part {
	return &Part{Lo: lo, Hi: hi, Keys: map[int64]struct{}{}}
}

// Load is the number of keys currently held; it is len(Keys) by
// definition, so it can never drift from a naive recount.
func (p *Part) Load() int { return len(p.Keys) }

// Contains reports whether key falls inside [Lo, Hi).
func (p *Part) Contains(key int64) bool { return p.Lo <= key && key < p.Hi }

// Mid is the split point: lo + (hi-lo)/2, integer division rounding down.
func (p *Part) Mid() int64 { return p.Lo + (p.Hi-p.Lo)/2 }

// Split divides p at Mid into [Lo, Mid) and [Mid, Hi). Keys < mid go
// left, keys >= mid go right (a key equal to mid goes right). Every key
// is moved exactly once, so the union of keys is preserved.
func (p *Part) Split() (left, right *Part) {
	mid := p.Mid()
	left, right = New(p.Lo, mid), New(mid, p.Hi)
	for k := range p.Keys {
		if k < mid {
			left.Keys[k] = struct{}{}
		} else {
			right.Keys[k] = struct{}{}
		}
	}
	return left, right
}

// CanMerge reports whether a and b are adjacent (a.Hi == b.Lo) and their
// combined load does not exceed mergeThreshold.
func CanMerge(a, b *Part, mergeThreshold int) bool {
	return a.Hi == b.Lo && a.Load()+b.Load() <= mergeThreshold
}

// Merge combines two adjacent partitions into [a.Lo, b.Hi) holding the
// union of both key sets.
func Merge(a, b *Part) *Part {
	m := New(a.Lo, b.Hi)
	for k := range a.Keys {
		m.Keys[k] = struct{}{}
	}
	for k := range b.Keys {
		m.Keys[k] = struct{}{}
	}
	return m
}
