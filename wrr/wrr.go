// Package wrr 实现平滑加权轮询（Nginx 平滑算法）核心。
// 不依赖本工程其他包。
package wrr

import (
	"container/heap"
	"errors"
	"sync"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrNoServers       = errors.New("wrr: weights 为空")
	ErrBadConfigWeight = errors.New("wrr: 配置中存在权重 <= 0")
	ErrIndexOutOfRange = errors.New("wrr: 下标越界")
	ErrBadWeight       = errors.New("wrr: SetWeight 的权重 <= 0")
)

// idxHeap 是服务器下标的最大堆：cw 大者优先，并列时下标小者优先。
type idxHeap struct {
	idx []int
	cw  *[]int
}

func (h idxHeap) Len() int { return len(h.idx) }
func (h idxHeap) Less(i, j int) bool {
	a, b := h.idx[i], h.idx[j]
	c := *h.cw
	if c[a] != c[b] {
		return c[a] > c[b]
	}
	return a < b
}
func (h idxHeap) Swap(i, j int) { h.idx[i], h.idx[j] = h.idx[j], h.idx[i] }
func (h *idxHeap) Push(x any)   { h.idx = append(h.idx, x.(int)) }
func (h *idxHeap) Pop() (v any) { old := h.idx; v = old[len(old)-1]; h.idx = old[:len(old)-1]; return }

// WRR 是平滑加权轮询调度器。cw 满足不变量 Σcw_i == 0。
type WRR struct {
	mu sync.Mutex
	w  []int // 权重，均 >= 1
	cw []int // 当前权重
	W  int   // 总权重
	h  idxHeap
	// checked 记录最近一次 Next 中为定位最大者检查的服务器个数。
	// 定位 = 读堆顶，堆化属于权重更新阶段的维护，不计入。
	checked int
}

// New 校验并构造调度器；非法配置整体失败，不留任何状态。
func New(weights []int) (*WRR, error) {
	if len(weights) == 0 {
		return nil, ErrNoServers
	}
	W := 0
	for _, x := range weights {
		if x <= 0 {
			return nil, ErrBadConfigWeight
		}
		W += x
	}
	w := append([]int(nil), weights...)
	r := &WRR{w: w, cw: make([]int, len(w)), W: W}
	r.h.cw = &r.cw
	r.h.idx = make([]int, len(w))
	for i := range r.h.idx {
		r.h.idx[i] = i
	}
	return r, nil
}

// Next 先给每个 cw_i 加 w_i，再取最大者（并列取下标最小），
// 把选中的 cw 减去 W，返回其下标。
func (r *WRR) Next() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.cw {
		r.cw[i] += r.w[i]
	}
	heap.Init(&r.h) // 权重更新后的堆维护
	r.checked = 1   // 定位最大者只检查堆顶一台
	s := r.h.idx[0]
	r.cw[s] -= r.W
	return s
}

// SetWeight 更新 w_i、重算 W 并把全部 cw 重置为 0。
// 参数非法时不改变任何状态。
func (r *WRR) SetWeight(i, w int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.w) {
		return ErrIndexOutOfRange
	}
	if w <= 0 {
		return ErrBadWeight
	}
	r.W += w - r.w[i]
	r.w[i] = w
	for j := range r.cw {
		r.cw[j] = 0
	}
	return nil
}

// Weight 返回服务器 i 的权重；下标越界返回 -1。
func (r *WRR) Weight(i int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.w) {
		return -1
	}
	return r.w[i]
}

// SumCW 返回 Σcw_i，不变量要求恒为 0。
func (r *WRR) SumCW() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := 0
	for _, c := range r.cw {
		s += c
	}
	return s
}

// N 返回服务器台数。
func (r *WRR) N() int { return len(r.w) }

// Total 返回总权重 W。
func (r *WRR) Total() int { return r.W }
