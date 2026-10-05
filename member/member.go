// Package member tracks per-port group membership for an IGMP snooping
// switch. Membership is a pure function of time: records carry an expiry
// and are considered alive exactly while now < exp. Expired records are
// reclaimed lazily through a min-heap, never by a full table scan.
package member

import (
	"container/heap"
	"errors"
	"sort"
)

var (
	ErrNonMember  = errors.New("member: not a member")
	ErrPortLimit  = errors.New("member: per-port membership limit exceeded")
	ErrGroupLimit = errors.New("member: group count limit exceeded")
)

// Spec describes group-specific queries scheduled by a Leave.
type Spec struct {
	Times []int64
	Epoch uint64
	MCut  int
}

type record struct {
	exp   int64
	epoch uint64
	cuts  []int64 // report times; pending queries with time > cut are cancelled
	qs    []int64 // specific-query times of the latest effective leave
	qMcut int     // len(cuts) when qs was scheduled
	qYcut int     // len(yieldCuts) when qs was scheduled
	hasQ  bool    // a leave scheduled specific queries on this generation
	maxQ  int64   // latest specific-query time scheduled on this generation
}

// tomb preserves the validity data of a removed membership generation for
// as long as undrained specific queries may still reference it.
type tomb struct {
	exp  int64
	cuts []int64
	maxQ int64
}

type expEntry struct {
	exp   int64
	group uint32
	port  int
	epoch uint64
}

type expHeap []expEntry

func (h expHeap) Len() int           { return len(h) }
func (h expHeap) Less(i, j int) bool { return h[i].exp < h[j].exp }
func (h expHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expHeap) Push(x any)        { *h = append(*h, x.(expEntry)) }
func (h *expHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Table stores membership for all groups and ports.
type Table struct {
	rb         int
	lmqi       int64
	gmi        int64
	lp         int
	gmax       int
	fastLeave  []bool // indexed by port, 1..P
	groups     map[uint32]map[int]*record
	tombs      map[uint64]tomb
	portCnt    []int // live membership count per port, indexed 1..P
	liveGroups int
	epochNext  uint64
	exps       expHeap
}

// NewTable builds a membership table for ports 1..ports.
func NewTable(ports, rb int, lmqi, gmi int64, fastLeave []bool, gmax, lp int) *Table {
	fl := make([]bool, ports+1)
	copy(fl[1:], fastLeave)
	return &Table{
		rb:        rb,
		lmqi:      lmqi,
		gmi:       gmi,
		lp:        lp,
		gmax:      gmax,
		fastLeave: fl,
		groups:    make(map[uint32]map[int]*record),
		tombs:     make(map[uint64]tomb),
		portCnt:   make([]int, ports+1),
	}
}

func (t *Table) get(port int, group uint32) *record {
	if m, ok := t.groups[group]; ok {
		return m[port]
	}
	return nil
}

// IsMember reports whether (group, port) is a live member at now.
func (t *Table) IsMember(port int, group uint32, now int64) bool {
	rec := t.get(port, group)
	return rec != nil && now < rec.exp
}

// Expire lazily removes every membership whose expiry is <= now.
// Each heap entry is popped exactly once, so the amortized cost is
// independent of the number of groups.
func (t *Table) Expire(now int64) {
	for len(t.exps) > 0 && t.exps[0].exp <= now {
		e := heap.Pop(&t.exps).(expEntry)
		rec := t.get(e.port, e.group)
		if rec != nil && rec.epoch == e.epoch && rec.exp <= now {
			t.remove(e.port, e.group)
		}
	}
}

func (t *Table) remove(port int, group uint32) {
	m := t.groups[group]
	if rec := m[port]; rec != nil && rec.hasQ {
		t.tombs[rec.epoch] = tomb{exp: rec.exp, cuts: rec.cuts, maxQ: rec.maxQ}
	}
	delete(m, port)
	t.portCnt[port]--
	if len(m) == 0 {
		delete(t.groups, group)
		t.liveGroups--
	}
}

func (t *Table) pushExp(port int, group uint32, rec *record) {
	heap.Push(&t.exps, expEntry{exp: rec.exp, group: group, port: port, epoch: rec.epoch})
}

// Report makes (group, port) a member with expiry now+gmi, refreshing an
// existing membership and cancelling its not-yet-due specific queries.
func (t *Table) Report(port int, group uint32, now int64) error {
	t.Expire(now)
	if rec := t.get(port, group); rec != nil && now < rec.exp {
		rec.exp = now + t.gmi
		rec.cuts = append(rec.cuts, now)
		rec.qs = nil
		t.pushExp(port, group, rec)
		return nil
	}
	if t.portCnt[port] >= t.lp {
		return ErrPortLimit
	}
	if _, ok := t.groups[group]; !ok && t.liveGroups >= t.gmax {
		return ErrGroupLimit
	}
	m := t.groups[group]
	if m == nil {
		m = make(map[int]*record)
		t.groups[group] = m
		t.liveGroups++
	}
	t.epochNext++
	rec := &record{exp: now + t.gmi, epoch: t.epochNext}
	m[port] = rec
	t.portCnt[port]++
	t.pushExp(port, group, rec)
	return nil
}

// Leave processes a leave for (group, port) at now. querier tells whether
// the local machine is the querier at this instant; yieldCuts are the
// historical querier-yield times used to tell whether previously scheduled
// specific queries are still pending. It returns the queries to schedule
// (nil when nothing must be scheduled).
func (t *Table) Leave(port int, group uint32, now int64, querier bool, yieldCuts []int64) (Spec, error) {
	rec := t.get(port, group)
	if rec == nil || now >= rec.exp {
		return Spec{}, ErrNonMember
	}
	if t.fastLeave[port] {
		t.remove(port, group)
		return Spec{}, nil
	}
	if !querier {
		return Spec{}, nil
	}
	if rec.pending(now, yieldCuts) {
		return Spec{}, nil
	}
	if newExp := now + int64(t.rb)*t.lmqi; newExp < rec.exp {
		rec.exp = newExp
		t.pushExp(port, group, rec)
	}
	times := make([]int64, t.rb)
	for i := range times {
		times[i] = now + int64(i)*t.lmqi
	}
	rec.qs = times
	rec.qMcut = len(rec.cuts)
	rec.qYcut = len(yieldCuts)
	rec.hasQ = true
	rec.maxQ = times[len(times)-1]
	return Spec{Times: times, Epoch: rec.epoch, MCut: rec.qMcut}, nil
}

// pending reports whether the record still has specific queries that have
// not yet been sent: scheduled after now, before expiry, and not cancelled
// by a report or a querier yield.
func (r *record) pending(now int64, yieldCuts []int64) bool {
	for _, s := range r.qs {
		if s <= now || s >= r.exp {
			continue
		}
		if r.qMcut < len(r.cuts) && s > r.cuts[r.qMcut] {
			continue
		}
		if r.qYcut < len(yieldCuts) && s > yieldCuts[r.qYcut] {
			continue
		}
		return true
	}
	return false
}

// QueryValid reports whether a scheduled specific query may still be sent:
// the membership must be the same generation, alive at the query time, and
// the query time must not be past the report cut recorded after scheduling.
func (t *Table) QueryValid(group uint32, port int, epoch uint64, mcut int, time int64) bool {
	if rec := t.get(port, group); rec != nil && rec.epoch == epoch {
		return time < rec.exp && (mcut >= len(rec.cuts) || time <= rec.cuts[mcut])
	}
	tm, ok := t.tombs[epoch]
	return ok && time < tm.exp && (mcut >= len(tm.cuts) || time <= tm.cuts[mcut])
}

// DropTombs reclaims tombstones whose latest scheduled query time is not
// after now; the querier has already drained every query up to now, so no
// heap entry can reference them any more.
func (t *Table) DropTombs(now int64) {
	for epoch, tm := range t.tombs {
		if tm.maxQ <= now {
			delete(t.tombs, epoch)
		}
	}
}

// LiveMembers returns the sorted ports whose membership of group is alive
// at now, pruning this group's expired records. examined counts the member
// records touched, which never exceeds the group's record count.
func (t *Table) LiveMembers(group uint32, now int64) (ports []int, examined int) {
	m := t.groups[group]
	if m == nil {
		return nil, 0
	}
	var expired []int
	for p, rec := range m {
		examined++
		if now < rec.exp {
			ports = append(ports, p)
		} else {
			expired = append(expired, p)
		}
	}
	for _, p := range expired {
		t.remove(p, group)
	}
	sort.Ints(ports)
	return ports, examined
}
