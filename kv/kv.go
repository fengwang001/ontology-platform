// Package kv implements a single replica's entry table, version
// comparison, and the primary's monotonic per-key version advance.
// It depends on nothing outside the standard library.
package kv

// Entry is one key's value and version on a replica.
// Ver == 0 means the key does not exist.
type Entry struct {
	Val string
	Ver int64
}

// Replica is one replica's key -> Entry table.
type Replica struct {
	m map[string]Entry
}

// NewReplica returns an empty replica.
func NewReplica() *Replica { return &Replica{m: make(map[string]Entry)} }

// Get returns the entry for key; an absent key yields the zero Entry.
func (r *Replica) Get(key string) Entry { return r.m[key] }

// CaughtUp reports whether this replica's version of key is >= wv.
func (r *Replica) CaughtUp(key string, wv int64) bool { return r.m[key].Ver >= wv }

// Put installs e for key. Callers only ever install newer entries.
func (r *Replica) Put(key string, e Entry) { r.m[key] = e }

// Snapshot returns a copy of the whole table.
func (r *Replica) Snapshot() map[string]Entry {
	out := make(map[string]Entry, len(r.m))
	for k, e := range r.m {
		out[k] = e
	}
	return out
}

// Primary is the authoritative replica R0: it owns the global
// monotonically increasing per-key version counter ver[key].
type Primary struct {
	ver map[string]int64
	rep *Replica
}

// NewPrimary returns an empty primary.
func NewPrimary() *Primary {
	return &Primary{ver: make(map[string]int64), rep: NewReplica()}
}

// Write advances ver[key] by one, stores (val, v) on R0 and returns the
// new entry. Followers are not touched. Callers validate key beforehand.
func (p *Primary) Write(key, val string) Entry {
	p.ver[key]++
	e := Entry{Val: val, Ver: p.ver[key]}
	p.rep.Put(key, e)
	return e
}

// Replica exposes R0's table (read-routing fallback and Sync source).
func (p *Primary) Replica() *Replica { return p.rep }

// Version returns the authoritative version of key (0 if never written).
func (p *Primary) Version(key string) int64 { return p.ver[key] }
