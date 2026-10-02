package refheap

// Operations on the naive model, mirroring every validation rule.

func (n *naiveSim) alloc(size int64, fields int, fin bool) (int64, ErrorCode) {
	if size < 1 || size > maxSize || fields < 0 || fields > maxFields {
		return 0, ErrInvalidArg
	}
	if n.used+size > n.c {
		return 0, ErrHeapFull
	}
	n.nextID++
	n.objs[n.nextID] = &nObj{
		size:        size,
		fields:      make([]int64, fields),
		finalizable: fin,
	}
	n.used += size
	return n.nextID, 0
}

func (n *naiveSim) createQueue() int64 {
	n.nextQ++
	n.queues[n.nextQ] = nil
	return n.nextQ
}

func (n *naiveSim) newRef(kind RefKind, target, queue, size, now int64) (int64, ErrorCode) {
	if kind < Soft || kind > Phantom || size < 1 || size > maxSize || !validNow(now) {
		return 0, ErrInvalidArg
	}
	if target != 0 {
		if _, ok := n.objs[target]; !ok {
			return 0, ErrNotFound
		}
	}
	if queue != 0 {
		if _, ok := n.queues[queue]; !ok {
			return 0, ErrNotFound
		}
	}
	if now < n.now {
		return 0, ErrClockBackward
	}
	if n.used+size > n.c {
		return 0, ErrHeapFull
	}
	n.now = now
	n.nextID++
	n.objs[n.nextID] = &nObj{
		size:   size,
		isRef:  true,
		kind:   kind,
		target: target,
		queue:  queue,
		ts:     now,
	}
	n.used += size
	return n.nextID, 0
}

func (n *naiveSim) setField(o int64, i int, t int64) ErrorCode {
	if i < 0 || i >= maxFields {
		return ErrInvalidArg
	}
	obj, ok := n.objs[o]
	if !ok {
		return ErrNotFound
	}
	if obj.isRef {
		return ErrWrongKind
	}
	if i >= len(obj.fields) {
		return ErrInvalidArg
	}
	if t != 0 {
		if _, ok := n.objs[t]; !ok {
			return ErrNotFound
		}
	}
	obj.fields[i] = t
	return 0
}

func (n *naiveSim) setRoot(o int64) ErrorCode {
	if _, ok := n.objs[o]; !ok {
		return ErrNotFound
	}
	n.roots[o] = true
	return 0
}

func (n *naiveSim) clearRoot(o int64) ErrorCode {
	if _, ok := n.objs[o]; !ok {
		return ErrNotFound
	}
	delete(n.roots, o)
	return 0
}

func (n *naiveSim) get(r, now int64) (int64, ErrorCode) {
	if !validNow(now) {
		return 0, ErrInvalidArg
	}
	obj, ok := n.objs[r]
	if !ok {
		return 0, ErrNotFound
	}
	if !obj.isRef {
		return 0, ErrWrongKind
	}
	if now < n.now {
		return 0, ErrClockBackward
	}
	n.now = now
	if obj.kind == Soft {
		obj.ts = now
	}
	return obj.target, 0
}

func (n *naiveSim) poll(q int64) (int64, ErrorCode) {
	qq, ok := n.queues[q]
	if !ok {
		return 0, ErrNotFound
	}
	if len(qq) == 0 {
		return 0, 0
	}
	id := qq[0]
	n.queues[q] = qq[1:]
	return id, 0
}

func (n *naiveSim) finalize(k int) ([]int64, ErrorCode) {
	if k < 0 || k > maxFinalN {
		return nil, ErrInvalidArg
	}
	out := []int64{}
	if k == 0 || len(n.finQ) == 0 {
		return out, 0
	}
	if k > len(n.finQ) {
		k = len(n.finQ)
	}
	out = append(out, n.finQ[:k]...)
	n.finQ = n.finQ[k:]
	for _, id := range out {
		n.objs[id].finalizable = false
		delete(n.inFin, id)
	}
	return out, 0
}
