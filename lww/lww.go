// Package lww holds the two-level last-write-wins CRDT state:
// outer[outer key] -> inner[inner key] -> (ts, rep, value), plus one
// outer tombstone per outer key. It depends on no other package.
package lww

// Entry is one inner value stamped with its Lamport timestamp and replica.
type Entry struct {
	TS    int64
	Rep   string
	Value int
}

// outer is the per-outer-key state. inner is never physically cleared by a
// tombstone: the tombstone only raises the visibility threshold.
type outer struct {
	tomb  int64
	inner map[string]Entry
}

// Map is the whole two-level LWW state. The zero value is not usable; use New.
type Map struct {
	m map[string]*outer
}

// New returns an empty Map.
func New() *Map { return &Map{m: make(map[string]*outer)} }

// wins reports whether b should replace a: larger ts wins; on a tie the
// lexicographically larger replica name wins.
func wins(a, b Entry) bool {
	return b.TS > a.TS || (b.TS == a.TS && b.Rep > a.Rep)
}

// Put writes inner[i] with keyed LWW. A write with ts <= the outer tombstone
// is late: it is ignored entirely (it neither revives the outer nor touches
// inner). Callers validate the keys and ts beforehand.
func (x *Map) Put(o, i string, v int, ts int64, rep string) {
	st := x.m[o]
	if st == nil {
		st = &outer{inner: make(map[string]Entry)}
		x.m[o] = st
	}
	if ts <= st.tomb {
		return
	}
	e := Entry{TS: ts, Rep: rep, Value: v}
	if cur, ok := st.inner[i]; !ok || wins(cur, e) {
		st.inner[i] = e
	}
}

// DelOuter raises the tombstone of outer key o to max(tomb, ts) without
// physically clearing its inner entries.
func (x *Map) DelOuter(o string, ts int64) {
	st := x.m[o]
	if st == nil {
		st = &outer{inner: make(map[string]Entry)}
		x.m[o] = st
	}
	if ts > st.tomb {
		st.tomb = ts
	}
}

// Clone returns a deep copy with no shared maps, so merging the clone into
// another map cannot alias the source state.
func (x *Map) Clone() *Map {
	c := &Map{m: make(map[string]*outer, len(x.m))}
	for key, st := range x.m {
		cp := &outer{tomb: st.tomb, inner: make(map[string]Entry, len(st.inner))}
		for ik, e := range st.inner {
			cp.inner[ik] = e
		}
		c.m[key] = cp
	}
	return c
}

// Merge incrementally merges y into x: per outer key the tombstone becomes
// the max of both sides and inner maps are unioned with keyed LWW. y is
// never mutated; on a previously-absent outer key the inner map is copied,
// so the replicas stay independent afterwards.
func (x *Map) Merge(y *Map) {
	for key, yo := range y.m {
		xo := x.m[key]
		if xo == nil {
			xo = &outer{tomb: yo.tomb, inner: make(map[string]Entry, len(yo.inner))}
			x.m[key] = xo
		} else if yo.tomb > xo.tomb {
			xo.tomb = yo.tomb
		}
		for ik, ye := range yo.inner {
			if cur, ok := xo.inner[ik]; !ok || wins(cur, ye) {
				xo.inner[ik] = ye
			}
		}
	}
}

// Tomb reports the tombstone timestamp of outer key o and whether the key
// exists in the state (a key with only a tombstone still exists).
func (x *Map) Tomb(o string) (ts int64, ok bool) {
	if st := x.m[o]; st != nil {
		return st.tomb, true
	}
	return 0, false
}

// View returns all visible inner entries: an entry is visible iff its ts is
// strictly greater than its outer tombstone. Outer keys without any visible
// inner entry do not appear. The returned maps are fresh copies.
func (x *Map) View() map[string]map[string]int {
	view := make(map[string]map[string]int)
	for key, st := range x.m {
		var vis map[string]int
		for ik, e := range st.inner {
			if e.TS > st.tomb {
				if vis == nil {
					vis = make(map[string]int)
				}
				vis[ik] = e.Value
			}
		}
		if vis != nil {
			view[key] = vis
		}
	}
	return view
}
