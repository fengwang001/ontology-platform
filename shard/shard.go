// Package shard maintains shard counters, per-key cumulative counts on
// base shards, the hot/migrated status of keys and the (unexported) counter
// used to prove O(1) migration-status lookup. It depends on no other
// package in this module.
//
// Tracker itself is single-goroutine; the caller (package route) holds the
// lock that guards concurrent access.
package shard

import "strconv"

// Tracker is the mutable sharding state.
type Tracker struct {
	S int // number of base shards
	T int // hot threshold

	// cnt[s] is the cumulative event count of shard s. Its length grows by
	// one every time a dedicated shard is allocated.
	cnt []int64

	// baseCnt[k] is k's cumulative count while still routed on a base shard.
	baseCnt map[string]int64

	// dedicated maps a migrated (hence permanently hot) key to its
	// dedicated shard. Presence in the map means the key is hot.
	dedicated map[string]int

	// lookupChecks counts the number of keys inspected while deciding
	// whether a key has migrated, across Feed calls. Unexported on purpose:
	// it must never appear in the public API, even as a returned number.
	lookupChecks int64
}

// New creates a Tracker with S base shards and threshold T.
func New(S, T int) *Tracker {
	return &Tracker{
		S:         S,
		T:         T,
		cnt:       make([]int64, S),
		baseCnt:   make(map[string]int64),
		dedicated: make(map[string]int),
	}
}

// Migrated reports whether key has migrated to a dedicated shard. Looking
// the key up in the hash map inspects exactly that one key.
func (t *Tracker) Migrated(key string) bool {
	t.lookupChecks++ // one key inspected: the hash lookup of key itself
	_, ok := t.dedicated[key]
	return ok
}

// DedicatedOf returns the dedicated shard of a migrated key.
func (t *Tracker) DedicatedOf(key string) (int, bool) {
	s, ok := t.dedicated[key]
	return s, ok
}

// AddBase counts one pre-migration event of key on base shard base and
// returns the key's new cumulative base count.
func (t *Tracker) AddBase(key string, base int) int64 {
	t.cnt[base]++
	t.baseCnt[key]++
	return t.baseCnt[key]
}

// AddShard counts one event on an already allocated shard s.
func (t *Tracker) AddShard(s int) {
	t.cnt[s]++
}

// CommitMigration permanently marks key hot and binds it to dedicated
// shard s, extending cnt so the new shard starts at zero.
func (t *Tracker) CommitMigration(key string, s int) {
	for len(t.cnt) <= s {
		t.cnt = append(t.cnt, 0)
	}
	t.dedicated[key] = s
}

// Snapshot returns a copy of cnt ordered by shard number.
func (t *Tracker) Snapshot() []int64 {
	out := make([]int64, len(t.cnt))
	copy(out, t.cnt)
	return out
}

// Sum returns the total count across all shards.
func (t *Tracker) Sum() int64 {
	var n int64
	for _, c := range t.cnt {
		n += c
	}
	return n
}

// LookupCostStaysConstant verifies, without ever exposing lookupChecks'
// numeric value, that deciding one non-hot key's status inspects a constant
// number of keys no matter how many keys have already migrated. Only the
// boolean verdict crosses the package boundary.
func LookupCostStaysConstant() bool {
	sizes := []int{100, 1000, 10000}
	var prev int64 = -1
	for _, m := range sizes {
		t := New(1, 3)
		for i := 0; i < m; i++ {
			t.CommitMigration("k"+strconv.Itoa(i), 1+i)
		}
		before := t.lookupChecks
		t.Migrated("a-non-hot-new-key")
		delta := t.lookupChecks - before
		if delta > 2 || (prev >= 0 && delta != prev) {
			return false
		}
		prev = delta
	}
	return true
}
