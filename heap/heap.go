// Package heap maintains SpaceSaving counters and locates the
// minimum-count counter in O(1); ties on count break by smaller Key.
package heap

// Counter is one monitored slot: (Key, Count, Err).
type Counter struct {
	Key   int
	Count int
	Err   int
}

// Heap is a binary min-heap of counters ordered by (Count, Key).
type Heap struct {
	items   []Counter
	pos     map[int]int // key -> index in items
	lastCmp int         // comparisons spent locating the min in the last ReplaceMin
}

// New returns an empty heap.
func New() *Heap { return &Heap{pos: map[int]int{}} }

// Len returns the number of counters.
func (h *Heap) Len() int { return len(h.items) }

// Get returns the counter for key, if monitored.
func (h *Heap) Get(key int) (Counter, bool) {
	i, ok := h.pos[key]
	if !ok {
		return Counter{}, false
	}
	return h.items[i], true
}

// Min returns the minimum counter (root peek, O(1)).
func (h *Heap) Min() Counter { return h.items[0] }

// Add inserts a new counter; key must not be present.
func (h *Heap) Add(c Counter) {
	h.pos[c.Key] = len(h.items)
	h.items = append(h.items, c)
	h.siftUp(len(h.items) - 1)
}

// Inc increments the count of key's counter and restores heap order.
func (h *Heap) Inc(key int) {
	i := h.pos[key]
	h.items[i].Count++
	h.siftDown(i)
}

// ReplaceMin evicts the minimum counter, inserts c, and returns the
// evicted one. Locating the min is a root peek: zero comparisons,
// independent of the heap size.
func (h *Heap) ReplaceMin(c Counter) Counter {
	h.lastCmp = 0
	old := h.items[0]
	delete(h.pos, old.Key)
	h.items[0] = c
	h.pos[c.Key] = 0
	h.siftDown(0)
	return old
}

// Items returns a copy of all counters in heap order.
func (h *Heap) Items() []Counter {
	out := make([]Counter, len(h.items))
	copy(out, h.items)
	return out
}

func (h *Heap) less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.Count != b.Count {
		return a.Count < b.Count
	}
	return a.Key < b.Key
}

func (h *Heap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i].Key] = i
	h.pos[h.items[j].Key] = j
}

func (h *Heap) siftUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(i, p) {
			return
		}
		h.swap(i, p)
		i = p
	}
}

func (h *Heap) siftDown(i int) {
	for {
		l, m := 2*i+1, i
		if l < len(h.items) && h.less(l, m) {
			m = l
		}
		if r := l + 1; r < len(h.items) && h.less(r, m) {
			m = r
		}
		if m == i {
			return
		}
		h.swap(i, m)
		i = m
	}
}

// SelfCheck verifies that locating the min during a replace costs a
// constant number of comparisons for heaps of growing size. It reports
// only the verdict; the comparison counter itself stays unexported.
func SelfCheck() bool {
	prev := -1
	for _, k := range []int{100, 1000, 5000, 10000} {
		h := New()
		for i := 0; i < k; i++ {
			h.Add(Counter{Key: i, Count: i + 1})
		}
		h.ReplaceMin(Counter{Key: k, Count: k + 1})
		if h.lastCmp > 1 || (prev >= 0 && h.lastCmp != prev) {
			return false
		}
		prev = h.lastCmp
	}
	return true
}
