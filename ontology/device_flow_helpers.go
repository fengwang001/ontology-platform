package ontology

import (
	"container/heap"
	"sort"
)

const maxNowValue = int64(1_000_000_000_000_000)

func validConfig(cfg Config) bool {
	if cfg.Gen == nil {
		return false
	}
	if !between(cfg.E, 1, 1_000_000_000) || !between(cfg.I0, 1, 1_000_000) || !between(cfg.D, 1, 1_000_000) {
		return false
	}
	if !between(cfg.Imax, 1, 1_000_000_000) || cfg.I0 > cfg.Imax {
		return false
	}
	return between(cfg.H, 1, 1_000_000_000) &&
		between(cfg.Cmax, 1, 1_000_000) &&
		between(cfg.Z, 1, 1_000_000)
}

func between(value, low, high int64) bool {
	return value >= low && value <= high
}

func validNow(now int64) bool {
	return now >= 0 && now <= maxNowValue
}

func (s *Service) compactEvents(state *clientState) {
	if state.eventStart == 0 {
		return
	}
	events := state.events[state.eventStart:]
	copy(state.events, events)
	state.events = state.events[:len(events)]
	state.eventStart = 0
}

func advanceEvents(state *clientState, penaltyWindow, now int64) {
	for state.eventStart < len(state.events) && state.events[state.eventStart]+penaltyWindow <= now {
		state.eventStart++
	}
}

func eventWindow(events []int64, penaltyWindow, now, threshold int64) (int64, int64) {
	first := sort.Search(len(events), func(index int) bool {
		return events[index]+penaltyWindow > now
	})
	events = events[first:]
	count := int64(len(events))
	var u int64
	if count >= threshold {
		u = events[count-threshold] + penaltyWindow
	}
	return count, u
}

func (s *Service) baseInterval(eventCount int64) int64 {
	remaining := s.cfg.Imax - s.cfg.I0
	if remaining <= 0 {
		return s.cfg.Imax
	}
	if eventCount >= (remaining+s.cfg.D-1)/s.cfg.D {
		return s.cfg.Imax
	}
	return s.cfg.I0 + s.cfg.D*eventCount
}

func (s *Service) reclaimActive(state *clientState, now int64) {
	for len(state.active) > 0 && state.active[0].expiresAt <= now {
		record := heap.Pop(activeHeap{state}).(*grant)
		record.active = false
		record.heapIndex = -1
		state.activeSize--
		s.expiryPops++
	}
}

func removeActive(state *clientState, record *grant) {
	if !record.active || record.heapIndex < 0 {
		return
	}
	heap.Remove(activeHeap{state}, record.heapIndex)
	record.active = false
	record.heapIndex = -1
	state.activeSize--
}

type activeHeap struct {
	state *clientState
}

func (h activeHeap) Len() int {
	return len(h.state.active)
}

func (h activeHeap) Less(i, j int) bool {
	left := h.state.active[i]
	right := h.state.active[j]
	if left.expiresAt != right.expiresAt {
		return left.expiresAt < right.expiresAt
	}
	return left.id < right.id
}

func (h activeHeap) Swap(i, j int) {
	h.state.active[i], h.state.active[j] = h.state.active[j], h.state.active[i]
	h.state.active[i].heapIndex = i
	h.state.active[j].heapIndex = j
}

func (h activeHeap) Push(value any) {
	record := value.(*grant)
	record.heapIndex = len(h.state.active)
	h.state.active = append(h.state.active, record)
}

func (h activeHeap) Pop() any {
	last := len(h.state.active) - 1
	record := h.state.active[last]
	record.heapIndex = -1
	h.state.active = h.state.active[:last]
	return record
}
