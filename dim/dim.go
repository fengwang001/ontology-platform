// Package dim holds versioned dimension snapshots: current table, monotonic
// version, retained snapshot history, byte accounting and oldest-first
// eviction under a memory cap. It depends on no other package.
package dim

import "errors"

// Distinct sentinel errors; every failure is decidable via errors.Is.
var (
	ErrEmptyKey         = errors.New("dim: empty key")                 // Entry/Fact Key == ""
	ErrEmptyBatch       = errors.New("dim: empty batch")               // zero entries
	ErrMaxBytes         = errors.New("dim: maxBytes must be positive") // NewStore arg <= 0
	ErrFuture           = errors.New("dim: version in the future")     // Vsn > V
	ErrSnapshotTooLarge = errors.New("dim: snapshot exceeds maxBytes") // floor still over cap
)

// Entry is one dimension row. Size rule: len(Key)+8 regardless of Val.
type Entry struct {
	Key string
	Val string
}

// Kind classifies a lookup outcome.
type Kind int

const (
	KindUnknown Kind = iota
	KindHit          // found in the requested snapshot
	KindMiss         // version live, Key absent
	KindStale        // older than the oldest retained snapshot
	KindFuture
)

// Snapshot is one immutable version of the dimension table.
type Snapshot struct {
	Vsn   int64
	bytes int64
	m     map[string]string
}

// Store is the versioned dimension store; build it with NewStore. The api
// layer serializes all access.
type Store struct {
	v     int64       // current version, starts at 0
	max   int64       // memory cap
	used  int64       // bytes of all retained snapshots
	hist  []*Snapshot // retained versions, oldest first; starts with empty v0
	probe int         // unexported: entries inspected by the last Lookup
}

// NewStore creates a store whose version 0 is an empty 0-byte snapshot.
func NewStore(maxBytes int64) (*Store, error) {
	if maxBytes <= 0 {
		return nil, ErrMaxBytes
	}
	return &Store{v: 0, max: maxBytes, hist: []*Snapshot{{Vsn: 0, m: map[string]string{}}}}, nil
}

// V returns the current version; Used the total retained bytes.
func (s *Store) V() int64    { return s.v }
func (s *Store) Used() int64 { return s.used }

// Versions returns retained version numbers, oldest first (a contiguous run).
func (s *Store) Versions() []int64 {
	out := make([]int64, len(s.hist))
	for i, sn := range s.hist {
		out[i] = sn.Vsn
	}
	return out
}

// Current returns a copy of the current dimension table.
func (s *Store) Current() map[string]string {
	cur := s.hist[len(s.hist)-1]
	out := make(map[string]string, len(cur.m))
	for k, v := range cur.m {
		out[k] = v
	}
	return out
}

func entryBytes(key string) int64 { return int64(len(key)) + 8 }

// Broadcast upserts the whole batch into a new snapshot, appends it, then
// evicts oldest-first until used<=maxBytes or only V and V-1 remain. If the
// floor still exceeds the cap the batch is rejected: simulation happens on
// local copies, so a rejection touches none of s.v/s.hist/s.used.
func (s *Store) Broadcast(batch []Entry) (int64, error) {
	if len(batch) == 0 {
		return 0, ErrEmptyBatch
	}
	cur := s.hist[len(s.hist)-1]
	next := make(map[string]string, len(cur.m)+len(batch))
	for k, v := range cur.m {
		next[k] = v
	}
	nb := cur.bytes
	for _, e := range batch {
		if e.Key == "" {
			return 0, ErrEmptyKey // before any state is touched
		}
		if _, ok := next[e.Key]; !ok {
			nb += entryBytes(e.Key)
		}
		next[e.Key] = e.Val
	}
	hist := append(append(make([]*Snapshot, 0, len(s.hist)+1), s.hist...),
		&Snapshot{Vsn: s.v + 1, m: next, bytes: nb})
	used := s.used + nb
	for used > s.max && len(hist) > 2 { // evict oldest first; floor is two versions
		used -= hist[0].bytes
		hist = hist[1:]
	}
	if used > s.max {
		return 0, ErrSnapshotTooLarge // reject, everything still untouched
	}
	s.v++
	s.hist, s.used = hist, used
	return s.v, nil
}

// Lookup classifies key against exactly snapshot vsn and records the number
// of entries inspected in the unexported probe counter.
func (s *Store) Lookup(vsn int64, key string) (string, Kind) {
	s.probe = 0
	if vsn > s.v {
		return "", KindFuture
	}
	oldest := s.hist[0].Vsn
	if vsn < oldest {
		return "", KindStale
	}
	s.probe = 1 // exactly one map probe, independent of snapshot size
	val, ok := s.hist[vsn-oldest].m[key]
	if !ok {
		return "", KindMiss
	}
	return val, KindHit
}
