package sampler

import "math"

// entry is one candidate currently retained in the reservoir.
type entry struct {
	id     string
	weight float64
	key    float64
	seq    int // arrival order, 0-based among accepted elements
}

// worse reports whether a ranks strictly below b in the reservoir. The
// reservoir keeps the k largest keys; on equal keys the earlier arrival wins,
// so a later arrival ranks worse.
func worse(a, b entry) bool {
	if a.key != b.key {
		return a.key < b.key
	}
	return a.seq > b.seq
}

// minHeap puts the currently worst retained entry at index 0.
type minHeap []entry

func (h minHeap) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !worse(h[i], h[parent]) {
			break
		}
		h[i], h[parent] = h[parent], h[i]
		i = parent
	}
}

func (h minHeap) siftDown(i, n int) {
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		smallest := left
		if right := left + 1; right < n && worse(h[right], h[left]) {
			smallest = right
		}
		if !worse(h[smallest], h[i]) {
			return
		}
		h[i], h[smallest] = h[smallest], h[i]
		i = smallest
	}
}

func (h *minHeap) push(e entry) {
	*h = append(*h, e)
	h.siftUp(len(*h) - 1)
}

func (h *minHeap) replaceRoot(e entry) {
	(*h)[0] = e
	h.siftDown(0, len(*h))
}

// key computes u^(1/w): the A-Res priority of an element. Larger is better.
func key(u, w float64) float64 {
	return math.Pow(u, 1.0/w)
}
