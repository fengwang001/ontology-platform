// Package queue is a bounded byte queue with one double-ended sequence per
// priority. Higher-priority (smaller number) records may evict strictly
// lower-priority ones, newest first; draining is by priority then seq.
package queue

import "errors"

const (
	MinCap  = 1
	MaxCap  = 1_000_000_000
	NumPrio = 3
)

var ErrInvalidCap = errors.New("queue: capacity out of range")

// Record is one queued item. Seq is assigned by the queue at enqueue time.
type Record struct {
	Seq    uint64
	Tenant string
	Prio   int
	Size   int64
}

// Queue is not safe for concurrent use; the caller (gateway) serializes.
type Queue struct {
	capBytes  int64
	used      int64
	seq       uint64
	deques    [NumPrio][]Record // per priority, seq ascending
	prioBytes [NumPrio]int64    // queued bytes per priority

	examinedEvict int // records inspected during the last Evict
	examinedDrain int // records inspected during the last Drain
}

func New(capBytes int64) (*Queue, error) {
	if capBytes < MinCap || capBytes > MaxCap {
		return nil, ErrInvalidCap
	}
	return &Queue{capBytes: capBytes}, nil
}

func (q *Queue) Cap() int64  { return q.capBytes }
func (q *Queue) Used() int64 { return q.used }
func (q *Queue) Free() int64 { return q.capBytes - q.used }

// EvictableBytes is the O(1) total of queued bytes with priority strictly
// lower (numerically greater) than prio.
func (q *Queue) EvictableBytes(prio int) int64 {
	var sum int64
	for p := prio + 1; p < NumPrio; p++ {
		sum += q.prioBytes[p]
	}
	return sum
}

// Evict removes records with priority strictly lower than prio, scanning
// lowest priority first and newest (largest seq) first within a priority,
// stopping as soon as freed bytes reach need. Callers must ensure
// EvictableBytes(prio) >= need beforehand.
func (q *Queue) Evict(prio int, need int64) []Record {
	q.examinedEvict = 0
	var freed int64
	var out []Record
	for p := NumPrio - 1; p > prio && freed < need; p-- {
		d := q.deques[p]
		for len(d) > 0 && freed < need {
			r := d[len(d)-1]
			q.examinedEvict++
			d = d[:len(d)-1]
			freed += r.Size
			q.prioBytes[p] -= r.Size
			q.used -= r.Size
			out = append(out, r)
		}
		q.deques[p] = d
	}
	return out
}

// Enqueue appends a record at the back of its priority sequence.
func (q *Queue) Enqueue(tenant string, prio int, size int64) Record {
	q.seq++
	r := Record{Seq: q.seq, Tenant: tenant, Prio: prio, Size: size}
	q.deques[prio] = append(q.deques[prio], r)
	q.prioBytes[prio] += size
	q.used += size
	return r
}

// Drain pops records by ascending priority then ascending seq while the
// next record fits the remaining budget; it stops at the first record
// that does not fit and never skips ahead.
func (q *Queue) Drain(budget int64) []Record {
	q.examinedDrain = 0
	remaining := budget
	var out []Record
	for p := 0; p < NumPrio; p++ {
		d := q.deques[p]
		stopped := false
		for len(d) > 0 {
			r := d[0]
			q.examinedDrain++
			if r.Size > remaining {
				stopped = true
				break
			}
			d = d[1:]
			remaining -= r.Size
			q.prioBytes[p] -= r.Size
			q.used -= r.Size
			out = append(out, r)
		}
		q.deques[p] = d
		if stopped {
			// A record at this priority did not fit; lower priorities
			// must not be considered.
			return out
		}
	}
	return out
}

// examined returns the inspection counters of the last Evict and Drain.
func (q *Queue) examined() (evict, drain int) {
	return q.examinedEvict, q.examinedDrain
}

// queuedBytes recomputes per-priority totals from scratch; test helper.
func (q *Queue) queuedBytes() (used int64, per [NumPrio]int64) {
	for p := 0; p < NumPrio; p++ {
		for _, r := range q.deques[p] {
			per[p] += r.Size
			used += r.Size
		}
	}
	return used, per
}
