package sched

import (
	"errors"
	"fmt"
)

// oracle is a deliberately naive, step-by-step reimplementation of the rules
// written in the specification. It uses slices and linear scans on purpose;
// the scheduler must match its outcomes while remaining O(1) in touched runs.
type oracle struct {
	C, Q  int
	held  int
	queue []int64 // FIFO waiting placeholder ids
	state map[int64]State
	grp   map[string]oGroup
	next  int64
}

type oGroup struct{ placeholder, pending int64 }

func newOracle(C, Q int) *oracle {
	return &oracle{
		C: C, Q: Q,
		state: map[int64]State{},
		grp:   map[string]oGroup{},
		next:  1,
	}
}

func (o *oracle) alloc() {
	for o.held < o.C && len(o.queue) > 0 {
		head := o.queue[0]
		o.queue = o.queue[1:]
		o.state[head] = Running
		o.held++
	}
}

func removeID(q []int64, id int64) []int64 {
	out := q[:0]
	for _, x := range q {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}

func (o *oracle) groupOf(id int64) (string, bool) {
	for name, g := range o.grp {
		if g.placeholder == id || g.pending == id {
			return name, true
		}
	}
	return "", false
}

// vacate mirrors the scheduler: free a slot when held, promote pending to the
// queue tail, then the caller allocates.
func (o *oracle) vacate(id int64, heldSlot bool) {
	if heldSlot {
		o.held--
	}
	name, ok := o.groupOf(id)
	if !ok {
		return
	}
	g := o.grp[name]
	if g.placeholder != id {
		return
	}
	g.placeholder = 0
	if g.pending != 0 {
		prom := g.pending
		g.pending = 0
		g.placeholder = prom
		o.state[prom] = Waiting
		o.queue = append(o.queue, prom)
	}
	if g == (oGroup{}) {
		delete(o.grp, name)
	} else {
		o.grp[name] = g
	}
}

// oStep is one oracle decision: error class, accepted id and human basis.
type oStep struct {
	err   error
	id    int64
	basis string
}

func (o *oracle) submit(name string, cancel, prot bool) oStep {
	g, exists := o.grp[name]
	if name != "" && exists && g.placeholder != 0 {
		if g.pending != 0 {
			o.state[g.pending] = Superseded // supersede before examining new flags
			g.pending = 0
		}
		id := o.next
		o.next++
		o.state[id] = Pending
		ph := g.placeholder
		switch {
		case cancel && o.state[ph] == Waiting:
			o.queue = removeID(o.queue, ph) // protected never shields Waiting
			o.state[ph] = Cancelled
			g.placeholder = id
			o.state[id] = Waiting
			o.queue = append(o.queue, id)
			o.grp[name] = g
			o.alloc()
			return oStep{id: id, basis: "Waiting placeholder cancelled/dequeued, new placeholder tail, net 0"}
		case cancel && o.state[ph] == Running && !prot:
			o.state[ph] = Cancelling // retains the slot
			g.pending = id
			o.grp[name] = g
			return oStep{id: id, basis: "Running non-protected -> Cancelling; new run Pending"}
		default:
			g.pending = id // Running+protected or already Cancelling
			o.grp[name] = g
			return oStep{id: id, basis: "placeholder kept (protected/Cancelling); new run Pending"}
		}
	}

	// No placeholder: derive L1 by simulating enqueue + allocation.
	L0 := len(o.queue)
	simQ, simHeld := L0+1, o.held
	for simHeld < o.C && simQ > 0 {
		simQ--
		simHeld++
	}
	if L1 := simQ; L1 > L0 && L1 > o.Q {
		return oStep{err: ErrQueueFull, basis: fmt.Sprintf("net growth rejects: L0=%d L1=%d Q=%d", L0, L1, o.Q)}
	}

	id := o.next
	o.next++
	o.state[id] = Waiting
	if name != "" {
		g.placeholder = id
		o.grp[name] = g
	}
	o.queue = append(o.queue, id)
	o.alloc()
	return oStep{id: id, basis: fmt.Sprintf("new placeholder enqueued; q=%v held=%d after allocation", o.queue, o.held)}
}

func (o *oracle) finish(id int64, ok bool) oStep {
	st, ok2 := o.state[id]
	if !ok2 {
		return oStep{err: ErrNotFound, basis: "id does not exist"}
	}
	switch st {
	case Running:
		if ok {
			o.state[id] = Succeeded
		} else {
			o.state[id] = Failed
		}
		o.vacate(id, true)
		o.alloc()
		return oStep{basis: "Running terminal; release, promote tail, allocate"}
	case Cancelling:
		o.state[id] = Cancelled // cancellation wins; ok discarded
		o.vacate(id, true)
		o.alloc()
		return oStep{basis: "late Finish on Cancelling records Cancelled"}
	default:
		return oStep{err: ErrConflict, basis: fmt.Sprintf("Finish illegal in %s", st)}
	}
}

func (o *oracle) ack(id int64) oStep {
	st, ok2 := o.state[id]
	if !ok2 {
		return oStep{err: ErrNotFound, basis: "id does not exist"}
	}
	if st != Cancelling {
		return oStep{err: ErrConflict, basis: fmt.Sprintf("AckCancel illegal in %s", st)}
	}
	o.state[id] = Cancelled
	o.vacate(id, true)
	o.alloc()
	return oStep{basis: "Cancelling acknowledged; release, promote, allocate"}
}

func (o *oracle) cancel(id int64) oStep {
	st, ok2 := o.state[id]
	if !ok2 {
		return oStep{err: ErrNotFound, basis: "id does not exist"}
	}
	switch st {
	case Pending:
		o.state[id] = Cancelled
		if name, ok := o.groupOf(id); ok {
			g := o.grp[name]
			if g.pending == id {
				g.pending = 0
				if g == (oGroup{}) {
					delete(o.grp, name)
				} else {
					o.grp[name] = g
				}
			}
		}
		return oStep{basis: "Pending -> Cancelled; no slot/queue effect"}
	case Waiting:
		o.queue = removeID(o.queue, id)
		o.state[id] = Cancelled
		o.vacate(id, false)
		o.alloc()
		return oStep{basis: "Waiting -> Cancelled and removed O(1); then allocate"}
	case Running:
		o.state[id] = Cancelling // explicit cancel bypasses protected
		return oStep{basis: "Running -> Cancelling, slot retained"}
	default:
		return oStep{err: ErrConflict, basis: fmt.Sprintf("Cancel illegal in %s", st)}
	}
}

func classOf(err error) string {
	switch {
	case err == nil:
		return "OK"
	case errors.Is(err, ErrInvalidArgument):
		return "ErrInvalidArgument"
	case errors.Is(err, ErrNotFound):
		return "ErrNotFound"
	case errors.Is(err, ErrConflict):
		return "ErrConflict"
	case errors.Is(err, ErrQueueFull):
		return "ErrQueueFull"
	default:
		return err.Error()
	}
}

func sameClass(a, b error) bool { return classOf(a) == classOf(b) }
