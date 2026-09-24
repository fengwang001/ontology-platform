// Package match 提供哈希链最长匹配器：在滑动窗口内为当前位置
// 寻找最长匹配，候选链长度有上限。依赖 window 包。
package match

import "ontology/window"

// MinMatch 是可接受为回指的最短匹配长度。
const MinMatch = 3

// Matcher 在窗口历史 + 前瞻字节上查找最长匹配。
// 位置为绝对坐标：窗口覆盖 [pos-Len(), pos)，前瞻由调用方经 at 提供。
type Matcher struct {
	win        *window.Window
	maxChain   int
	head       map[uint32]int // 3 字节哈希 -> 最新位置+1（0 表示空）
	prev       []int          // prev[pos%cap] -> 同哈希前一位置，-1 表示空
	candidates int            // 非导出计数器：考察过的候选位置总数
}

// New 创建匹配器；maxChain 为单次查询的候选链长度上限，必须为正。
func New(win *window.Window, maxChain int) *Matcher {
	prev := make([]int, win.Cap())
	for i := range prev {
		prev[i] = -1
	}
	return &Matcher{win: win, maxChain: maxChain, head: map[uint32]int{}, prev: prev}
}

// Candidates 返回至今考察过的候选位置总数（复杂度审计用）。
func (m *Matcher) Candidates() int { return m.candidates }

func hash3(a, b, c byte) uint32 {
	return (uint32(a)<<16 | uint32(b)<<8 | uint32(c)) * 2654435761
}

func (m *Matcher) slot(pos int) int {
	cap := m.win.Cap()
	return ((pos % cap) + cap) % cap
}

// Insert 把位置 pos 入链；调用方保证 pos+MinMatch 字节可读且按递增顺序调用。
func (m *Matcher) Insert(pos int, at func(int) byte) {
	h := hash3(at(pos), at(pos+1), at(pos+2))
	m.prev[m.slot(pos)] = m.head[h] - 1
	m.head[h] = pos + 1
}

// Find 在 [pos-win.Len(), pos) 内为 pos 处数据找最长匹配，长度上限 maxLen。
// 返回距离与长度（无匹配时长度为 0）；touched 表示最佳匹配在 dataEnd
// 处结束且未达 maxLen，即更多输入可能延长它（调用方应推迟决定）。
func (m *Matcher) Find(pos, dataEnd, maxLen int, at func(int) byte) (dist, length int, touched bool) {
	limit := dataEnd - pos
	if limit > maxLen {
		limit = maxLen
	}
	cand := m.head[hash3(at(pos), at(pos+1), at(pos+2))] - 1
	lower := pos - m.win.Len()
	for i := 0; i < m.maxChain && cand >= lower && cand >= 0; i++ {
		m.candidates++
		l := 0
		for l < limit && at(cand+l) == at(pos+l) {
			l++
		}
		if l > length {
			length, dist = l, pos-cand
			if l >= limit {
				break
			}
		}
		next := m.prev[m.slot(cand)]
		if next >= cand {
			break // 槽位被无关位置覆盖，链到此为止
		}
		cand = next
	}
	touched = length < maxLen && pos+length == dataEnd
	return dist, length, touched
}
