// Package join resolves facts against exactly the dimension snapshot named by
// the fact's version, producing join rows and classifying miss/stale/future.
// It depends only on dim.
package join

import "ontology/dim"

// Fact is a streaming fact; Vsn selects which dimension snapshot to query.
type Fact struct {
	Key string
	Vsn int64
}

// Row is one emitted join row.
type Row struct {
	Key string
	Val string
	Vsn int64
}

// Result classifies a Resolve outcome (stale and miss are legal, not errors).
type Result int

const (
	ResultUnknown  Result = iota
	ResultHit             // Row is valid
	ResultMiss            // version live, key absent
	ResultStale           // version evicted; caller counts a drop
	ResultFuture          // version > current V; caller surfaces an error
	ResultEmptyKey        // fact key was ""; caller surfaces an error
)

// Joiner binds a dim.Store to fact resolution. The bound store's access is
// serialized by the api layer; Joiner itself holds no mutable state.
type Joiner struct{ st *dim.Store }

// New returns a Joiner over the given store.
func New(st *dim.Store) *Joiner { return &Joiner{st: st} }

// Resolve looks f.Key up in snapshot f.Vsn. It performs exactly one snapshot
// lookup, so probe accounting inside dim reflects a single fact.
func (j *Joiner) Resolve(f Fact) (Row, Result) {
	if f.Key == "" {
		return Row{}, ResultEmptyKey
	}
	val, kind := j.st.Lookup(f.Vsn, f.Key)
	switch kind {
	case dim.KindHit:
		return Row{Key: f.Key, Val: val, Vsn: f.Vsn}, ResultHit
	case dim.KindMiss:
		return Row{}, ResultMiss
	case dim.KindStale:
		return Row{}, ResultStale
	default:
		return Row{}, ResultFuture
	}
}
