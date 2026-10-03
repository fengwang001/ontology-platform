// Package queue provides a bounded byte-capacity three-lane priority queue.
package queue

import "errors"

const Priorities = 3

// ErrQueueFull means strictly lower-priority records cannot free enough bytes.
var ErrQueueFull = errors.New("queue: full")

type Record struct {
	Seq    int64
	Tenant string
	Prio   int
	Size   int64

	older, newer *Record
}

type Queue struct {
	cap   int64
	used  int64
	seq   int64
	lanes [Priorities]lane

	evictExamined int64
	drainExamined int64
	drainReturned int64
	evictReturned int64
}

func New(cap int64) *Queue { return &Queue{cap: cap} }

func (q *Queue) Cap() int64                { return q.cap }
func (q *Queue) Used() int64               { return q.used }
func (q *Queue) UsedInLane(prio int) int64 { return q.lanes[prio].bytes }
func (q *Queue) LenInLane(prio int) int    { return q.lanes[prio].count }

func (q *Queue) Len() int {
	n := 0
	for p := 0; p < Priorities; p++ {
		n += q.lanes[p].count
	}
	return n
}

func (q *Queue) EvictableBytes(prio int) int64 {
	sum := int64(0)
	for p := prio + 1; p < Priorities; p++ {
		sum += q.lanes[p].bytes
	}
	return sum
}

// Plan is the eviction plan computed for a would-be admission.
type Plan struct {
	Need  int64
	Evict []*Record // removal order: low prio first, newest within a lane
}

// PlanAdmit returns the shortest eviction plan, mutating nothing.
func (q *Queue) PlanAdmit(prio int, size int64) (*Plan, error) {
	free := q.cap - q.used
	plan := &Plan{}
	if size <= free {
		return plan, nil
	}
	need := size - free
	if q.EvictableBytes(prio) < need {
		return nil, ErrQueueFull
	}
	plan.Need = need
	p, offset, released := Priorities-1, 0, int64(0)
	for p > prio && released < need {
		if offset >= q.lanes[p].count {
			p, offset = p-1, 0
			continue
		}
		r := q.lanes[p].atFromBack(offset)
		offset++
		q.evictExamined++
		plan.Evict = append(plan.Evict, r)
		released += r.Size
	}
	return plan, nil
}

func (q *Queue) Commit(tenant string, prio int, size int64, plan *Plan) *Record {
	for _, r := range plan.Evict {
		q.lanes[r.Prio].popBack()
		q.used -= r.Size
		q.evictReturned++
	}
	q.seq++
	r := &Record{Seq: q.seq, Tenant: tenant, Prio: prio, Size: size}
	q.lanes[prio].pushBack(r)
	q.used += size
	return r
}

func (q *Queue) Admit(tenant string, prio int, size int64) (*Record, []*Record, error) {
	plan, err := q.PlanAdmit(prio, size)
	if err != nil {
		return nil, nil, err
	}
	evicted := append([]*Record(nil), plan.Evict...)
	return q.Commit(tenant, prio, size, plan), evicted, nil
}

// PopDrain pops in priority/seq order within budget, stopping at first miss.
func (q *Queue) PopDrain(budget int64) []*Record {
	out := []*Record{}
	used := int64(0)
	for p := 0; p < Priorities; p++ {
		for q.lanes[p].front != nil {
			r := q.lanes[p].front
			q.drainExamined++
			if used+r.Size > budget {
				return out
			}
			q.lanes[p].popFront()
			q.used -= r.Size
			used += r.Size
			q.drainReturned++
			out = append(out, r)
		}
	}
	return out
}

func (q *Queue) Snapshot() []*Record {
	out := make([]*Record, 0, q.Len())
	for p := 0; p < Priorities; p++ {
		for r := q.lanes[p].front; r != nil; r = r.newer {
			cp := *r
			out = append(out, &cp)
		}
	}
	return out
}

type lane struct {
	front, back *Record
	count       int
	bytes       int64
}

func (l *lane) pushBack(r *Record) {
	r.older, r.newer = l.back, nil
	if l.back == nil {
		l.front = r
	} else {
		l.back.newer = r
	}
	l.back, l.count, l.bytes = r, l.count+1, l.bytes+r.Size
}

// remove unlinks r, which the caller already selected from this lane.
func (l *lane) remove(r *Record) {
	if r.older != nil {
		r.older.newer = r.newer
	} else {
		l.front = r.newer
	}
	if r.newer != nil {
		r.newer.older = r.older
	} else {
		l.back = r.older
	}
	r.older, r.newer = nil, nil
	l.count--
	l.bytes -= r.Size
}

func (l *lane) popFront() *Record {
	r := l.front
	if r != nil {
		l.remove(r)
	}
	return r
}

func (l *lane) popBack() *Record {
	r := l.back
	if r != nil {
		l.remove(r)
	}
	return r
}

// atFromBack returns the record offset slots from the back (0 == newest).
func (l *lane) atFromBack(offset int) *Record {
	for r := l.back; r != nil; r = r.older {
		if offset == 0 {
			return r
		}
		offset--
	}
	return nil
}
