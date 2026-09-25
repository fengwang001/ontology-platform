// Package ver defines a single MVCC version: a commit timestamp plus
// either a value or a tombstone. It depends on nothing.
package ver

// Version is one immutable entry in a key's version chain.
type Version struct {
	ts    int64
	value string
	tomb  bool
}

// Value creates a version carrying a non-empty value.
func Value(ts int64, value string) Version { return Version{ts: ts, value: value} }

// Tombstone creates a deletion-marker version.
func Tombstone(ts int64) Version { return Version{ts: ts, tomb: true} }

// TS returns the commit timestamp.
func (v Version) TS() int64 { return v.ts }

// IsTombstone reports whether this version is a deletion marker.
func (v Version) IsTombstone() bool { return v.tomb }

// Get returns the value and whether the version is visible (not a tombstone).
func (v Version) Get() (string, bool) {
	if v.tomb {
		return "", false
	}
	return v.value, true
}

// Before reports whether v sorts before other by commit timestamp.
func (v Version) Before(other Version) bool { return v.ts < other.ts }
