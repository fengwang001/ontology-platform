package ftl

type indexedHeap struct {
	ids     []int
	pos     []int
	less    func(a, b int) bool
	metrics *heapMetrics
}

type heapMetrics struct {
	Comparisons int
	Swaps       int
}

type bucketSet struct {
	buckets []*freeBucket
	first   int
	size    int
	used    []uint64
}

type freeBucket struct {
	bits []uint64
}

func newBucketSet(blockCount, maxKey int) bucketSet {
	return bucketSet{
		buckets: make([]*freeBucket, maxKey+1),
		first:   maxKey + 1,
		used:    make([]uint64, maxKey/64+1),
	}
}

func (s *bucketSet) add(key, id int) {
	if s.buckets[key] == nil {
		s.buckets[key] = &freeBucket{}
	}
	s.buckets[key].set(id)
	s.used[key/64] |= 1 << uint(key%64)
	s.size++
	if key < s.first {
		s.first = key
	}
}

func (s *bucketSet) remove(key, id int) {
	current := s.buckets[key]
	if current == nil {
		return
	}
	if !current.contains(id) {
		return
	}
	current.clear(id)
	s.size--
	if current.empty() {
		s.used[key/64] &^= 1 << uint(key%64)
	}
	if current.empty() && key == s.first {
		s.advanceFirst()
	}
}

func (s *bucketSet) len() int {
	return s.size
}

func (s *bucketSet) pop() (int, bool) {
	if s.first >= len(s.buckets) || s.buckets[s.first] == nil || s.buckets[s.first].empty() {
		s.advanceFirst()
	}
	if s.first >= len(s.buckets) {
		return 0, false
	}
	current := s.buckets[s.first]
	selected := current.smallest()
	current.clear(selected)
	s.size--
	if current.empty() {
		s.advanceFirst()
	}
	return selected, true
}

func (s *bucketSet) advanceFirst() {
	for s.first/64 < len(s.used) && s.used[s.first/64]&(^uint64(0)<<uint(s.first%64)) == 0 {
		s.first = (s.first/64 + 1) * 64
	}
	for s.first < len(s.buckets) && (s.buckets[s.first] == nil || s.buckets[s.first].empty()) {
		s.first++
	}
}

func (b *freeBucket) ensure(id int) {
	required := id/64 + 1
	for len(b.bits) < required {
		b.bits = append(b.bits, 0)
	}
}

func (b *freeBucket) set(id int) {
	b.ensure(id)
	b.bits[id/64] |= 1 << uint(id%64)
}

func (b *freeBucket) clear(id int) {
	if id/64 >= len(b.bits) {
		return
	}
	b.bits[id/64] &^= 1 << uint(id%64)
}

func (b *freeBucket) contains(id int) bool {
	return id/64 < len(b.bits) && b.bits[id/64]&(1<<uint(id%64)) != 0
}

func (b *freeBucket) empty() bool {
	for _, word := range b.bits {
		if word != 0 {
			return false
		}
	}
	return true
}

func (b *freeBucket) smallest() int {
	for wordIndex, word := range b.bits {
		if word != 0 {
			return wordIndex*64 + trailingZeros64(word)
		}
	}
	return -1
}

func trailingZeros64(x uint64) int {
	bit := 0
	for x&1 == 0 {
		x >>= 1
		bit++
	}
	return bit
}

func newIndexedHeap(blockCount int, less func(a, b int) bool) indexedHeap {
	pos := make([]int, blockCount)
	for i := range pos {
		pos[i] = -1
	}
	return indexedHeap{pos: pos, less: less}
}

func (h *indexedHeap) len() int {
	return len(h.ids)
}

func (h *indexedHeap) contains(id int) bool {
	return h.pos[id] >= 0
}

func (h *indexedHeap) peek() (int, bool) {
	if len(h.ids) == 0 {
		return 0, false
	}
	return h.ids[0], true
}

func (h *indexedHeap) push(id int) {
	if h.pos[id] >= 0 {
		return
	}
	h.pos[id] = len(h.ids)
	h.ids = append(h.ids, id)
	h.up(h.pos[id])
}

func (h *indexedHeap) pop() (int, bool) {
	if len(h.ids) == 0 {
		return 0, false
	}
	id := h.ids[0]
	last := len(h.ids) - 1
	h.swap(0, last)
	h.ids = h.ids[:last]
	h.pos[id] = -1
	if len(h.ids) > 0 {
		h.down(0)
	}
	return id, true
}

func (h *indexedHeap) remove(id int) bool {
	index := h.pos[id]
	if index < 0 {
		return false
	}
	last := len(h.ids) - 1
	if index != last {
		h.swap(index, last)
	}
	h.ids = h.ids[:last]
	h.pos[id] = -1
	if index < len(h.ids) {
		h.up(index)
		h.down(index)
	}
	return true
}

func (h *indexedHeap) fix(id int) {
	index := h.pos[id]
	if index >= 0 {
		h.up(index)
		h.down(index)
	}
}

func (h *indexedHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.lessAt(i, parent) {
			return
		}
		h.swap(i, parent)
		i = parent
	}
}

func (h *indexedHeap) down(i int) {
	for {
		left := i*2 + 1
		right := left + 1
		smallest := i
		if left < len(h.ids) && h.lessAt(left, smallest) {
			smallest = left
		}
		if right < len(h.ids) && h.lessAt(right, smallest) {
			smallest = right
		}
		if smallest == i {
			return
		}
		h.swap(i, smallest)
		i = smallest
	}
}

func (h *indexedHeap) swap(i, j int) {
	if h.metrics != nil {
		h.metrics.Swaps++
	}
	h.ids[i], h.ids[j] = h.ids[j], h.ids[i]
	h.pos[h.ids[i]] = i
	h.pos[h.ids[j]] = j
}

func (h *indexedHeap) lessAt(i, j int) bool {
	if h.metrics != nil {
		h.metrics.Comparisons++
	}
	return h.less(h.ids[i], h.ids[j])
}
