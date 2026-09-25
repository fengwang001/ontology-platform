// Package match 在滑动窗口内用哈希链找最长匹配，候选链长度有上限。
package match

import (
	"errors"

	"ontology/window"
)

// ErrZeroChain 表示链长上限非法（为 0 或负数）。
var ErrZeroChain = errors.New("match: chain limit must be positive")

// MinMatch 是回指值得发出的最小匹配长度。
const MinMatch = 3

// Matcher 为窗口中的位置维护哈希链。不是并发安全的。
type Matcher struct {
	win      *window.Window
	maxChain int
	head     []uint64 // 哈希槽 -> 位置+1，0 表示空
	prev     []uint64 // 位置 % cap -> 链上下一个位置+1
	mask     uint32
	indexed  uint64 // 小于该绝对位置的均已入链
	examined int    // 考察过的候选位置总数
}

// New 构造匹配器；maxChain <= 0 时拒绝。
func New(w *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrZeroChain
	}
	size := 1 << 8
	for size < w.Cap() && size < 1<<20 {
		size <<= 1
	}
	return &Matcher{
		win: w, maxChain: maxChain,
		head: make([]uint64, size), prev: make([]uint64, w.Cap()),
		mask: uint32(size - 1),
	}, nil
}

// Examined 返回考察过的候选位置总数（用于复杂度断言）。
func (m *Matcher) Examined() int { return m.examined }

func hash3(a, b, c byte) uint32 { return uint32(a)<<16 | uint32(b)<<8 | uint32(c) }

// sync 把 [indexed, base) 的位置入链；src 能读到 base 之后两个字节。
func (m *Matcher) sync(src func(uint64) byte, base uint64) {
	for ; m.indexed < base; m.indexed++ {
		q := m.indexed
		slot := hash3(src(q), src(q+1), src(q+2)) & m.mask
		m.prev[q%uint64(len(m.prev))] = m.head[slot]
		m.head[slot] = q + 1
	}
}

// FindLongest 在窗口历史中找 lookahead 的最长匹配，返回距离与长度。
// 要求 len(lookahead) >= MinMatch；src(p) 返回绝对位置 p 的字节，
// 对 p >= base 须能读到 lookahead 内部（重叠匹配）。最多考察 maxChain
// 个候选；某个候选已匹配到 lookahead 末尾时提前停止。
func (m *Matcher) FindLongest(src func(uint64) byte, base uint64, lookahead []byte) (dist, length int) {
	m.sync(src, base)
	best, bestDist := 0, 0
	cand := m.head[hash3(lookahead[0], lookahead[1], lookahead[2])&m.mask]
	for chain := 0; cand != 0 && chain < m.maxChain; chain++ {
		c := cand - 1
		d := base - c
		if d > uint64(m.win.Len()) { // 已挤出窗口，链上更早的也一样
			break
		}
		m.examined++
		l := 0
		for l < len(lookahead) && src(c+uint64(l)) == lookahead[l] {
			l++
		}
		if l > best {
			best, bestDist = l, int(d)
			if best == len(lookahead) {
				break
			}
		}
		cand = m.prev[c%uint64(len(m.prev))]
	}
	return bestDist, best
}
