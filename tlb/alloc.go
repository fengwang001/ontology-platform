package tlb

import "math/bits"

// asidAlloc 是一个两级位图分配器，管理编号 1..n 的 ASID。
// 第一级为 64 个 64 位字（共 4096 位），第二级 summary 的第 i 位为 1
// 表示该字已满。alloc/free 均为严格 O(1)，reset 为 O(n/64)。
// 超出 n 的位始终置 1（视为已占用），因此永远不会被分配。
type asidAlloc struct {
	words   [64]uint64
	summary uint64
	n       int // 可用 ASID 个数（编号 1..n）
	count   int // 当前已占用个数
}

func newAsidAlloc(n int) asidAlloc {
	a := asidAlloc{n: n}
	a.reset()
	return a
}

// reset 清空全部占用位（超出 n 的位保持置 1），O(n/64)。
func (a *asidAlloc) reset() {
	for i := range a.words {
		a.words[i] = 0
	}
	// 将编号 > n 的位标记为占用。
	for asid := a.n + 1; asid <= 4096; asid++ {
		a.words[(asid-1)/64] |= 1 << uint((asid-1)%64)
	}
	a.summary = 0
	for i, w := range a.words {
		if w == ^uint64(0) {
			a.summary |= 1 << uint(i)
		}
	}
	a.count = 0
}

// alloc 返回编号最小的空闲 ASID；无空闲时返回 0。O(1)。
func (a *asidAlloc) alloc() uint32 {
	if a.count == a.n {
		return 0
	}
	wi := bits.TrailingZeros64(^a.summary)
	bi := bits.TrailingZeros64(^a.words[wi])
	a.words[wi] |= 1 << uint(bi)
	if a.words[wi] == ^uint64(0) {
		a.summary |= 1 << uint(wi)
	}
	a.count++
	return uint32(wi*64 + bi + 1)
}

// free 释放一个 ASID，调用方保证其处于占用状态。O(1)。
func (a *asidAlloc) free(asid uint32) {
	wi := (asid - 1) / 64
	bi := (asid - 1) % 64
	a.words[wi] &^= 1 << uint(bi)
	a.summary &^= 1 << uint(wi)
	a.count--
}

// set 标记一个 ASID 为占用，调用方保证其处于空闲状态。O(1)。
func (a *asidAlloc) set(asid uint32) {
	wi := (asid - 1) / 64
	bi := (asid - 1) % 64
	a.words[wi] |= 1 << uint(bi)
	if a.words[wi] == ^uint64(0) {
		a.summary |= 1 << uint(wi)
	}
	a.count++
}

// taken 报告一个 ASID 是否处于占用状态。O(1)。
func (a *asidAlloc) taken(asid uint32) bool {
	if asid == 0 || int(asid) > a.n {
		return false
	}
	return a.words[(asid-1)/64]&(1<<uint((asid-1)%64)) != 0
}
