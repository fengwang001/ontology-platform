package refheap

import (
	"fmt"
	"math/rand"
	"reflect"
)

type snapshot struct {
	used    int64
	objects map[int64]objSnap
	roots   []int64
	queues  map[int64][]int64
	finQ    []int64
}

type objSnap struct {
	size        int64
	isRef       bool
	kind        RefKind
	target      int64
	queue       int64
	ts          int64
	fields      []int64
	finalizable bool
}

func sortedKeys[V any](m map[int64]V) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortInts(out)
	return out
}

func sortInts(s []int64) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func copyInts(s []int64) []int64 {
	out := make([]int64, len(s))
	copy(out, s)
	return out
}

func snapHeap(h *Heap) snapshot {
	s := snapshot{
		used:    h.used,
		objects: map[int64]objSnap{},
		roots:   sortedKeys(h.roots),
		queues:  map[int64][]int64{},
		finQ:    copyInts(h.finalQueue),
	}
	for id, o := range h.objs {
		s.objects[id] = objSnap{
			size: o.size, isRef: o.isRef, kind: o.kind,
			target: o.target, queue: o.queue, ts: o.timestamp,
			fields: copyInts(o.fields), finalizable: o.finalizable,
		}
	}
	for q, qq := range h.queues {
		s.queues[q] = copyInts(qq)
	}
	return s
}

func snapNaive(n *naiveSim) snapshot {
	s := snapshot{
		used:    n.used,
		objects: map[int64]objSnap{},
		roots:   sortedKeys(n.roots),
		queues:  map[int64][]int64{},
		finQ:    copyInts(n.finQ),
	}
	for id, o := range n.objs {
		s.objects[id] = objSnap{
			size: o.size, isRef: o.isRef, kind: o.kind,
			target: o.target, queue: o.queue, ts: o.ts,
			fields: copyInts(o.fields), finalizable: o.finalizable,
		}
	}
	for q, qq := range n.queues {
		s.queues[q] = copyInts(qq)
	}
	return s
}

type generator struct {
	rng   *rand.Rand
	heap  *Heap
	sim   *naiveSim
	now   int64
	live  []int64 // ordinary object ids
	refs  []int64 // reference ids
	queue []int64
}

func kindName(k RefKind) string {
	switch k {
	case Soft:
		return "软"
	case Weak:
		return "弱"
	}
	return "虚"
}

func (g *generator) pick(from []int64) int64 {
	if len(from) == 0 {
		return 0
	}
	return from[g.rng.Intn(len(from))]
}

// oneOp builds one operation; deliberately malformed ops are included ~12% of
// the time to exercise the validation/ordering paths.
func (g *generator) oneOp() op {
	roll := g.rng.Intn(100)
	bad := g.rng.Intn(100) < 12

	switch {
	case roll < 18: // Alloc ordinary
		size := int64(1 + g.rng.Intn(20))
		fields := g.rng.Intn(4)
		fin := g.rng.Intn(2) == 0
		if bad {
			size = int64(g.rng.Intn(3) - 1) // -1 or 0
			if size < 1 {
				size = maxSize + int64(g.rng.Intn(5))
			}
		}
		return op{
			line: fmt.Sprintf("Alloc(s=%d,f=%d,fin=%v)", size, fields, fin),
			run: func(h *Heap, n *naiveSim) (bool, string) {
				id, err := h.Alloc(size, fields, fin)
				nid, code := n.alloc(size, fields, fin)
				if err != nil || code != 0 {
					return err != nil && code != 0, fmt.Sprintf("reject real=%v naive=%d", codeOf(err), code)
				}
				if id != nid {
					panic("id divergence")
				}
				g.live = append(g.live, id)
				return true, fmt.Sprintf("-> %d", id)
			},
		}
	case roll < 27: // NewRef
		kind := []RefKind{Soft, Weak, Phantom}[g.rng.Intn(3)]
		size := int64(1 + g.rng.Intn(8))
		now := g.now + int64(g.rng.Intn(6))
		target := g.pick(append(append([]int64{}, g.live...), g.refs...))
		if g.rng.Intn(4) == 0 {
			target = 0
		}
		queue := g.pick(g.queue)
		if g.rng.Intn(3) == 0 {
			queue = 0
		}
		if bad {
			kind = RefKind(7)
		}
		kName := kindName(kind)
		return op{
			line: fmt.Sprintf("NewRef(%s,t=%d,q=%d,s=%d,now=%d)", kName, target, queue, size, now),
			run: func(h *Heap, n *naiveSim) (bool, string) {
				id, err := h.NewRef(kind, target, queue, size, now)
				nid, code := n.newRef(kind, target, queue, size, now)
				if err != nil || code != 0 {
					return err != nil && code != 0, fmt.Sprintf("reject real=%v naive=%d", codeOf(err), code)
				}
				if id != nid {
					panic("ref id divergence")
				}
				g.now = now
				g.refs = append(g.refs, id)
				return true, fmt.Sprintf("-> %d", id)
			},
		}
	case roll < 32: // CreateQueue
		return op{
			line: "CreateQueue()",
			run: func(h *Heap, n *naiveSim) (bool, string) {
				q := h.CreateQueue()
				nq := n.createQueue()
				if q != nq {
					panic("queue divergence")
				}
				g.queue = append(g.queue, q)
				return true, fmt.Sprintf("-> %d", q)
			},
		}
	case roll < 48: // SetField
		o := g.pick(g.live)
		i := g.rng.Intn(3)
		t := g.pick(append(append([]int64{}, g.live...), g.refs...))
		if g.rng.Intn(3) == 0 {
			t = 0
		}
		return op{
			line: fmt.Sprintf("SetField(o=%d,i=%d,t=%d)", o, i, t),
			run: func(h *Heap, n *naiveSim) (bool, string) {
				err := h.SetField(o, i, t)
				code := n.setField(o, i, t)
				return err != nil == (code != 0), fmt.Sprintf("real=%v naive=%d", codeOf(err), code)
			},
		}
	case roll < 56: // SetRoot / ClearRoot
		id := g.pick(append(append([]int64{}, g.live...), g.refs...))
		clear := g.rng.Intn(2) == 0
		return op{
			line: fmt.Sprintf("Root(o=%d,clear=%v)", id, clear),
			run: func(h *Heap, n *naiveSim) (bool, string) {
				var err error
				var code ErrorCode
				if clear {
					err = h.ClearRoot(id)
					code = n.clearRoot(id)
				} else {
					err = h.SetRoot(id)
					code = n.setRoot(id)
				}
				return err != nil == (code != 0), fmt.Sprintf("real=%v naive=%d", codeOf(err), code)
			},
		}
	case roll < 64: // Get
		r := g.pick(g.refs)
		now := g.now + int64(g.rng.Intn(6))
		if bad {
			now = g.now - 1 - int64(g.rng.Intn(3))
		}
		return op{
			line: fmt.Sprintf("Get(r=%d,now=%d)", r, now),
			run: func(h *Heap, n *naiveSim) (bool, string) {
				t, err := h.Get(r, now)
				nt, code := n.get(r, now)
				if err != nil || code != 0 {
					return err != nil && code != 0, fmt.Sprintf("reject real=%v naive=%d", codeOf(err), code)
				}
				if t != nt {
					panic(fmt.Sprintf("get divergence %d vs %d", t, nt))
				}
				g.now = now
				return true, fmt.Sprintf("-> %d", t)
			},
		}
	case roll < 72: // Poll
		q := g.pick(g.queue)
		return op{
			line: fmt.Sprintf("Poll(q=%d)", q),
			run: func(h *Heap, n *naiveSim) (bool, string) {
				id, err := h.Poll(q)
				nid, code := n.poll(q)
				if err != nil || code != 0 {
					return err != nil && code != 0, fmt.Sprintf("reject real=%v naive=%d", codeOf(err), code)
				}
				if id != nid {
					panic("poll divergence")
				}
				return true, fmt.Sprintf("-> %d", id)
			},
		}
	case roll < 79: // Finalize
		k := g.rng.Intn(5)
		return op{
			line: fmt.Sprintf("Finalize(n=%d)", k),
			run: func(h *Heap, n *naiveSim) (bool, string) {
				ids, err := h.Finalize(k)
				nids, code := n.finalize(k)
				if err != nil || code != 0 {
					return err != nil && code != 0, fmt.Sprintf("reject real=%v naive=%d", codeOf(err), code)
				}
				if !reflect.DeepEqual(ids, nids) {
					panic(fmt.Sprintf("finalize divergence %v vs %v", ids, nids))
				}
				return true, fmt.Sprintf("-> %s", fmtSlice(ids))
			},
		}
	default: // Collect
		now := g.now + int64(g.rng.Intn(8))
		if bad && g.rng.Intn(2) == 0 {
			now = g.now - 1
		}
		if now > maxNow {
			now = maxNow
		}
		return op{
			line: fmt.Sprintf("Collect(now=%d)", now),
			run: func(h *Heap, n *naiveSim) (bool, string) {
				res, err := h.Collect(now)
				nres, ncode := n.collect(now)
				realRej, naiveRej := err != nil, ncode != 0
				if realRej || naiveRej {
					if realRej != naiveRej {
						panic(fmt.Sprintf("reject mismatch at Collect(%d): real=%v naive=%d", now, codeOf(err), ncode))
					}
					return false, fmt.Sprintf("reject real=%v naive=%d", codeOf(err), ncode)
				}
				g.gcLists(now)
				cmp := normalizeResult(res)
				ncmp := normalizeResult(nres)
				if !reflect.DeepEqual(cmp, ncmp) {
					panic(fmt.Sprintf("collect divergence\nreal %+v\nnaive %+v", cmp, ncmp))
				}
				return true, fmt.Sprintf("-> rec=%s soft=%s weak=%s catch=%s fin=%s used=%d",
					fmtSlice(res.Reclaimed), fmtSlice(res.SoftCleared),
					fmtSlice(res.WeakCleared), fmtSlice(res.CatchAll),
					fmtSlice(res.Finalized), res.Used)
			},
		}
	}
}

func normalizeResult(r CollectResult) CollectResult {
	r.Reclaimed = emptyInts(r.Reclaimed)
	r.SoftCleared = emptyInts(r.SoftCleared)
	r.WeakCleared = emptyInts(r.WeakCleared)
	r.CatchAll = emptyInts(r.CatchAll)
	r.Finalized = emptyInts(r.Finalized)
	return r
}

// gcLists refreshes the generator's id lists after a sweep.
func (g *generator) gcLists(_ int64) {
	alive := map[int64]bool{}
	for id := range g.heap.objs {
		alive[id] = true
	}
	var live, refs []int64
	for _, id := range g.live {
		if alive[id] {
			live = append(live, id)
		}
	}
	for _, id := range g.refs {
		if alive[id] {
			refs = append(refs, id)
		}
	}
	g.live, g.refs = live, refs
}
