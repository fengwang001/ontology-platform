package heap

import (
	"sort"
)

// naive is an independent, deliberately simple reference
// implementation used only by the differential tests. It recomputes
// reachability with plain sets at every phase.
type naive struct {
	C, U, M int64
	used    int64
	nextID  int
	nextQ   int
	objects map[int]*naiveObject
	roots   map[int]bool
	queues  map[int][]int
	finalQ  []int
	maxNow  int64
}

type naiveObject struct {
	size        int64
	ref         bool
	kind        Kind
	target      int
	queue       int
	timestamp   int64
	fields      []int
	finalizable bool
	inFinalQ    bool
}

func newNaive(C, U, M int64) *naive {
	return &naive{
		C: C, U: U, M: M,
		nextID:  1,
		nextQ:   1,
		objects: make(map[int]*naiveObject),
		roots:   make(map[int]bool),
		queues:  make(map[int][]int),
	}
}

func (n *naive) Alloc(s int64, f int, fin bool) (int, error) {
	if s < 1 || s > maxSize || f < 0 || f > maxFields {
		return 0, ErrInvalidParam
	}
	if n.used+s > n.C {
		return 0, ErrHeapFull
	}
	id := n.nextID
	n.nextID++
	n.objects[id] = &naiveObject{size: s, fields: make([]int, f), finalizable: fin}
	n.used += s
	return id, nil
}

func (n *naive) NewRef(k Kind, target, q int, s, now int64) (int, error) {
	if !k.valid() || s < 1 || s > maxSize || now < 0 || now > maxNow {
		return 0, ErrInvalidParam
	}
	if target != 0 {
		if _, ok := n.objects[target]; !ok {
			return 0, ErrNotFound
		}
	}
	if q != 0 {
		if _, ok := n.queues[q]; !ok {
			return 0, ErrNotFound
		}
	}
	if now < n.maxNow {
		return 0, ErrClockRegression
	}
	if n.used+s > n.C {
		return 0, ErrHeapFull
	}
	id := n.nextID
	n.nextID++
	n.objects[id] = &naiveObject{size: s, ref: true, kind: k, target: target, queue: q, timestamp: now}
	n.used += s
	n.maxNow = now
	return id, nil
}

func (n *naive) CreateQueue() int {
	q := n.nextQ
	n.nextQ++
	n.queues[q] = nil
	return q
}

func (n *naive) SetField(o, i, t int) error {
	if i < 0 || i >= maxFields {
		return ErrInvalidParam
	}
	obj, ok := n.objects[o]
	if !ok {
		return ErrNotFound
	}
	if t != 0 {
		if _, ok := n.objects[t]; !ok {
			return ErrNotFound
		}
	}
	if obj.ref {
		return ErrKindMismatch
	}
	if i >= len(obj.fields) {
		return ErrInvalidParam
	}
	obj.fields[i] = t
	return nil
}

func (n *naive) SetRoot(o int) error {
	if _, ok := n.objects[o]; !ok {
		return ErrNotFound
	}
	n.roots[o] = true
	return nil
}

func (n *naive) ClearRoot(o int) error {
	if _, ok := n.objects[o]; !ok {
		return ErrNotFound
	}
	delete(n.roots, o)
	return nil
}

func (n *naive) Get(r int, now int64) (int, error) {
	if now < 0 || now > maxNow {
		return 0, ErrInvalidParam
	}
	obj, ok := n.objects[r]
	if !ok {
		return 0, ErrNotFound
	}
	if !obj.ref {
		return 0, ErrKindMismatch
	}
	if now < n.maxNow {
		return 0, ErrClockRegression
	}
	n.maxNow = now
	if obj.kind == Soft {
		obj.timestamp = now
	}
	return obj.target, nil
}

func (n *naive) Poll(q int) (int, error) {
	items, ok := n.queues[q]
	if !ok {
		return 0, ErrNotFound
	}
	if len(items) == 0 {
		return 0, nil
	}
	n.queues[q] = items[1:]
	return items[0], nil
}

func (n *naive) Finalize(k int) ([]int, error) {
	if k < 0 || k > maxFinalizeN {
		return nil, ErrInvalidParam
	}
	if k > len(n.finalQ) {
		k = len(n.finalQ)
	}
	out := make([]int, 0, k)
	for _, id := range n.finalQ[:k] {
		obj := n.objects[id]
		obj.finalizable = false
		obj.inFinalQ = false
		out = append(out, id)
	}
	n.finalQ = n.finalQ[k:]
	return out, nil
}

// closure adds every object reachable from seeds through ordinary
// object fields to marked. Reference objects are marked but their
// targets are not followed.
func (n *naive) closure(marked map[int]bool, seeds ...int) {
	stack := append([]int(nil), seeds...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == 0 || marked[id] {
			continue
		}
		obj, ok := n.objects[id]
		if !ok {
			continue
		}
		marked[id] = true
		if !obj.ref {
			stack = append(stack, obj.fields...)
		}
	}
}

func (n *naive) sortedRefs(marked map[int]bool, kind Kind, anyKind bool) []int {
	var out []int
	for id, obj := range n.objects {
		if !obj.ref || !marked[id] {
			continue
		}
		if !anyKind && obj.kind != kind {
			continue
		}
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func (n *naive) clear(obj *naiveObject, id int, out *[]int) {
	obj.target = 0
	if obj.queue != 0 {
		n.queues[obj.queue] = append(n.queues[obj.queue], id)
	}
	*out = append(*out, id)
}

func (n *naive) Collect(now int64) (CollectResult, error) {
	if now < 0 || now > maxNow {
		return CollectResult{}, ErrInvalidParam
	}
	if now < n.maxNow {
		return CollectResult{}, ErrClockRegression
	}
	n.maxNow = now

	res := CollectResult{
		Collected:        []int{},
		SoftCleared:      []int{},
		WeakCleared:      []int{},
		FallbackCleared:  []int{},
		FinalizeSelected: []int{},
	}

	// Phase 1: strong marking from roots and the finalization queue.
	marked := make(map[int]bool)
	var seeds []int
	for id := range n.roots {
		seeds = append(seeds, id)
	}
	seeds = append(seeds, n.finalQ...)
	n.closure(marked, seeds...)
	var used0 int64
	for id := range marked {
		used0 += n.objects[id].size
	}

	// Phase 2: soft references; free is fixed at phase start.
	free := n.C - used0
	for _, id := range n.sortedRefs(marked, Soft, false) {
		obj := n.objects[id]
		t := obj.target
		if t == 0 || marked[t] {
			continue
		}
		if softRetain(free, n.U, n.M, now-obj.timestamp) {
			n.closure(marked, t)
		} else {
			n.clear(obj, id, &res.SoftCleared)
		}
	}

	// Phase 3: weak references.
	for _, id := range n.sortedRefs(marked, Weak, false) {
		obj := n.objects[id]
		t := obj.target
		if t == 0 || marked[t] {
			continue
		}
		n.clear(obj, id, &res.WeakCleared)
	}

	// Phase 4: select all finalizable candidates at once, then
	// resurrect them with everything reachable through their fields.
	for id, obj := range n.objects {
		if !obj.ref && !marked[id] && obj.finalizable && !obj.inFinalQ {
			res.FinalizeSelected = append(res.FinalizeSelected, id)
		}
	}
	sort.Ints(res.FinalizeSelected)
	for _, id := range res.FinalizeSelected {
		n.objects[id].inFinalQ = true
		n.finalQ = append(n.finalQ, id)
	}
	n.closure(marked, res.FinalizeSelected...)

	// Phase 5: fallback for any marked reference.
	for _, id := range n.sortedRefs(marked, 0, true) {
		obj := n.objects[id]
		t := obj.target
		if t == 0 || marked[t] {
			continue
		}
		n.clear(obj, id, &res.FallbackCleared)
	}

	// Phase 6: sweep.
	for id, obj := range n.objects {
		if !marked[id] {
			res.Collected = append(res.Collected, id)
			n.used -= obj.size
			delete(n.objects, id)
			delete(n.roots, id)
		}
	}
	sort.Ints(res.Collected)
	res.Used = n.used
	return res, nil
}
