package search

import (
	"container/heap"

	"ontology/hyper"
	"ontology/vec"
)

// Result 是一个近邻结果：向量内部下标与其到查询的平方距离。
type Result struct {
	ID   int32
	Dist float64
}

// candidates 生成所有与查询在任一表同桶的向量下标（去重并集）。
func (ix *Index) candidates(q vec.Vector) (map[int32]struct{}, []hyper.Signature, error) {
	sigs := make([]hyper.Signature, ix.family.Tables)
	for t := 0; t < ix.family.Tables; t++ {
		s, err := ix.family.Sign(q, t)
		if err != nil {
			return nil, nil, err
		}
		sigs[t] = s
	}
	set := make(map[int32]struct{})
	for t, s := range sigs {
		for _, id := range ix.tables.Get(t, s) {
			set[id] = struct{}{}
		}
	}
	return set, sigs, nil
}

// Search 做近似最近邻，返回距离最小的至多 K 个结果。
// rerankN 返回精排阶段实际计算距离的向量数（候选集合大小）。
func (ix *Index) Search(q vec.Vector, k int) (res []Result, rerankN int, err error) {
	cand, _, err := ix.candidates(q)
	if err != nil {
		return nil, 0, err
	}
	if k > len(cand) {
		k = len(cand)
	}
	h := &maxHeap{}
	heap.Init(h)
	for id := range cand {
		// 每个候选恰好计算一次距离。
		d, derr := vec.EuclideanSq(q, ix.vectors[id])
		if derr != nil {
			return nil, 0, derr
		}
		if h.Len() < k {
			heap.Push(h, Result{ID: id, Dist: d})
		} else if d < (*h)[0].Dist {
			(*h)[0] = Result{ID: id, Dist: d}
			heap.Fix(h, 0)
		}
	}
	out := make([]Result, 0, h.Len())
	for h.Len() > 0 {
		out = append(out, heap.Pop(h).(Result))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, len(cand), nil
}

// maxHeap 以距离为序的最大堆，用于保留最小的 K 个。
type maxHeap []Result

func (h maxHeap) Len() int            { return len(h) }
func (h maxHeap) Less(i, j int) bool  { return h[i].Dist > h[j].Dist }
func (h maxHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *maxHeap) Push(x interface{}) { *h = append(*h, x.(Result)) }
func (h *maxHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
