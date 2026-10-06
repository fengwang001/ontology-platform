package retention

import "container/heap"

type scheduleItem struct {
	entry    *refEntry
	deadline int64
	version  int64
	index    int
}

type scheduleHeap []*scheduleItem

func (h scheduleHeap) Len() int { return len(h) }

func (h scheduleHeap) Less(i int, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	if h[i].entry.record.At != h[j].entry.record.At {
		return h[i].entry.record.At < h[j].entry.record.At
	}
	if h[i].version != h[j].version {
		return h[i].version < h[j].version
	}
	return h[i].entry.record.Operator < h[j].entry.record.Operator
}

func (h scheduleHeap) Swap(i int, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *scheduleHeap) Push(value any) {
	item := value.(*scheduleItem)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *scheduleHeap) Pop() any {
	last := len(*h) - 1
	item := (*h)[last]
	item.index = -1
	*h = (*h)[:last]
	return item
}

var _ heap.Interface = (*scheduleHeap)(nil)

func (s *Store) scheduleEntry(entry *refEntry) {
	state := entry.state
	deadline := s.deadlineFor(entry, state.current)
	item := &scheduleItem{entry: entry, deadline: deadline, version: state.version, index: -1}
	entry.item = item
	heap.Push(&s.schedule, item)
}

func (s *Store) popCurrentDue(now int64) (*refEntry, *scheduleItem) {
	for len(s.schedule) > 0 {
		item := s.schedule[0]
		heap.Pop(&s.schedule)
		if item.version != item.entry.state.version || item.entry.item != item || !item.entry.active {
			continue
		}
		if item.deadline > now {
			heap.Push(&s.schedule, item)
			return nil, nil
		}
		return item.entry, item
	}
	return nil, nil
}

func (s *Store) temporaryExpiredRoots(now int64) map[ID]int {
	roots := make(map[ID]int)
	var saved []*scheduleItem
	for {
		entry, item := s.popCurrentDue(now)
		if entry == nil {
			break
		}
		entry.item = nil
		saved = append(saved, item)
		state := entry.state
		if entry.record.Old != "" && entry.record.Old != state.current {
			roots[entry.record.Old]++
		}
		if entry.record.New != "" && entry.record.New != state.current {
			roots[entry.record.New]++
		}
	}
	for _, item := range saved {
		item.entry.item = item
		heap.Push(&s.schedule, item)
	}
	return roots
}

func (s *Store) releaseRecordRoot(state *refState, entry *refEntry) {
	if entry.record.Old != "" && entry.record.Old != state.current {
		s.releasePin(state, entry.record.Old)
	}
	if entry.record.New != "" && entry.record.New != state.current {
		s.releasePin(state, entry.record.New)
	}
}

func (s *Store) releasePin(state *refState, commit ID) {
	state.roots[commit]--
	if state.roots[commit] == 0 {
		delete(state.roots, commit)
	}
	s.pins[commit]--
	if s.pins[commit] == 0 {
		delete(s.pins, commit)
	}
}
