// Package pool 维护英雄池可用状态。
//
// 可用性用分层位图存储：第 0 层每位对应一个英雄（1 可用 / 0 已占用），
// 每升高一层，一个字（64 位）汇总其下 64 个字是否存在任意置位。
// 查找最小可用英雄自顶向下定位，读取的记录数只取决于层高而与英雄总数无关。
package pool

const wordBits = 64

// Pool 为英雄池：英雄编号 1..H，可用（未被禁也未被选）置 1。
type Pool struct {
	h       int
	layers  [][]uint64
	touched int
}

// New 创建英雄 1..h 全部可用的英雄池。
func New(h int) *Pool {
	p := &Pool{h: h}
	size := h + 1 // 索引 0 保留（英雄编号从 1 开始）
	for size > 1 {
		n := (size + wordBits - 1) / wordBits
		p.layers = append(p.layers, make([]uint64, n))
		size = n
	}
	// 第 0 层：英雄 1..h 置 1，保留位 0 保持为 0。
	base := p.layers[0]
	for hero := 1; hero <= h; hero++ {
		base[hero/wordBits] |= 1 << uint(hero%wordBits)
	}
	// 由下向上汇总，使每一层第 j 字的第 k 位等于其下一层第 64*j+k 字是否非零。
	for lv := 1; lv < len(p.layers); lv++ {
		lower := p.layers[lv-1]
		cur := p.layers[lv]
		for j, w := range lower {
			if w != 0 {
				cur[j/wordBits] |= 1 << uint(j%wordBits)
			}
		}
	}
	return p
}

// H 返回英雄总数。
func (p *Pool) H() int { return p.h }

// Available 报告英雄 hero 是否仍可用。
func (p *Pool) Available(hero int) bool {
	if hero < 1 || hero > p.h {
		return false
	}
	w := p.layers[0][hero/wordBits]
	p.touched++
	return w&(1<<uint(hero%wordBits)) != 0
}

// Remove 将英雄标记为已禁或已选；返回 false 表示编号非法或该英雄本不可用。
func (p *Pool) Remove(hero int) bool {
	if !p.Available(hero) {
		return false
	}
	idx := hero / wordBits
	bit := uint(hero % wordBits)
	p.layers[0][idx] &^= 1 << bit
	// 该字变为 0 时沿汇总链向上清除对应位；任一层字仍非零即停止。
	if p.layers[0][idx] != 0 {
		return true
	}
	for lv := 1; lv < len(p.layers); lv++ {
		parentIdx := idx / wordBits
		cur := p.layers[lv]
		cur[parentIdx] &^= 1 << uint(idx%wordBits)
		idx = parentIdx
		if cur[idx] != 0 {
			break
		}
	}
	return true
}

// FirstAvailable 返回编号最小的可用英雄；无可用英雄时 ok 为 false。
func (p *Pool) FirstAvailable() (hero int, ok bool) {
	top := p.layers[len(p.layers)-1]
	idx := -1
	for j, w := range top {
		p.touched++
		if w != 0 {
			idx = j*wordBits + trailingZeros(w)
			break
		}
	}
	if idx < 0 {
		return 0, false
	}
	for lv := len(p.layers) - 2; lv >= 0; lv-- {
		w := p.layers[lv][idx]
		p.touched++
		idx = idx*wordBits + trailingZeros(w)
	}
	if idx < 1 || idx > p.h {
		return 0, false
	}
	return idx, true
}

// Restore 把英雄重新标记为可用，仅用于试探性追赶被拒绝后的整体回滚；
// 正常流程中每个英雄至多被占用一次，不存在恢复后与其它状态冲突。
func (p *Pool) Restore(hero int) {
	if hero < 1 || hero > p.h {
		return
	}
	idx := hero / wordBits
	bit := uint(hero % wordBits)
	p.layers[0][idx] |= 1 << bit
	for lv := 1; lv < len(p.layers); lv++ {
		cur := p.layers[lv]
		old := cur[idx/wordBits]
		cur[idx/wordBits] |= 1 << uint(idx%wordBits)
		idx /= wordBits
		if old != 0 {
			break
		}
	}
}

// ResetTouched 将非导出的记录读取计数器清零，供自动选人前后计量使用。
func (p *Pool) ResetTouched() { p.touched = 0 }

// Touched 返回自上次 ResetTouched 以来读取的位图记录（uint64 字）数。
func (p *Pool) Touched() int { return p.touched }

func trailingZeros(w uint64) int {
	if w == 0 {
		return wordBits
	}
	n := 0
	for w&1 == 0 {
		w >>= 1
		n++
	}
	return n
}
