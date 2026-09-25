// Package rep is a single in-memory replica: a key/val map held at some
// applied lsn, plus catch-up that replays a contiguous lsn interval taken
// directly from the global log. It depends only on package log.
package rep

import "ontology/log"

// Log and Entry are re-exported so upper layers depend only on rep; the
// package direction stays api -> rep -> log with no reverse dependency.
type Log = log.Log
type Entry = log.Entry

// NewLog creates the global append-only write log.
func NewLog() *Log { return log.New() }

// Replica applies writes in lsn order. Offline periods simply leave applied
// behind the log; CatchUp replays the missed interval later.
type Replica struct {
	applied int
	data    map[string]string
	// lastScan is the number of log entries examined by the most recent
	// CatchUp. Unexported on purpose: it is an internal complexity probe and
	// must never be reachable through the public API.
	lastScan int
}

// New returns an empty replica at applied 0.
func New() *Replica { return &Replica{data: map[string]string{}} }

// Applied is the latest lsn applied to the replica.
func (r *Replica) Applied() int { return r.applied }

// Apply stores one entry and advances applied to its lsn.
func (r *Replica) Apply(e log.Entry) {
	r.data[e.Key] = e.Val
	r.applied = e.LSN
}

// Get returns the value at applied, and false if the key was never written.
func (r *Replica) Get(key string) (string, bool) {
	v, ok := r.data[key]
	return v, ok
}

// CatchUp applies the closed interval (r.applied, through] from src, in lsn
// order, and returns the lsns it applied. The interval is located directly
// by lsn via log.Range, so the entries examined are exactly through-applied
// regardless of total log length; that count is recorded in lastScan.
func (r *Replica) CatchUp(src *log.Log, through int) []int {
	es := src.Range(r.applied, through)
	r.lastScan = len(es)
	lsns := make([]int, 0, len(es))
	for _, e := range es {
		r.Apply(e)
		lsns = append(lsns, e.LSN)
	}
	return lsns
}

// Snapshot returns applied and a copy of the data as of applied.
func (r *Replica) Snapshot() (int, map[string]string) {
	out := make(map[string]string, len(r.data))
	for k, v := range r.data {
		out[k] = v
	}
	return r.applied, out
}
