// Package align implements two-input Chandy-Lamport barrier alignment.
package align

import (
	"errors"
	"maps"
	"ontology/chanbuf"
	"sync"
)

// Rejection reasons, mutually distinct sentinel errors.
var (
	ErrBadElem      = errors.New("align: invalid element")
	ErrBarrierOrder = errors.New("align: barrier out of order")
	ErrBufferFull   = errors.New("align: buffer limit exceeded")
)

type Item = chanbuf.Item // one input element
type Out = chanbuf.Item  // one output-stream element

const ( // element kinds
	Record  = chanbuf.Record
	Barrier = chanbuf.Barrier
)

// state is the mutable core, kept separate so Push can clone-and-swap
// without copying the mutex.
type state struct {
	ch      [2]chanbuf.Chan
	sum     map[string]int64
	snap    map[int64]map[string]int64
	out     []Out
	max     int
	proc    [2]int64
	checked int // buffer elements examined while handling the most recent input element
}

// Align is a two-input summing operator with aligned checkpoints.
type Align struct {
	mu sync.RWMutex
	st state
}

// New returns an operator allowing at most maxBuffered buffered elements.
func New(maxBuffered int) *Align {
	return &Align{st: state{sum: map[string]int64{}, snap: map[int64]map[string]int64{}, max: maxBuffered}}
}

// Push applies one batch atomically: any rejection fails the whole batch
// without touching sum, buffers, snapshots or the output stream.
func (a *Align) Push(items []Item) ([]Out, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.st.clone()
	base := len(w.out)
	for _, it := range items {
		if err := w.arrive(it); err != nil {
			return nil, err
		}
	}
	a.st = *w
	return append([]Out(nil), a.st.out[base:]...), nil
}

// arrive validates one element at arrival time and dispatches it.
func (s *state) arrive(it Item) error {
	s.checked = 0
	if it.Ch != 0 && it.Ch != 1 {
		return ErrBadElem
	}
	c := &s.ch[it.Ch]
	if it.Kind == Record && it.Key == "" || it.Kind != Record && it.Kind != Barrier {
		return ErrBadElem
	}
	if it.Kind == Barrier {
		if it.ID != c.Barriers()+1 {
			return ErrBarrierOrder
		}
		c.NoteBarrier()
	}
	if c.Blocked() {
		if s.ch[0].Len()+s.ch[1].Len() >= s.max {
			return ErrBufferFull
		}
		c.Enqueue(it)
		return nil
	}
	s.process(it)
	return nil
}

// process handles an element as if just arrived on an unblocked channel.
func (s *state) process(it Item) {
	if it.Kind == Record {
		s.sum[it.Key] += it.Val
		s.out = append(s.out, it)
		return
	}
	s.ch[it.Ch].Block()
	s.proc[it.Ch] = it.ID
	if s.proc[0] == it.ID && s.proc[1] == it.ID {
		s.align(it.ID)
	}
}

// align takes snapshot n, forwards barrier n, then unblocks and replays.
func (s *state) align(n int64) {
	s.snap[n] = maps.Clone(s.sum)
	s.out = append(s.out, Out{Ch: -1, Kind: Barrier, ID: n})
	for i := range s.ch {
		s.ch[i].Unblock()
		s.replay(&s.ch[i])
	}
}

// replay drains one channel's buffer in arrival order until it re-blocks.
func (s *state) replay(c *chanbuf.Chan) {
	for !c.Blocked() {
		it, ok := c.Dequeue()
		if !ok {
			return
		}
		s.checked++
		s.process(it)
	}
}

// State returns a copy of the current sum.
func (a *Align) State() map[string]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return maps.Clone(a.st.sum)
}

// Snapshot returns a copy of checkpoint snapshot n.
func (a *Align) Snapshot(n int64) (map[string]int64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s, ok := a.st.snap[n]
	return maps.Clone(s), ok
}

func (s *state) clone() *state {
	w := *s
	w.ch = [2]chanbuf.Chan{s.ch[0].Clone(), s.ch[1].Clone()}
	w.sum = maps.Clone(s.sum)
	w.snap = maps.Clone(s.snap) // stored snapshots are immutable
	w.out = append([]Out(nil), s.out...)
	return &w
}
