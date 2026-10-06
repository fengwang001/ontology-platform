package narledger

// batchHeap 按 (效期, 入库序号) 排序的可删除索引堆。
//
// 库存为零的批次在分出/退回数量变化时通过 fix/remove 维护，
// 因此一次领用扫描到的元素只属于实际可发出的批次集合，
// 选批开销不随该药品历史批次总数增长。
type batchHeap struct {
	drug    string
	entries []*batch
	index   map[string]int
}

func newBatchHeap(drug string) *batchHeap {
	return &batchHeap{drug: drug, index: make(map[string]int)}
}

func (h *batchHeap) Len() int { return len(h.entries) }

func (h *batchHeap) less(i, j int) bool {
	a, b := h.entries[i], h.entries[j]
	if a.expireAt != b.expireAt {
		return a.expireAt < b.expireAt
	}
	return a.seq < b.seq
}

func (h *batchHeap) swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.index[h.entries[i].key()] = i
	h.index[h.entries[j].key()] = j
}

func (h *batchHeap) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(i, p) {
			return
		}
		h.swap(i, p)
		i = p
	}
}

func (h *batchHeap) down(i, n int) {
	for {
		small := i
		l, r := 2*i+1, 2*i+2
		if l < n && h.less(l, small) {
			small = l
		}
		if r < n && h.less(r, small) {
			small = r
		}
		if small == i {
			return
		}
		h.swap(i, small)
		i = small
	}
}

// PushB 加入一个库存为正的批次。
func (h *batchHeap) PushB(b *batch) {
	if _, exists := h.index[b.key()]; exists {
		return
	}
	h.index[b.key()] = len(h.entries)
	h.entries = append(h.entries, b)
	h.up(len(h.entries) - 1)
}

func (h *batchHeap) Peek() *batch {
	if len(h.entries) == 0 {
		return nil
	}
	return h.entries[0]
}

func (h *batchHeap) PopB() *batch {
	n := len(h.entries)
	if n == 0 {
		return nil
	}
	b := h.entries[0]
	h.swap(0, n-1)
	delete(h.index, b.key())
	h.entries = h.entries[:n-1]
	if n > 1 {
		h.down(0, n-1)
	}
	return b
}

func (h *batchHeap) Remove(key string) bool {
	i, ok := h.index[key]
	if !ok {
		return false
	}
	b := h.entries[i]
	n := len(h.entries) - 1
	h.swap(i, n)
	delete(h.index, b.key())
	h.entries = h.entries[:n]
	if i < n {
		h.down(i, n)
		h.up(i)
	}
	return true
}

// contains 仅供测试与复杂度统计使用。
func (h *batchHeap) contains(key string) bool {
	_, ok := h.index[key]
	return ok
}

// scanned 供基准测试统计堆内候选数。
func (h *batchHeap) size() int { return len(h.entries) }
