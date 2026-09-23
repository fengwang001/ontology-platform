// Package match 在滑动窗口内用哈希链找最长匹配，候选链长度有上限。
// 依赖 window 包。单个实例不要求并发安全。
package match

import (
	"errors"

	"ontology/window"
)

// ErrBadChainLimit 表示链长上限非法（≤0）。
var ErrBadChainLimit = errors.New("match: chain limit must be positive")

const headSize = 1 << 17

// Matcher 通过 win 读取历史字节；maxDist 为允许的最大回指距离。
// win 的容量必须 ≥ maxDist + 调用方传入的最大 maxLen。
type Matcher struct {
	win      *window.Window
	maxDist  int64
	limit    int
	head     []int64
	prev     []int64
	examined int64 // 考察过的候选位置总数
}

// New 创建匹配器；chainLimit ≤ 0 时拒绝。
func New(win *window.Window, maxDist, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrBadChainLimit
	}
	head := make([]int64, headSize)
	for i := range head {
		head[i] = -1
	}
	return &Matcher{
		win: win, maxDist: int64(maxDist), limit: chainLimit,
		head: head, prev: make([]int64, maxDist),
	}, nil
}

// Examined 返回累计考察过的候选位置总数。
func (m *Matcher) Examined() int64 { return m.examined }

func hash3(a, b, c byte) int64 {
	return (int64(a)*251 + int64(b)*251 + int64(c)) & (headSize - 1)
}

// Insert 把位置 pos 加入哈希链；调用前需保证 pos+2 已喂入窗口。
func (m *Matcher) Insert(pos int64) {
	h := hash3(m.win.Abs(pos), m.win.Abs(pos+1), m.win.Abs(pos+2))
	m.prev[pos%m.maxDist] = m.head[h]
	m.head[h] = pos
}

// Find 在窗口内为位置 pos 找最长匹配，返回距离与长度（无匹配时长度为 0）。
// 调用前需保证 pos+maxLen-1 已喂入窗口。
func (m *Matcher) Find(pos int64, maxLen int) (dist, length int) {
	h := hash3(m.win.Abs(pos), m.win.Abs(pos+1), m.win.Abs(pos+2))
	best, bestDist := 0, 0
	for cand, steps := m.head[h], 0; cand >= 0 && steps < m.limit; steps++ {
		d := pos - cand
		if d > m.maxDist {
			break
		}
		m.examined++
		l := 0
		for l < maxLen && m.win.Abs(cand+int64(l)) == m.win.Abs(pos+int64(l)) {
			l++
		}
		if l > best {
			best, bestDist = l, int(d)
			if l >= maxLen {
				break
			}
		}
		next := m.prev[cand%m.maxDist]
		if next >= cand {
			break // 槽位被复用，链到此为止
		}
		cand = next
	}
	return bestDist, best
}
