// Package match 在滑动窗口内用哈希链找最长匹配，候选链长度有上限。
// 依赖 window。单个实例不是并发安全的。
package match

import (
	"errors"

	"ontology/window"
)

// ErrBadChain 表示链长上限非法（必须为正）。
var ErrBadChain = errors.New("match: 链长上限必须为正整数")

// Matcher 是哈希链匹配器。窗口内每个位置在进入窗口时按 3 字节哈希入链。
type Matcher struct {
	win      *window.Window
	maxChain int
	head     map[uint32]int // 3 字节哈希 -> 最新绝对位置
	prev     []int          // 链环，下标为 绝对位置 % 窗口容量
	total    int            // 已插入字节总数
	pend     uint32         // 滚动哈希的待定字节
	pendN    int
	examined int64 // 考察过的候选位置总数（非导出，经 Examined 读取）
}

// New 创建匹配器，maxChain <= 0 时返回 ErrBadChain。
func New(win *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrBadChain
	}
	return &Matcher{
		win: win, maxChain: maxChain,
		head: make(map[uint32]int), prev: make([]int, win.Cap()),
	}, nil
}

func hash3(a, b, c byte) uint32 {
	return (uint32(a)<<16 ^ uint32(b)<<8 ^ uint32(c)) * 2654435761
}

// Insert 插入一个字节；凑满 3 字节时为对应位置登记哈希链。
func (m *Matcher) Insert(b byte) {
	m.win.Write(b)
	m.pend = m.pend<<8 | uint32(b)
	if m.pendN++; m.pendN >= 3 {
		p := m.total - 2
		h := m.pend & 0xFFFFFF
		m.prev[p%m.win.Cap()] = m.head[h] - 1
		m.head[h] = p + 1
	}
	m.total++
}

// Examined 返回至今考察过的候选位置总数。
func (m *Matcher) Examined() int64 { return m.examined }

// FindLongest 在窗口中为 lookahead 前缀找最长匹配，返回距离与长度。
// 未找到任何匹配时返回长度 0。匹配长度不超过 maxLen，允许距离 < 长度
// （重叠部分取自 lookahead 自身，与解码端逐字节前向复制语义一致）。
func (m *Matcher) FindLongest(lookahead []byte, maxLen int) (dist, length int) {
	if len(lookahead) < 3 || maxLen < 1 {
		return 0, 0
	}
	if maxLen > len(lookahead) {
		maxLen = len(lookahead)
	}
	h := hash3(lookahead[0], lookahead[1], lookahead[2])
	best := 0
	for pos, chain := m.head[h]-1, 0; pos >= 0 && chain < m.maxChain; chain++ {
		d := m.total - pos
		if d < 1 || d > m.win.Len() {
			break // 已滑出窗口
		}
		m.examined++
		if l := m.matchLen(pos, lookahead, maxLen); l > best {
			best, dist = l, d
			if l >= maxLen {
				break
			}
		}
		next := m.prev[pos%m.win.Cap()]
		if next >= pos { // 链环槽位被复写，终止
			break
		}
		pos = next
	}
	return dist, best
}

// matchLen 计算窗口中绝对位置 pos 处与 lookahead 的最长公共前缀。
func (m *Matcher) matchLen(pos int, lookahead []byte, maxLen int) int {
	l := 0
	for l < maxLen {
		q := pos + l
		var hb byte
		if q < m.total {
			hb = m.win.At(m.total - q)
		} else {
			hb = lookahead[q-m.total] // 重叠回指：历史即 lookahead 自身
		}
		if hb != lookahead[l] {
			break
		}
		l++
	}
	return l
}
