// Package route 管理 P 个分区的有序接入与跨分区统计：
// 水位 W()=min(各分区计数) 与全局已提交前缀和 PrefixTotal()。
// 依赖方向：route -> pofs，不允许反向依赖。
package route

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/pofs"
)

// 三类可判定、互不相同的哨兵错误。
var (
	// ErrPartOutOfRange：Part < 0 或 Part >= P。
	ErrPartOutOfRange = errors.New("route: partition out of range")
	// ErrGap：Pos 不等于该分区当前计数（空洞或重复）。
	ErrGap = pofs.ErrGap
	// ErrNegative：Val 为负。
	ErrNegative = pofs.ErrNegative
)

// Router 是多分区保序路由器，状态全部在进程内存。
type Router struct {
	mu    sync.RWMutex
	parts []*pofs.Buffer
	total int64
	// prefixChecks 记录最近一次 PrefixTotal() 为取各分区前缀和
	// 而检查过的分区个数。非导出，不经过任何公开方法暴露。
	prefixChecks atomic.Int64
}

// NewRouter 创建 P 个空分区的路由器。
func NewRouter(P int) *Router {
	r := &Router{parts: make([]*pofs.Buffer, P)}
	for i := range r.parts {
		r.parts[i] = pofs.New()
	}
	return r
}

// P 返回分区总数。
func (r *Router) P() int { return len(r.parts) }

// Append 按 (part, pos, val) 接收一条事件。三类拒绝互不相同：
// 先查 Part 越界，再查 Val 为负，最后查 Pos 是否等于该分区当前计数；
// 任一失败都在写入前返回，状态零改动。
func (r *Router) Append(part int, pos, val int64) error {
	if part < 0 || part >= len(r.parts) {
		return ErrPartOutOfRange
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if val < 0 {
		return ErrNegative
	}
	b := r.parts[part]
	if pos != int64(b.Count()) {
		return ErrGap
	}
	if err := b.Append(pos, val); err != nil { // 理论上不会再失败
		return err
	}
	r.total += val
	return nil
}

// watermarkLocked 返回 min(各分区计数)，调用方须持有读锁。
func (r *Router) watermarkLocked() int {
	w := -1
	for _, b := range r.parts {
		if c := b.Count(); w < 0 || c < w {
			w = c
		}
	}
	if w < 0 {
		return 0
	}
	return w
}

// W 返回所有分区都已对齐到的最大前缀长度。
func (r *Router) W() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.watermarkLocked()
}

// PrefixTotal 返回「全局已提交前缀」的和：每个分区取前 W() 条求和再跨分区相加。
// 经各分区前缀和数组 O(1) 取前缀，整体 O(P)；prefixChecks 记录实际检查的分区数。
func (r *Router) PrefixTotal() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w := r.watermarkLocked()
	r.prefixChecks.Store(0)
	var sum int64
	for _, b := range r.parts {
		r.prefixChecks.Add(1)
		sum += b.PrefixSum(w)
	}
	return sum
}

// Total 返回全部已接受事件的 Val 之和。
func (r *Router) Total() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.total
}

// PartCount 返回分区 p 已接受事件数；p 越界返回 0。
func (r *Router) PartCount(p int) int {
	if p < 0 || p >= len(r.parts) {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.parts[p].Count()
}

// PartSum 返回分区 p 的 Val 之和；p 越界返回 0。
func (r *Router) PartSum(p int) int64 {
	if p < 0 || p >= len(r.parts) {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.parts[p].Sum()
}
