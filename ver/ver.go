// Package ver holds an immutable version of the key/value table.
// It must not import any other ontology package.
package ver

// Version is one immutable, published table state.
// Once constructed, its map must never be mutated by anyone.
type Version struct {
	id int
	m  map[string]string
}

// New creates a version with the given id holding m.
// The caller passes ownership of m and must not modify it afterwards.
func New(id int, m map[string]string) *Version {
	return &Version{id: id, m: m}
}

// Empty returns an empty version with the given id.
func Empty(id int) *Version {
	return &Version{id: id, m: nil}
}

// Clone returns a fresh, mutable map with a copy of every entry in v.
// The writer mutates the clone; v itself stays untouched.
func Clone(v *Version) map[string]string {
	c := make(map[string]string, v.Len())
	for k, val := range v.m {
		c[k] = val
	}
	return c
}

// ID returns the monotonically increasing version id.
func (v *Version) ID() int { return v.id }

// Len returns the number of distinct keys in this version.
func (v *Version) Len() int { return len(v.m) }

// Get returns the value and presence of k in this immutable version.
func (v *Version) Get(k string) (string, bool) {
	val, ok := v.m[k]
	return val, ok
}
