// Package trace keeps the multi-node event history.
package trace

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/hlc"
)

// Decidable sentinel errors, each a distinct failure class.
var ErrParam = errors.New("trace: invalid parameter")
var ErrNoMsg = errors.New("trace: unknown message id")
var ErrNotYours = errors.New("trace: message addressed to another node")
var ErrReceived = errors.New("trace: message already received")

type Event struct {
	TS         hlc.T
	PT         int64
	Prev, Send *Event
}

type msg struct {
	to    int
	ts    hlc.T
	send  *Event
	taken bool
}

type Trace struct {
	mu        sync.Mutex
	clocks    []*hlc.Clock
	maxOffset int64
	msgs      map[int64]*msg
	hist      [][]*Event
	lastCmp   int // comparisons done by the most recent CountUpTo
}

func New(n int, maxOffset, maxC int64) (*Trace, error) {
	if n <= 0 || maxC <= 0 || maxOffset < 0 {
		return nil, ErrParam
	}
	t := &Trace{maxOffset: maxOffset, msgs: map[int64]*msg{}, hist: make([][]*Event, n)}
	for range n {
		t.clocks = append(t.clocks, hlc.New(maxC))
	}
	return t, nil
}

// append records an event; callers hold t.mu and call it only after all checks passed.
func (t *Trace) append(node int, ts hlc.T, pt int64, send *Event) *Event {
	ev := &Event{TS: ts, PT: pt, Send: send}
	if h := t.hist[node]; len(h) > 0 {
		ev.Prev = h[len(h)-1]
	}
	t.hist[node] = append(t.hist[node], ev)
	return ev
}

func (t *Trace) Local(node int, pt int64) (hlc.T, error) {
	if node < 0 || node >= len(t.clocks) || pt < 0 {
		return hlc.T{}, ErrParam
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ts, err := t.clocks[node].Tick(pt)
	if err != nil {
		return hlc.T{}, err
	}
	t.append(node, ts, pt, nil)
	return ts, nil
}

func (t *Trace) Send(from, to int, pt int64) (hlc.T, int64, error) {
	if from < 0 || from >= len(t.clocks) || to < 0 || to >= len(t.clocks) || pt < 0 {
		return hlc.T{}, 0, ErrParam
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ts, err := t.clocks[from].Tick(pt)
	if err != nil {
		return hlc.T{}, 0, err
	}
	id := int64(len(t.msgs))
	t.msgs[id] = &msg{to: to, ts: ts, send: t.append(from, ts, pt, nil)}
	return ts, id, nil
}

func (t *Trace) Recv(to int, id int64, pt int64) (hlc.T, error) {
	if to < 0 || to >= len(t.clocks) || pt < 0 {
		return hlc.T{}, ErrParam
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.msgs[id]
	switch {
	case !ok:
		return hlc.T{}, ErrNoMsg
	case m.to != to:
		return hlc.T{}, ErrNotYours
	case m.taken:
		return hlc.T{}, ErrReceived
	}
	ts, err := t.clocks[to].Receive(pt, m.ts, t.maxOffset)
	if err != nil {
		return hlc.T{}, err // clock and message both untouched
	}
	m.taken = true
	t.append(to, ts, pt, m.send)
	return ts, nil
}

func (t *Trace) CountUpTo(node int, ts hlc.T) (int, error) {
	if node < 0 || node >= len(t.clocks) {
		return 0, ErrParam
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	cmp := 0
	n := sort.Search(len(t.hist[node]), func(i int) bool { cmp++; return !t.hist[node][i].TS.LE(ts) })
	t.lastCmp = cmp
	return n, nil
}

func (t *Trace) Verify() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for node, h := range t.hist {
		for i, ev := range h {
			bad := ev.TS.L != causalMaxPT(ev, map[*Event]bool{}) ||
				i > 0 && !(h[i-1].TS.LE(ev.TS) && h[i-1].TS != ev.TS) ||
				ev.Send != nil && !(ev.Send.TS.LE(ev.TS) && ev.Send.TS != ev.TS)
			if bad {
				return fmt.Errorf("node %d event %d: invariant 1/2/3 violated", node, i)
			}
		}
	}
	return nil
}

// causalMaxPT is the naive reference: max pt over the causal past, brute-forced via Prev/Send links.
func causalMaxPT(ev *Event, seen map[*Event]bool) int64 {
	if ev == nil || seen[ev] {
		return -1
	}
	seen[ev] = true
	return max(ev.PT, causalMaxPT(ev.Prev, seen), causalMaxPT(ev.Send, seen))
}
