// Package frames 提供定长环形帧缓冲：帧号从 1 连续递增，只保留最近 K 帧，
// 支持 O(1) 追加与 O(1) 区间字节和查询。
package frames

import "sync"

// Buffer 是定长环形帧缓冲。
//
// 实现要点：不存每帧 size，而是存前缀和 pref[i] = 帧 1..i 的 size 之和。
// 环容量为 K+1（而非 K），因为区间 [low, hi] 求和需要 low-1 处的边界前缀，
// K+1 格恰好覆盖 [low-1, cur]，仍只对应"最近 K 帧"的语义。
type Buffer struct {
	mu      sync.Mutex
	k       int64
	cur     int64
	pref    []int64 // pref[i%cap] = 帧 1..i 的 size 前缀和，i ∈ [low-1, cur]
	touched int64   // 非导出计数器：区间查询累计读取的帧记录数
}

// NewBuffer 创建容量为 k 的帧缓冲，k 须 ≥ 1。
func NewBuffer(k int64) *Buffer {
	return &Buffer{k: k, pref: make([]int64, k+1)}
}

// Append 追加下一帧，返回其帧号（从 1 连续递增）。
func (b *Buffer) Append(size int64) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	prev := int64(0)
	if b.cur > 0 {
		prev = b.pref[b.cur%b.cap()]
	}
	b.cur++
	b.pref[b.cur%b.cap()] = prev + size
	return b.cur
}

// Cur 返回最新帧号，尚无帧时为 0。
func (b *Buffer) Cur() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cur
}

// Low 返回缓冲内最旧帧号：max(1, cur-K+1)。
func (b *Buffer) Low() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lowLocked()
}

// Sum 返回区间 [lo, hi] 内各帧 size 之和；lo > hi（空区间）返回 0 且不读记录。
// 调用方须保证 Low() <= lo <= hi <= Cur()；本系统中由计划规则保证（见 DESIGN.md）。
// 非空区间恰好读取 2 条记录（hi 与 lo-1 处的前缀和）。
func (b *Buffer) Sum(lo, hi int64) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if lo > hi {
		return 0
	}
	b.touched += 2
	return b.pref[hi%b.cap()] - b.pref[(lo-1)%b.cap()]
}

// Touched 返回区间查询累计读取的帧记录数（测试用）。
func (b *Buffer) Touched() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.touched
}

// ResetTouched 清零读取计数器（测试用）。
func (b *Buffer) ResetTouched() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.touched = 0
}

func (b *Buffer) cap() int64 { return b.k + 1 }

func (b *Buffer) lowLocked() int64 {
	low := b.cur - b.k + 1
	if low < 1 {
		low = 1
	}
	return low
}
