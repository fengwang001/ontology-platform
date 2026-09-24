// Package pofs 实现单分区的有序缓冲：连续位点校验、按位点存值、
// 前缀和数组、计数与总和。不依赖其他包。
package pofs

import "errors"

// ErrGap 位点不连续：Append 的 pos 不等于当前计数。
var ErrGap = errors.New("pofs: position not contiguous")

// ErrNegative 值为负。
var ErrNegative = errors.New("pofs: negative value")

// Buffer 是单分区有序缓冲。prefix[i] 为前 i 条事件的 Val 之和，
// 故 prefix 长度恒为 count+1，取前 k 条之和是 O(1) 的 prefix[k]。
type Buffer struct {
	prefix []int64
}

// New 返回空缓冲。
func New() *Buffer {
	return &Buffer{prefix: []int64{0}}
}

// Append 接受第 count 条事件：pos 必须恰好等于当前计数，val 必须非负。
// 任一校验失败都不改变状态。
func (b *Buffer) Append(pos, val int64) error {
	if val < 0 {
		return ErrNegative
	}
	if pos != int64(len(b.prefix)-1) {
		return ErrGap
	}
	b.prefix = append(b.prefix, b.prefix[len(b.prefix)-1]+val)
	return nil
}

// Count 返回已接受事件数。
func (b *Buffer) Count() int {
	return len(b.prefix) - 1
}

// Sum 返回全部已接受事件的 Val 之和。
func (b *Buffer) Sum() int64 {
	return b.prefix[len(b.prefix)-1]
}

// PrefixSum 返回前 k 条事件的 Val 之和；k 越界时截断到 [0, Count]。
func (b *Buffer) PrefixSum(k int) int64 {
	if k < 0 {
		k = 0
	}
	if k > b.Count() {
		k = b.Count()
	}
	return b.prefix[k]
}
