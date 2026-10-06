package parking

import "container/heap"

type freeSet struct {
	next map[int]int
	prev map[int]int
	head int
	has  bool
}

func newFreeSet(ids ...int) freeSet {
	set := freeSet{next: make(map[int]int, len(ids)), prev: make(map[int]int, len(ids))}
	for _, id := range ids {
		set.add(id)
	}
	return set
}

func (s *freeSet) add(id int) bool {
	if _, exists := s.next[id]; exists {
		return false
	}
	if !s.has {
		s.head = id
		s.next[id] = id
		s.prev[id] = id
		s.has = true
		return true
	}
	tail := s.prev[s.head]
	s.next[tail] = id
	s.prev[id] = tail
	s.next[id] = s.head
	s.prev[s.head] = id
	if id < s.head {
		s.head = id
	}
	return true
}

func (s *freeSet) remove(id int) bool {
	if _, exists := s.next[id]; !exists {
		return false
	}
	if len(s.next) == 1 {
		delete(s.next, id)
		delete(s.prev, id)
		s.head = 0
		s.has = false
		return true
	}
	previous := s.prev[id]
	next := s.next[id]
	s.next[previous] = next
	s.prev[next] = previous
	if s.head == id {
		s.head = next
	}
	delete(s.next, id)
	delete(s.prev, id)
	return true
}

func (s *freeSet) min() (int, bool) {
	return s.head, s.has
}

type scheduleItem struct {
	spotID  int
	version int
}

type scheduleHeap []scheduleItem

func (h scheduleHeap) Len() int           { return len(h) }
func (h scheduleHeap) Less(i, j int) bool { return h[i].spotID < h[j].spotID }
func (h scheduleHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *scheduleHeap) Push(x any)        { *h = append(*h, x.(scheduleItem)) }
func (h *scheduleHeap) Pop() any {
	old := *h
	tail := len(old) - 1
	item := old[tail]
	*h = old[:tail]
	return item
}

type scheduleEntry struct {
	interval  Interval
	version   int
	available bool
}

type scheduleIndex struct {
	buckets [minutesPerDay]scheduleHeap
	entries map[int]*scheduleEntry
}

func newScheduleIndex() *scheduleIndex {
	return &scheduleIndex{entries: make(map[int]*scheduleEntry)}
}

func intervalActive(interval Interval, minute int) bool {
	minute %= minutesPerDay
	if interval.StartMinute < interval.EndMinute {
		return minute >= interval.StartMinute && minute < interval.EndMinute
	}
	return minute >= interval.StartMinute || minute < interval.EndMinute
}

func (s *scheduleIndex) add(spotID int, interval Interval, now int, available bool) {
	entry := s.entries[spotID]
	if entry == nil {
		entry = &scheduleEntry{}
		s.entries[spotID] = entry
	}
	entry.version++
	entry.interval = interval
	entry.available = available
	item := scheduleItem{spotID: spotID, version: entry.version}
	if interval.StartMinute < interval.EndMinute {
		for minute := interval.StartMinute; minute < interval.EndMinute; minute++ {
			heap.Push(&s.buckets[minute], item)
		}
	} else {
		for minute := interval.StartMinute; minute < minutesPerDay; minute++ {
			heap.Push(&s.buckets[minute], item)
		}
		for minute := 0; minute < interval.EndMinute; minute++ {
			heap.Push(&s.buckets[minute], item)
		}
	}
}

func (s *scheduleIndex) remove(spotID int) {
	if entry := s.entries[spotID]; entry != nil {
		entry.version++
		entry.available = false
		delete(s.entries, spotID)
	}
}

func (s *scheduleIndex) setAvailable(spotID int, available bool) {
	if entry := s.entries[spotID]; entry != nil {
		entry.available = available
	}
}

func (s *scheduleIndex) take(now int) (int, bool) {
	minute := now % minutesPerDay
	bucket := &s.buckets[minute]
	for bucket.Len() > 0 {
		item := (*bucket)[0]
		entry := s.entries[item.spotID]
		valid := entry != nil && entry.version == item.version && entry.available &&
			intervalActive(entry.interval, now)
		if !valid {
			heap.Pop(bucket)
			continue
		}
		heap.Pop(bucket)
		entry.available = false
		return item.spotID, true
	}
	return 0, false
}

func (s *scheduleIndex) peek(now int) (int, bool) {
	minute := now % minutesPerDay
	bucket := &s.buckets[minute]
	for bucket.Len() > 0 {
		item := (*bucket)[0]
		entry := s.entries[item.spotID]
		valid := entry != nil && entry.version == item.version && entry.available &&
			intervalActive(entry.interval, now)
		if !valid {
			heap.Pop(bucket)
			continue
		}
		return item.spotID, true
	}
	return 0, false
}

func (s *scheduleIndex) release(spotID int, now int) {
	entry := s.entries[spotID]
	if entry == nil || !intervalActive(entry.interval, now) {
		return
	}
	entry.available = true
	heap.Push(&s.buckets[now%minutesPerDay], scheduleItem{spotID: spotID, version: entry.version})
}
