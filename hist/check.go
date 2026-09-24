package hist

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"

	"ontology/lc"
)

func batchSort(h *History) []Event {
	out := append([]Event{}, h.byID[1:]...)
	sort.Slice(out, func(i, j int) bool { return lc.Less(out[i].TS, out[i].Node, out[j].TS, out[j].Node) })
	return out
}
func genHist(seed int64, ops int) (*History, error) {
	r := rand.New(rand.NewSource(seed))
	h, _ := New(2+r.Intn(4), ops+10)
	var pend []int
	for i := 0; i < ops; i++ {
		switch r.Intn(3) {
		case 0:
			h.Local(1 + r.Intn(h.n))
		case 1:
			if _, id, e := h.Send(1+r.Intn(h.n), 1+r.Intn(h.n)); e == nil {
				pend = append(pend, id)
			}
		case 2:
			if len(pend) == 0 {
				h.Local(1 + r.Intn(h.n))
				break
			}
			j := r.Intn(len(pend))
			if _, e := h.Recv(pend[j]); e != nil {
				return nil, e
			}
			pend = append(pend[:j], pend[j+1:]...)
		}
		if !reflect.DeepEqual(h.Order(), batchSort(h)) {
			return nil, fmt.Errorf("seed=%d step=%d: order drifted from batch sort", seed, i)
		}
	}
	return h, nil
}
func state(h *History) any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cs := make([]int64, h.n)
	for i := range cs {
		cs[i] = h.clocks[i].L
	}
	ms := make(map[int]message, len(h.msgs))
	for id, m := range h.msgs {
		ms[id] = *m
	}
	return []any{cs, h.nextID, append([]Event{}, h.order...), ms}
}
func (h *History) checkInvariants() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	batch := batchSort(h)
	if !reflect.DeepEqual(batch, h.order) {
		return fmt.Errorf("invariant 1: order != batch re-sort")
	}
	for a := 1; a < len(h.byID); a++ {
		seen := h.closure(a)
		for b := 1; b < len(h.byID); b++ {
			if a != b && seen[b] && !(h.byID[a].TS < h.byID[b].TS) {
				return fmt.Errorf("invariant 2: e%d -> e%d but %d >= %d", a, b, h.byID[a].TS, h.byID[b].TS)
			}
		}
	}
	for node := 1; node <= h.n; node++ {
		var prev int64
		for _, id := range h.chains[node-1] {
			ts := h.byID[id].TS
			if ts <= prev {
				return fmt.Errorf("invariant 3: node %d ts %d <= %d", node, ts, prev)
			}
			prev = ts
		}
	}
	for i := 1; i < len(h.order); i++ {
		if p, c := h.order[i-1], h.order[i]; p.TS == c.TS && p.Node == c.Node {
			return fmt.Errorf("invariant 3: duplicate key (%d,%d)", c.TS, c.Node)
		}
	}
	return nil
}

// SelfCheck verifies invariants 1-3 on the receiver, then invariant 4:
// rejected operations must leave no trace (probed without holding h.mu).
func (h *History) SelfCheck() error {
	if err := h.checkInvariants(); err != nil {
		return err
	}
	before := state(h)
	if _, err := h.Local(0); !errors.Is(err, ErrNode) {
		return fmt.Errorf("invariant 4: local(0) err=%v", err)
	}
	if _, err := h.Recv(-1); !errors.Is(err, ErrNoMessage) {
		return fmt.Errorf("invariant 4: recv(-1) err=%v", err)
	}
	if !reflect.DeepEqual(before, state(h)) {
		return fmt.Errorf("invariant 4: state changed after rejection")
	}
	lim, _ := New(1, 1)
	lim.Local(1)
	beforeLim := state(lim)
	if _, err := lim.Local(1); !errors.Is(err, ErrLimit) {
		return fmt.Errorf("invariant 4: limit err=%v", err)
	}
	if !reflect.DeepEqual(beforeLim, state(lim)) {
		return fmt.Errorf("invariant 4: limit rejection changed state")
	}
	return nil
}
func ceilLog2(x int) int {
	k, p := 0, 1
	for p < x {
		p, k = p<<1, k+1
	}
	return k
}

// CheckInsertBound inserts one mid-order event at several sizes m and
// fails if comparisons exceed 2*ceil(log2(m+1))+2; the numeric counter
// never leaves this package.
func CheckInsertBound() error {
	for _, m := range []int{102, 1002, 9999} {
		h, _ := New(3, m+1)
		for i := 0; i < m/2; i++ {
			h.Local(3)
		}
		for node := 1; h.nextID < m; node = 3 - node {
			h.Local(node)
		}
		ev, _ := h.Local(1)
		if bound := 2*ceilLog2(m+1) + 2; h.cmpCount > bound {
			return fmt.Errorf("m=%d: %d comparisons > bound %d", m, h.cmpCount, bound)
		}
		pos := sort.Search(len(h.order), func(i int) bool { return h.order[i].ID == ev.ID })
		if pos == 0 || pos == len(h.order)-1 {
			return fmt.Errorf("m=%d: event inserted at edge pos=%d", m, pos)
		}
	}
	return nil
}
