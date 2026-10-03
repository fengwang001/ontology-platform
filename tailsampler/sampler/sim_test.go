package sampler

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"ontology/tailsampler/policy"
)

func (n *naive) decide(tid string, e *naiveEntry, now uint64, evicted bool) Decision {
	keep, reason := policy.Decide(tid, e.seenErr, e.maxDur, n.p.L, n.p.P, policy.FNV1a32)
	win := now / n.p.Wb
	if !n.hasWin || win != n.lastWin {
		n.used, n.lastWin, n.hasWin = 0, win, true
	}
	if keep {
		if reason == policy.ReasonProb {
			if n.used < n.p.Q {
				n.used++
			} else {
				keep, reason = false, policy.ReasonBudget
			}
		} else {
			n.used++
		}
	}
	if n.cacheEnabled() {
		for id, ce := range n.cache {
			if ce.decidedAt+n.p.Td <= now {
				delete(n.cache, id)
			}
		}
		n.cache[tid] = naiveCacheEnt{keep: keep, decidedAt: now}
		for uint64(len(n.cache)) > n.p.Cmax {
			victim := minKey(n.cache, func(a string, av naiveCacheEnt, b string, bv naiveCacheEnt) bool {
				return av.decidedAt < bv.decidedAt || (av.decidedAt == bv.decidedAt && a < b)
			})
			delete(n.cache, victim)
		}
	}
	return Decision{TraceID: tid, Keep: keep, Reason: reason, Spans: len(e.ids), At: now, Evicted: evicted}
}

func (n *naive) ingest(now uint64, tid, sid string, dur uint64, isErr bool) ([]Decision, error) {
	if tid == "" || sid == "" || dur > 1e9 || now > 1e12 {
		return nil, ErrInvalidParam
	}
	if n.hasNow && now < n.maxNow {
		return nil, ErrClock
	}
	if n.cacheEnabled() {
		if ce, ok := n.cache[tid]; ok && ce.decidedAt+n.p.Td > now {
			n.maxNow, n.hasNow = now, true
			return nil, nil
		}
	}
	if e, ok := n.buf[tid]; ok {
		if _, dup := e.ids[sid]; dup {
			return nil, ErrDuplicate
		}
	}
	n.maxNow, n.hasNow = now, true
	var out []Decision
	e, ok := n.buf[tid]
	if !ok {
		if uint64(len(n.buf)) >= n.p.Nmax {
			victim := minKey(n.buf, func(a string, av *naiveEntry, b string, bv *naiveEntry) bool {
				return av.lastSeen < bv.lastSeen || (av.lastSeen == bv.lastSeen && a < b)
			})
			ve := n.buf[victim]
			delete(n.buf, victim)
			out = append(out, n.decide(victim, ve, now, true))
		}
		e = &naiveEntry{ids: map[string]struct{}{}}
		n.buf[tid] = e
	}
	e.ids[sid] = struct{}{}
	if dur > e.maxDur {
		e.maxDur = dur
	}
	if isErr {
		e.seenErr = true
	}
	e.lastSeen = now
	if uint64(len(e.ids)) >= n.p.Sc {
		delete(n.buf, tid)
		out = append(out, n.decide(tid, e, now, false))
	}
	return out, nil
}

func (n *naive) tick(now uint64) ([]Decision, error) {
	if now > 1e12 {
		return nil, ErrInvalidParam
	}
	if n.hasNow && now < n.maxNow {
		return nil, ErrClock
	}
	n.maxNow, n.hasNow = now, true
	var due []string
	for id, e := range n.buf {
		if e.lastSeen+n.p.W <= now {
			due = append(due, id)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		a, b := n.buf[due[i]], n.buf[due[j]]
		if a.lastSeen != b.lastSeen {
			return a.lastSeen < b.lastSeen
		}
		return due[i] < due[j]
	})
	var out []Decision
	for _, id := range due {
		e := n.buf[id]
		delete(n.buf, id)
		out = append(out, n.decide(id, e, now, false))
	}
	return out, nil
}

func TestNaiveComparison(t *testing.T) {
	p := Params{W: 5, Sc: 3, Nmax: 4, Td: 8, Cmax: 3, L: 50, P: 5000, Wb: 20, Q: 2}
	ops := genOps(1, 3000)
	s, n := mustNew(t, p), newNaive(p)
	var accepted, decidedSpans uint64
	var allA []Decision
	for i, o := range ops {
		da, db, ea, eb := runOp(s, n, o)
		if (ea == nil) != (eb == nil) || (ea != nil && !errors.Is(ea, eb)) {
			t.Fatalf("op %d %+v: sampler err %v, naive err %v", i, o, ea, eb)
		}
		if !reflect.DeepEqual(da, db) {
			t.Fatalf("op %d %+v:\n sampler=%v\n naive  =%v", i, o, da, db)
		}
		t.Logf("op %d %+v -> err=%v decisions=%v", i, o, ea, da)
		if ea == nil && !o.isTick {
			accepted++
		}
		for _, d := range da {
			decidedSpans += uint64(d.Spans)
		}
		allA = append(allA, da...)
		if uint64(len(s.buf)) > p.Nmax {
			t.Fatalf("op %d: buffer %d exceeds Nmax", i, len(s.buf))
		}
		for _, it := range s.buf {
			if uint64(it.entry.Count()) > p.Sc {
				t.Fatalf("op %d: trace %s spans %d exceeds Sc", i, it.traceID, it.entry.Count())
			}
		}
	}
	var buffered uint64
	for _, it := range s.buf {
		buffered += uint64(it.entry.Count())
	}
	total := decidedSpans + buffered + s.LateKept() + s.LateDropped()
	if accepted != total {
		t.Fatalf("conservation: accepted=%d != decided %d + buffered %d + lateK %d + lateD %d",
			accepted, decidedSpans, buffered, s.LateKept(), s.LateDropped())
	}
	t.Logf("accepted=%d decided=%d buffered=%d lateKept=%d lateDropped=%d",
		accepted, decidedSpans, buffered, s.LateKept(), s.LateDropped())

	// Replay: the same input sequence must reproduce the same decisions.
	s2, n2 := mustNew(t, p), newNaive(p)
	var allB []Decision
	for _, o := range ops {
		da, _, _, _ := runOp(s2, n2, o)
		allB = append(allB, da...)
	}
	if !reflect.DeepEqual(allA, allB) {
		t.Fatal("replay diverged: same input sequence gave different decisions")
	}
}
