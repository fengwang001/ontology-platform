package refheap

import (
	"os"
	"sort"
	"strconv"
	"strings"
)

// naiveSim is an independent, deliberately literal reimplementation of the
// specification using plain maps; reachability is recomputed from sets at
// every phase rather than incrementally marked.
type naiveSim struct {
	c, u, m int64
	now     int64
	nextID  int64
	nextQ   int64
	used    int64

	objs   map[int64]*nObj
	roots  map[int64]bool
	queues map[int64][]int64
	finQ   []int64
	inFin  map[int64]bool
}

type nObj struct {
	size        int64
	isRef       bool
	kind        RefKind
	target      int64
	queue       int64
	ts          int64
	fields      []int64
	finalizable bool
}

func newNaive(c, u, m int64) *naiveSim {
	return &naiveSim{
		c: c, u: u, m: m,
		objs:   map[int64]*nObj{},
		roots:  map[int64]bool{},
		queues: map[int64][]int64{},
		inFin:  map[int64]bool{},
	}
}

// strongFrom computes the strong-reachability closure from seeds, traversing
// ordinary-object fields only.
func (n *naiveSim) strongFrom(seeds map[int64]bool) map[int64]bool {
	mark := map[int64]bool{}
	var stack []int64
	push := func(id int64) {
		if id != 0 && !mark[id] {
			if _, ok := n.objs[id]; ok {
				mark[id] = true
				stack = append(stack, id)
			}
		}
	}
	for id := range seeds {
		push(id)
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		o := n.objs[id]
		if !o.isRef {
			for _, f := range o.fields {
				push(f)
			}
		}
	}
	return mark
}

func idsOf(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

func nCollectResult() CollectResult {
	return CollectResult{
		Reclaimed:   []int64{},
		SoftCleared: []int64{},
		WeakCleared: []int64{},
		CatchAll:    []int64{},
		Finalized:   []int64{},
	}
}

func (n *naiveSim) collect(now int64) (CollectResult, ErrorCode) {
	if !validNow(now) {
		return CollectResult{}, ErrInvalidArg
	}
	if now < n.now {
		return CollectResult{}, ErrClockBackward
	}
	n.now = now
	res := nCollectResult()

	// Phase 1.
	seeds := map[int64]bool{}
	for id := range n.roots {
		seeds[id] = true
	}
	for _, id := range n.finQ {
		seeds[id] = true
	}
	marked := n.strongFrom(seeds)

	var used0 int64
	for id := range marked {
		used0 += n.objs[id].size
	}

	enqueue := func(o *nObj, id int64) {
		if o.queue != 0 {
			n.queues[o.queue] = append(n.queues[o.queue], id)
		}
	}

	// Phase 2: candidates frozen at end of phase 1; free frozen once.
	var softIDs []int64
	for id := range marked {
		if n.objs[id].isRef && n.objs[id].kind == Soft {
			softIDs = append(softIDs, id)
		}
	}
	sort.Slice(softIDs, func(a, b int) bool { return softIDs[a] < softIDs[b] })
	free := n.c - used0
	window := (free / n.u) * n.m
	for _, id := range softIDs {
		r := n.objs[id]
		if r.target == 0 || marked[r.target] {
			continue
		}
		if now-r.ts <= window {
			marked = n.strongFrom(mergeSeeds(marked, map[int64]bool{r.target: true}))
		} else {
			r.target = 0
			enqueue(r, id)
			res.SoftCleared = append(res.SoftCleared, id)
		}
	}

	// Phase 3.
	var weakIDs []int64
	for id := range marked {
		if n.objs[id].isRef && n.objs[id].kind == Weak {
			weakIDs = append(weakIDs, id)
		}
	}
	sort.Slice(weakIDs, func(a, b int) bool { return weakIDs[a] < weakIDs[b] })
	for _, id := range weakIDs {
		r := n.objs[id]
		if r.target != 0 && !marked[r.target] {
			r.target = 0
			enqueue(r, id)
			res.WeakCleared = append(res.WeakCleared, id)
		}
	}

	// Phase 4: whole selection determined before any resurrection.
	var selected []int64
	for id, o := range n.objs {
		if !marked[id] && o.finalizable && !o.isRef && !n.inFin[id] {
			selected = append(selected, id)
		}
	}
	sort.Slice(selected, func(a, b int) bool { return selected[a] < selected[b] })
	rSeeds := map[int64]bool{}
	for _, id := range selected {
		n.finQ = append(n.finQ, id)
		n.inFin[id] = true
		o := n.objs[id]
		o.finalizable = true // flag cleared only by Finalize
		rSeeds[id] = true
	}
	if len(selected) > 0 {
		marked = n.strongFrom(mergeSeeds(marked, rSeeds))
	}
	res.Finalized = append([]int64{}, selected...)

	// Phase 5.
	var refIDs []int64
	for id := range marked {
		if n.objs[id].isRef {
			refIDs = append(refIDs, id)
		}
	}
	sort.Slice(refIDs, func(a, b int) bool { return refIDs[a] < refIDs[b] })
	for _, id := range refIDs {
		r := n.objs[id]
		if r.target != 0 && !marked[r.target] {
			r.target = 0
			enqueue(r, id)
			res.CatchAll = append(res.CatchAll, id)
		}
	}

	// Phase 6.
	for _, id := range idsOf(mapNot(marked, n.objs)) {
		res.Reclaimed = append(res.Reclaimed, id)
	}
	for _, id := range res.Reclaimed {
		n.used -= n.objs[id].size
		delete(n.objs, id)
		delete(n.roots, id)
	}
	res.Used = n.used
	return res, 0
}

func mergeSeeds(a, b map[int64]bool) map[int64]bool {
	out := make(map[int64]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func mapNot(marked map[int64]bool, objs map[int64]*nObj) map[int64]bool {
	out := map[int64]bool{}
	for id := range objs {
		if !marked[id] {
			out[id] = true
		}
	}
	return out
}

// op is one replayable operation with a rendered log line.
type op struct {
	line string
	run  func(h *Heap, n *naiveSim) (accept bool, out string)
}

func fmtSlice(s []int64) string {
	parts := make([]string, len(s))
	for i, v := range s {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

var verboseNaive = os.Getenv("REFHEAP_VERBOSE") == "1"

func emptyInts(s []int64) []int64 {
	if s == nil {
		return []int64{}
	}
	return s
}
