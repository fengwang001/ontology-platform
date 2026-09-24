// Package match 在滑动窗口内用哈希链找最长匹配，候选链长度有上限。
package match

import (
	"errors"

	"ontology/window"
)

// ErrChain 表示非法的链长上限。
var ErrChain = errors.New("match: chain limit must be positive")

// Matcher 维护窗口内位置的哈希链，并统计考察过的候选位置总数。
type Matcher struct {
	win   *window.Window
	chain int
	head  map[uint32]int64 // 3 字节哈希 -> 最新位置+1
	prev  []int64          // 位置 % 容量 -> 同哈希前一位置+1
	cand  int64            // 考察过的候选位置总数
}

// New 创建匹配器；win 为 nil 或 chain <= 0 时返回错误。
func New(win *window.Window, chain int) (*Matcher, error) {
	if chain <= 0 {
		return nil, ErrChain
	}
	if win == nil {
		return nil, errors.New("match: nil window")
	}
	return &Matcher{win: win, chain: chain, head: map[uint32]int64{}, prev: make([]int64, win.Cap())}, nil
}

// Append 把一个字节写入窗口，并在凑满 3 字节时索引其起始位置。
func (m *Matcher) Append(b byte) {
	m.win.Write(b)
	if n := m.win.Len(); n >= 3 {
		p := n - 3
		h := m.hashAt(p)
		m.prev[p%m.win.Cap()] = m.head[h]
		m.head[h] = p + 1
	}
}

func (m *Matcher) hashAt(p int64) uint32 {
	n := m.win.Len()
	v := uint32(m.win.At(n-p))<<16 | uint32(m.win.At(n-p-1))<<8 | uint32(m.win.At(n-p-2))
	return v * 2654435761
}

// Find 在 pos 处找最长匹配，返回距离与长度；无匹配时长度 < 3。
// 只考察窗口内、pos 之前的候选，最多 chain 个。
func (m *Matcher) Find(pos int64, maxLen int) (dist, length int) {
	n := m.win.Len()
	if maxLen < 3 || pos+3 > n {
		return 0, 0
	}
	c := m.head[m.hashAt(pos)]
	best, bestLen := 0, 0
	for k := 0; k < m.chain && c > 0; k++ {
		p := c - 1
		if p < n-m.win.Cap() {
			break // 链上位置单调递减，更老的都已出窗
		}
		if p < pos {
			m.cand++
			if l := m.cmp(pos, p, maxLen, n); l > bestLen {
				best, bestLen = int(pos-p), l
				if l >= maxLen {
					break
				}
			}
		}
		c = m.prev[p%m.win.Cap()]
	}
	return best, bestLen
}

func (m *Matcher) cmp(a, b int64, maxLen int, n int64) int {
	l := 0
	for l < maxLen && m.win.At(n-a-int64(l)) == m.win.At(n-b-int64(l)) {
		l++
	}
	return l
}

// Candidates 返回考察过的候选位置总数。
func (m *Matcher) Candidates() int64 { return m.cand }
