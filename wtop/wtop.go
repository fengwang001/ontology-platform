// Package wtop maintains the count-based sliding window of the most recent
// N changes and the per-key score sums inside it. It depends on no other
// package in this module.
package wtop

// Change is one incoming CDC event.
type Change struct {
	Key   string
	Score int64
}

// Window keeps exactly the most recent New(n) changes.
type Window struct {
	n      int
	buf    []Change // ring of capacity n
	size   int      // events currently held (<= n)
	head   int      // index of the oldest event
	sums   map[string]int64
	counts map[string]int64 // occurrences per key; key exists iff count > 0
}

// New creates a window holding at most n changes; n must be >= 1.
func New(n int) *Window {
	return &Window{
		n:      n,
		buf:    make([]Change, n),
		sums:   make(map[string]int64),
		counts: make(map[string]int64),
	}
}

// N reports the window capacity.
func (w *Window) N() int { return w.n }

// Len reports the number of changes currently in the window.
func (w *Window) Len() int { return w.size }

// Add appends c. When the window was already full it returns the evicted
// oldest change with ok=true. sums/counts are updated synchronously, so a
// key exists in the sum table exactly while it has >= 1 change in window.
func (w *Window) Add(c Change) (evicted Change, ok bool) {
	if w.size == w.n {
		old := w.buf[w.head]
		w.sums[old.Key] -= old.Score
		w.counts[old.Key]--
		if w.counts[old.Key] == 0 {
			delete(w.sums, old.Key)
			delete(w.counts, old.Key)
		}
		w.buf[w.head] = c
		w.head = (w.head + 1) % w.n
		evicted, ok = old, true
	} else {
		w.buf[(w.head+w.size)%w.n] = c
		w.size++
	}
	w.sums[c.Key] += c.Score
	w.counts[c.Key]++
	return evicted, ok
}

// Sum returns the summed score of key inside the window. exists is false
// when the key currently has no change in the window (a sum of 0 from a
// key that does exist still returns exists=true).
func (w *Window) Sum(key string) (sum int64, exists bool) {
	sum, exists = w.sums[key]
	return sum, exists
}

// Range visits every existing key and its current sum in arbitrary order.
func (w *Window) Range(fn func(key string, sum int64) bool) {
	for k, s := range w.sums {
		if !fn(k, s) {
			return
		}
	}
}
