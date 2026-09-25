// Package rank 从候选里按 (频率降, 字典序升) 用小顶堆做部分选择。依赖 trie。
// collected 为非导出复杂度计数器，只供包内测试读取。
package rank

import (
	"container/heap"
	"errors"
	"sync/atomic"

	"ontology/trie"
)

var ErrInvalidK = errors.New("rank: k must be >= 1")

var collected atomic.Int64 // Complete 为做 top-k 而收集/比较的候选数

// candHeap 是小顶堆：堆顶永远是当前已选中最差的一个。
type candHeap []trie.Candidate

func (h candHeap) Len() int { return len(h) }
func (h candHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.Freq != b.Freq {
		return a.Freq < b.Freq // 频率低者更差
	}
	return a.Word > b.Word // 同频字典序大者更差
}
func (h candHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *candHeap) Push(x any)   { *h = append(*h, x.(trie.Candidate)) }
func (h *candHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// better 报告 a 是否比 b 更该入选（频率高，或同频字典序小）。
func better(a, b trie.Candidate) bool {
	if a.Freq != b.Freq {
		return a.Freq > b.Freq
	}
	return a.Word < b.Word
}

// TopK 返回候选中排序键 (频率降, 字典序升) 的前 k 个串；k<=0 报错且无副作用。
func TopK(cands []trie.Candidate, k int) ([]string, error) {
	if k <= 0 {
		return nil, ErrInvalidK
	}
	collected.Add(int64(len(cands)))
	h := &candHeap{}
	for _, c := range cands {
		if h.Len() < k {
			heap.Push(h, c)
		} else if better(c, (*h)[0]) {
			(*h)[0] = c
			heap.Fix(h, 0)
		}
	}
	out := make([]string, h.Len())
	for i := len(out) - 1; i >= 0; i-- { // 弹出顺序为最差→最好，倒填即得正序
		out[i] = heap.Pop(h).(trie.Candidate).Word
	}
	return out, nil
}
