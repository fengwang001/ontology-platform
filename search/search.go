// Package search 实现按排序键 (sortVal, 段号, 段内序号) 的翻页检索。
// Searcher 无状态，每次检索在 segstore.Store 的锁内以 k 路归并完成。
package search

import (
	"container/heap"
	"sort"

	"ontology/pit"
	"ontology/segstore"
)

// Key 是文档的排序键，按字典序升序。
type Key struct {
	SortVal int64
	Seg     int64
	Idx     int64
}

func (k Key) less(o Key) bool {
	if k.SortVal != o.SortVal {
		return k.SortVal < o.SortVal
	}
	if k.Seg != o.Seg {
		return k.Seg < o.Seg
	}
	return k.Idx < o.Idx
}

// Hit 是一条命中结果：文档 id 及其排序键。
type Hit struct {
	ID  string
	Key Key
}

// Searcher 在 segstore 之上提供翻页检索。
type Searcher struct {
	store *segstore.Store
	pits  *pit.Manager
}

// NewSearcher 创建检索器；m 可为 nil（只允许 pid=0 的当前视图检索）。
func NewSearcher(store *segstore.Store, m *pit.Manager) *Searcher {
	return &Searcher{store: store, pits: m}
}

// Search 返回排序键严格大于 after 的前 size 条命中。
// pid 为 0 表示搜当前视图（此时 after 必须为 nil 且 ka 必须为 0）；
// pid 非 0 时 ka>0 表示把该 PIT 的过期时刻续期为 max(exp, now+ka)。
func (s *Searcher) Search(now, pid int64, size int, after *Key, ka int64) ([]Hit, error) {
	if now < 0 || now > segstore.MaxNow || size < 1 || size > 1000 ||
		ka < 0 || ka > pit.MaxKA || pid < 0 {
		return nil, segstore.ErrInvalidParam
	}
	if pid == 0 && (after != nil || ka != 0) {
		return nil, segstore.ErrInvalidParam
	}
	var hits []Hit
	err := s.store.RunOp(now, func(tx *segstore.Tx) error {
		var segs []int64
		var openOp int64
		current := pid == 0
		if current {
			segs = tx.View()
		} else {
			if s.pits == nil {
				return pit.ErrNotFound
			}
			p, ok := s.pits.Lookup(pid)
			if !ok {
				return pit.ErrNotFound
			}
			if ka > 0 {
				s.pits.Renew(pid, now+ka)
			}
			segs = p.Segs
			openOp = p.OpenOp
		}
		hits = collect(tx, segs, current, openOp, size, after)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}

// cursor 是某段内的归并游标。
type cursor struct {
	seg *segstore.Segment
	idx int
}

type curHeap []cursor

func (h curHeap) Len() int { return len(h) }

func (h curHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	av, bv := a.seg.SortVal(a.idx), b.seg.SortVal(b.idx)
	if av != bv {
		return av < bv
	}
	if a.seg.Num() != b.seg.Num() {
		return a.seg.Num() < b.seg.Num()
	}
	return a.idx < b.idx
}

func (h curHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *curHeap) Push(x any) { *h = append(*h, x.(cursor)) }

func (h *curHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	*h = old[:n-1]
	return c
}

// startIndex 返回段内第一个排序键严格大于 after 的下标。
func startIndex(seg *segstore.Segment, after *Key) int {
	if after == nil {
		return 0
	}
	return sort.Search(seg.Len(), func(i int) bool {
		k := Key{SortVal: seg.SortVal(i), Seg: seg.Num(), Idx: int64(i)}
		return after.less(k)
	})
}

// collect 在 segs 上做 k 路归并，返回键严格大于 after 的前 size 条可见命中。
// current 为 true 时可见性规则是“未被删除”；否则是 PIT 规则
// “未删除或删除操作号大于 openOp”。
func collect(tx *segstore.Tx, segNums []int64, current bool, openOp int64, size int, after *Key) []Hit {
	h := &curHeap{}
	for _, n := range segNums {
		seg := tx.Segment(n)
		if seg == nil {
			continue
		}
		if start := startIndex(seg, after); start < seg.Len() {
			*h = append(*h, cursor{seg: seg, idx: start})
		}
	}
	heap.Init(h)
	var hits []Hit
	for h.Len() > 0 && len(hits) < size {
		c := heap.Pop(h).(cursor)
		delOp := c.seg.DelOp(c.idx)
		if delOp == 0 || (!current && delOp > openOp) {
			hits = append(hits, Hit{
				ID:  c.seg.ID(c.idx),
				Key: Key{SortVal: c.seg.SortVal(c.idx), Seg: c.seg.Num(), Idx: int64(c.idx)},
			})
		}
		if c.idx+1 < c.seg.Len() {
			heap.Push(h, cursor{seg: c.seg, idx: c.idx + 1})
		}
	}
	return hits
}
