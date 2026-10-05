// Package querier implements IGMP querier election and the local query
// transmission schedule. General queries are derived lazily from phase
// segments (no background timers); group-specific queries live in a
// min-heap and are validated against membership state at drain time.
package querier

import (
	"container/heap"
	"math"
	"sort"
)

// Event is one locally originated query. General queries carry Group=0
// and Port=0.
type Event struct {
	Time    int64
	General bool
	Group   uint32
	Port    int
}

type segment struct {
	start int64
	end   int64 // inclusive; queries at start+k*qi <= end
}

type specEntry struct {
	time  int64
	group uint32
	port  int
	epoch uint64
	mcut  int
	ycut  int
}

type specHeap []specEntry

func (h specHeap) Len() int { return len(h) }
func (h specHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	if h[i].group != h[j].group {
		return h[i].group < h[j].group
	}
	return h[i].port < h[j].port
}
func (h specHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *specHeap) Push(x any)   { *h = append(*h, x.(specEntry)) }
func (h *specHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Querier tracks election state, router ports and the query schedule.
type Querier struct {
	ownIP   uint32
	qi      int64
	oqpi    int64
	querier bool
	oq      int64 // other-querier expiry while not querier
	segs    []segment
	gSeg    int
	gNext   int64
	yields  []int64 // times at which this machine yielded the querier role
	spec    specHeap
	routers map[int]int64 // port -> router-port expiry
}

// New creates a Querier that is the querier from time 0 and sends general
// queries at 0, qi, 2*qi, ...
func New(ownIP uint32, qi, oqpi int64) *Querier {
	return &Querier{
		ownIP:   ownIP,
		qi:      qi,
		oqpi:    oqpi,
		querier: true,
		segs:    []segment{{start: 0, end: math.MaxInt64}},
		routers: make(map[int]int64),
	}
}

// IsQuerier reports the current local querier role.
func (q *Querier) IsQuerier() bool { return q.querier }

// YieldCuts returns the historical yield times; index i is the cut that
// cancels specific queries scheduled before yield i with time > cut.
func (q *Querier) YieldCuts() []int64 { return q.yields }

// Advance applies the time-driven recovery: if the other querier's
// presence expired at or before now, the local machine resumes the
// querier role and restarts general queries from the recovery instant.
func (q *Querier) Advance(now int64) {
	if !q.querier && q.oq <= now {
		q.querier = true
		q.segs = append(q.segs, segment{start: q.oq, end: math.MaxInt64})
		q.oq = 0
	}
}

// Query records a received foreign general query. The port always becomes
// (or stays) a router port; a lower source IP makes the local machine
// yield, a higher one only refreshes the router port.
func (q *Querier) Query(port int, srcIP uint32, now int64) {
	q.routers[port] = now + q.oqpi
	if srcIP < q.ownIP {
		if q.querier {
			q.querier = false
			q.segs[len(q.segs)-1].end = now
			q.yields = append(q.yields, now)
		}
		q.oq = now + q.oqpi
	}
}

// Schedule enqueues group-specific queries. mcut indexes the membership
// report cuts; the current yield epoch is captured internally.
func (q *Querier) Schedule(group uint32, port int, times []int64, epoch uint64, mcut int) {
	ycut := len(q.yields)
	for _, t := range times {
		heap.Push(&q.spec, specEntry{time: t, group: group, port: port, epoch: epoch, mcut: mcut, ycut: ycut})
	}
}

// RouterPorts returns the sorted router ports alive at now, pruning
// expired entries. examined counts the router entries touched.
func (q *Querier) RouterPorts(now int64) (ports []int, examined int) {
	for p, exp := range q.routers {
		examined++
		if now < exp {
			ports = append(ports, p)
		} else {
			delete(q.routers, p)
		}
	}
	sort.Ints(ports)
	return ports, examined
}

// Drain returns every local query with time in (lastDrain, now], ordered
// by (time, general before specific, group, port). valid is the membership
// validity callback for specific queries.
func (q *Querier) Drain(now int64, valid func(group uint32, port int, epoch uint64, mcut int, time int64) bool) []Event {
	var general []Event
	for q.gSeg < len(q.segs) {
		seg := &q.segs[q.gSeg]
		if q.gNext < seg.start {
			q.gNext = seg.start
		}
		if q.gNext > seg.end {
			q.gSeg++
			continue
		}
		if q.gNext > now {
			break
		}
		general = append(general, Event{Time: q.gNext, General: true})
		q.gNext += q.qi
	}
	var specific []Event
	for len(q.spec) > 0 && q.spec[0].time <= now {
		e := heap.Pop(&q.spec).(specEntry)
		if e.ycut < len(q.yields) && e.time > q.yields[e.ycut] {
			continue
		}
		if !valid(e.group, e.port, e.epoch, e.mcut, e.time) {
			continue
		}
		specific = append(specific, Event{Time: e.time, Group: e.group, Port: e.port})
	}
	out := make([]Event, 0, len(general)+len(specific))
	i, j := 0, 0
	for i < len(general) && j < len(specific) {
		if general[i].Time <= specific[j].Time {
			out = append(out, general[i])
			i++
		} else {
			out = append(out, specific[j])
			j++
		}
	}
	out = append(out, general[i:]...)
	out = append(out, specific[j:]...)
	return out
}
