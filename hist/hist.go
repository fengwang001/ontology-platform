// Package hist keeps the multi-node event history, the (TS, Node) total
// order and happens-before reachability; it depends only on package lc.
package hist

import (
	"errors"
	"ontology/lc"
	"slices"
	"sort"
	"sync"
)

// The four rejection classes are distinct sentinels (errors.Is).
var (
	ErrNode        = errors.New("hist: invalid node id")
	ErrNoMessage   = errors.New("hist: message does not exist")
	ErrAlreadyRecv = errors.New("hist: message was already received")
	ErrLimit       = errors.New("hist: event limit exceeded")
)

type Kind uint8

const (
	KLocal Kind = iota
	KSend
	KRecv
)

// Event: ID is the 1-based execution sequence; (TS, Node) is the key; MsgID is 0 for local events.
type Event struct {
	ID, Node, MsgID int
	TS              int64
	Kind            Kind
}
type message struct {
	to, recvID int
	tMsg       int64
	received   bool
}
type History struct {
	mu                                  sync.RWMutex
	n, maxEv, nextID, nextMsg, cmpCount int
	clocks                              []lc.Clock
	order, byID                         []Event // byID slot 0 unused; order kept by (TS, Node)
	chains                              [][]int // per-node event IDs, execution order
	next                                []int   // same-node successor event ID, indexed by ID; 0 = none
	msgs                                map[int]*message
}

func New(n, maxEvents int) (*History, error) {
	if n <= 0 || maxEvents <= 0 {
		return nil, ErrNode
	}
	return &History{n: n, maxEv: maxEvents, clocks: make([]lc.Clock, n), chains: make([][]int, n),
		byID: make([]Event, 1), next: make([]int, 1), msgs: map[int]*message{}}, nil
}
func (h *History) valid(node int) bool { return node >= 1 && node <= h.n }
func (h *History) appendEvent(node int, ts int64, k Kind, msgID int) Event {
	h.nextID++
	ev := Event{ID: h.nextID, Node: node, TS: ts, Kind: k, MsgID: msgID}
	if chain := h.chains[node-1]; len(chain) > 0 {
		h.next[chain[len(chain)-1]] = ev.ID
	}
	h.chains[node-1] = append(h.chains[node-1], ev.ID)
	h.byID = append(h.byID, ev)
	h.next = append(h.next, 0)
	h.cmpCount = 0
	i := sort.Search(len(h.order), func(i int) bool {
		h.cmpCount++
		return !lc.Less(h.order[i].TS, h.order[i].Node, ts, node)
	})
	h.order = slices.Insert(h.order, i, ev)
	return ev
}
func (h *History) Local(node int) (Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.valid(node) {
		return Event{}, ErrNode
	}
	if h.nextID >= h.maxEv {
		return Event{}, ErrLimit
	}
	return h.appendEvent(node, h.clocks[node-1].Tick(), KLocal, 0), nil
}
func (h *History) Send(from, to int) (Event, int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.valid(from) || !h.valid(to) {
		return Event{}, 0, ErrNode
	}
	if h.nextID >= h.maxEv {
		return Event{}, 0, ErrLimit
	}
	ts := h.clocks[from-1].Tick()
	h.nextMsg++
	ev := h.appendEvent(from, ts, KSend, h.nextMsg)
	h.msgs[h.nextMsg] = &message{to: to, tMsg: ts}
	return ev, h.nextMsg, nil
}
func (h *History) Recv(msgID int) (Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.msgs[msgID]
	if !ok {
		return Event{}, ErrNoMessage
	}
	if m.received {
		return Event{}, ErrAlreadyRecv
	}
	if h.nextID >= h.maxEv {
		return Event{}, ErrLimit
	}
	ts := h.clocks[m.to-1].Recv(m.tMsg)
	ev := h.appendEvent(m.to, ts, KRecv, msgID)
	m.received, m.recvID = true, ev.ID
	return ev, nil
}
func (h *History) Order() []Event {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return append([]Event{}, h.order...)
}
func (h *History) closure(a int) []bool {
	seen := make([]bool, len(h.byID))
	if a < 1 || a >= len(h.byID) {
		return seen
	}
	seen[a] = true
	for q := []int{a}; len(q) > 0; q = q[1:] {
		x := q[0]
		if nx := h.next[x]; nx != 0 && !seen[nx] {
			seen[nx], q = true, append(q, nx)
		}
		if e := h.byID[x]; e.Kind == KSend {
			if m := h.msgs[e.MsgID]; m != nil && m.received && !seen[m.recvID] {
				seen[m.recvID], q = true, append(q, m.recvID)
			}
		}
	}
	return seen
}
func (h *History) HappensBefore(a, b int) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if a < 1 || b < 1 || a >= len(h.byID) || b >= len(h.byID) || a == b {
		return false
	}
	return h.closure(a)[b]
}
