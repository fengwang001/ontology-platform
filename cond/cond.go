// Package cond evaluates conditional-write preconditions against the
// current version of a key. It is pure evaluation logic: it knows
// nothing about storage, versions or quotas.
package cond

// MaxTime is the largest legal value for timestamps (now, mtime) and
// for the IfUnmodifiedSince parameter s.
const MaxTime = int64(1_000_000_000_000)

// Names of the sub-conditions, used in failure reports.
const (
	IfMatchName           = "IfMatch"
	IfUnmodifiedSinceName = "IfUnmodifiedSince"
	IfNoneMatchName       = "IfNoneMatch"
)

// Cond is a conditional-write precondition. Each sub-condition is
// optional; all given sub-conditions must hold simultaneously.
type Cond struct {
	// IfMatch, when non-nil, requires the current version to exist,
	// be a data version, and have an equal etag.
	IfMatch *string
	// IfUnmodifiedSince, when non-nil, requires the current version to
	// exist, be a data version, and have mtime <= s (equality passes).
	IfUnmodifiedSince *int64
	// IfNoneMatchStar, when true, requires that no current object
	// exists or that the current version is a delete marker.
	IfNoneMatchStar bool
}

// Valid reports whether all parameters of c are in range.
func (c Cond) Valid() bool {
	if c.IfUnmodifiedSince != nil {
		s := *c.IfUnmodifiedSince
		if s < 0 || s > MaxTime {
			return false
		}
	}
	return true
}

// Current describes the key's current version (the one with the
// highest version number).
type Current struct {
	Exists    bool   // any current version exists
	Tombstone bool   // current version is a delete marker
	Etag      string // etag of a data version
	Mtime     int64  // write time of a data version
}

// Check evaluates the given sub-conditions in the fixed order
// IfMatch, IfUnmodifiedSince, IfNoneMatch and reports the first
// failure. It returns ("", true) when all given sub-conditions hold.
func (c Cond) Check(cur Current) (failed string, ok bool) {
	data := cur.Exists && !cur.Tombstone
	if c.IfMatch != nil {
		if !data || cur.Etag != *c.IfMatch {
			return IfMatchName, false
		}
	}
	if c.IfUnmodifiedSince != nil {
		if !data || cur.Mtime > *c.IfUnmodifiedSince {
			return IfUnmodifiedSinceName, false
		}
	}
	if c.IfNoneMatchStar {
		if cur.Exists && !cur.Tombstone {
			return IfNoneMatchName, false
		}
	}
	return "", true
}
