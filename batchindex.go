package ontology

import "container/heap"

type productKey struct {
	supplier ID
	product  ID
}

// batchEntry 是 FIFO 堆中的条目。排序键 (arrival, supplier, number) 不可变，
// 因此条目一旦入堆便无需调整位置；批次剩余量的变化只反映在批次对象上。
type batchEntry struct {
	arrival  int64
	supplier ID
	number   BatchID
	// inHeap 表示条目当前是否在堆中。任一批次任一时刻最多一个活动条目：
	// 冲销把数量退回批次时，仅当条目不在堆中且批次未到期才重新入堆。
	inHeap bool
}

type batchHeap []*batchEntry

func (h batchHeap) Len() int { return len(h) }

func (h batchHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.arrival != b.arrival {
		return a.arrival < b.arrival
	}
	if a.supplier != b.supplier {
		return a.supplier < b.supplier
	}
	return a.number < b.number
}

func (h batchHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *batchHeap) Push(x any) { *h = append(*h, x.(*batchEntry)) }

func (h *batchHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}

// batchIndex 为每种商品维护一个按 FIFO 次序排序的批次堆。
// 批次对象本身集中存于 system.batches，索引只持有条目引用。
type batchIndex struct {
	heaps   map[ID]*batchHeap
	entries map[productKey]map[BatchID]*batchEntry
}

func newBatchIndex() *batchIndex {
	return &batchIndex{
		heaps:   map[ID]*batchHeap{},
		entries: map[productKey]map[BatchID]*batchEntry{},
	}
}

func (bi *batchIndex) entryMap(key productKey) map[BatchID]*batchEntry {
	m := bi.entries[key]
	if m == nil {
		m = map[BatchID]*batchEntry{}
		bi.entries[key] = m
	}
	return m
}

// push 把新到货批次放入对应商品的堆。
func (bi *batchIndex) push(supplier, product ID, number BatchID, arrival int64) {
	key := productKey{supplier, product}
	e := &batchEntry{arrival: arrival, supplier: supplier, number: number, inHeap: true}
	bi.entryMap(key)[number] = e
	h := bi.heaps[product]
	if h == nil {
		h = &batchHeap{}
		bi.heaps[product] = h
	}
	heap.Push(h, e)
}

// reactivate 在冲销把数量退回批次后，按需把批次重新放回堆。
// expired 必须是“该批次在冲销时刻 now 的到期状态”（到期是时刻的纯函数）：
// 批次在过去某刻到期、但冲销时刻仍未到期（例如 duration 覆盖 now）时，
// 它仍可能可领用，需要重新入堆。
func (bi *batchIndex) reactivate(supplier, product ID, number BatchID, arrival int64, expired bool) {
	key := productKey{supplier, product}
	m := bi.entryMap(key)
	if e, ok := m[number]; ok && e.inHeap {
		return
	}
	// 条目此前已被摘除（或耗尽后被冲销回库）；以相同的不可变排序键新建并入堆。
	e := &batchEntry{arrival: arrival, supplier: supplier, number: number, inHeap: true}
	m[number] = e
	h := bi.heaps[product]
	heap.Push(h, e)
}

// scanAlloc 是一次领用在堆的有序快照上做的纯计算结果。
type scanAlloc struct {
	supplier ID
	number   BatchID
	qty      int64
	entry    *batchEntry
}

// scan 在不修改堆的前提下，按 FIFO 次序在时刻 now 为 quantity 件货物
// 计算可行分配。它只读取堆的有序副本并摘除其中的无效条目（不存在、
// 剩余为零、已到期），对真实堆没有任何副作用，因此被拒绝的领用不会
// 改变索引：例如某次时钟未被接受的未来时刻扫描不会把当前时钟仍有效
// 的条目误删。
//
// 返回值：
//   - allocs 为按 FIFO 次序的逐批分配；
//   - survivors 为扫描后仍可保留在堆中的条目（在 now 有效且未被本次
//     领用耗尽，保持 FIFO 次序）；
//   - involved 为实际被拆分到的供应商集合；
//   - satisfied 表示可用量是否足够。
func (bi *batchIndex) scan(product ID, now, quantity int64,
	stateOf func(supplier ID, number BatchID) (remaining int64, exists, expired bool),
) (allocs []scanAlloc, survivors, dropped []*batchEntry, involved map[ID]bool, satisfied bool) {
	h := bi.heaps[product]
	involved = map[ID]bool{}
	if h == nil || h.Len() == 0 {
		return nil, nil, nil, involved, quantity == 0
	}
	// 堆拷贝：堆顺序即 FIFO 顺序；弹出拷贝不影响真实堆。
	snap := append(batchHeap(nil), (*h)...)
	need := quantity
	for len(snap) > 0 {
		e := snap[0]
		snap = snap[1:]
		rem, exists, expired := stateOf(e.supplier, e.number)
		if !exists || rem <= 0 || expired {
			// 无效条目在本次扫描中被丢弃；真实堆仅在领用成功提交时才重建。
			dropped = append(dropped, e)
			continue
		}
		if need <= 0 {
			survivors = append(survivors, e)
			continue
		}
		take := rem
		if take > need {
			take = need
		}
		allocs = append(allocs, scanAlloc{
			supplier: e.supplier, number: e.number, qty: take, entry: e,
		})
		involved[e.supplier] = true
		need -= take
		if rem-take > 0 {
			survivors = append(survivors, e)
		} else {
			// 本次领用恰好耗尽：离开堆。
			dropped = append(dropped, e)
		}
	}
	return allocs, survivors, dropped, involved, need <= 0
}

// commitScan 用领用扫描得到的幸存者有序列表重建某商品的堆。
// 幸存者已按 FIFO 次序排列且条目排序键不可变，直接按序填充即构成合法堆。
// dropped 中被耗尽/到期的条目标记为离堆，使 entries 索引与真实堆成员一致，
// 避免冲销重激活时依据陈旧的 inHeap 标志漏入堆。
func (bi *batchIndex) commitScan(product ID, survivors, dropped []*batchEntry) {
	h := batchHeap(append([]*batchEntry(nil), survivors...))
	heap.Init(&h)
	for _, e := range h {
		e.inHeap = true
	}
	bi.heaps[product] = &h
	for _, e := range dropped {
		e.inHeap = false
		key := productKey{e.supplier, product}
		if m := bi.entries[key]; m != nil {
			if cur, ok := m[e.number]; ok && cur == e {
				delete(m, e.number)
			}
		}
	}
}

// heapLen 返回某商品堆的当前条目数，供测试验证有界性。
func (bi *batchIndex) heapLen(product ID) int {
	if h := bi.heaps[product]; h != nil {
		return h.Len()
	}
	return 0
}

var _ heap.Interface = (*batchHeap)(nil)
