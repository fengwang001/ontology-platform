package match

import (
	"errors"

	"ontology/window"
)

var (
	ErrBadConfig = errors.New("match: chain limit must be > 0")
	MinMatch     = 4
)

// Matcher 是确定性哈希链匹配器，只在窗口容量内找候选。
// 非导出 candidates 记录考察过的候选位置总数。
type Matcher struct {
	win        *window.Window
	chain      int
	head       map[uint32]int
	prev       []int
	data       []byte
	base       int
	candidates int64
}

func New(win *window.Window, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrBadConfig
	}
	m := &Matcher{
		win:   win,
		chain: chainLimit,
		head:  make(map[uint32]int),
		prev:  make([]int, win.Cap()+1),
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	return m, nil
}

// BeginBlock 声明接下来扫描 data；窗口中已有历史（可能是预置字典）。
func (m *Matcher) BeginBlock(data []byte) {
	m.data = data
	m.base = m.win.LastPos() + 1
}

func (m *Matcher) hash(pos int) uint32 {
	h := uint32(2166136261)
	for _, b := range m.data[pos : pos+3] {
		h ^= uint32(b)
		h *= 16777619
	}
	return h
}

func (m *Matcher) byteAt(abs int) byte {
	if abs >= m.base {
		return m.data[abs-m.base]
	}
	return m.win.AtAbs(abs)
}

// Find 返回 data[pos] 起的最长匹配长度与距离（0 表示无 ≥MinMatch 的匹配）。
func (m *Matcher) Find(pos int) (length, dist int) {
	if pos+3 > len(m.data) {
		return 0, 0
	}
	curAbs := m.base + pos
	h := m.hash(pos)
	cand, examined := m.head[h], 0
	for cand >= 0 && examined < m.chain && m.win.Contains(cand) {
		m.candidates++
		examined++
		d := curAbs - cand
		limit := len(m.data) - pos
		l := 0
		for l < limit && m.byteAt(cand+l) == m.data[pos+l] {
			l++
		}
		if l > length && l >= MinMatch {
			length, dist = l, d
		}
		next := m.prev[cand%(len(m.prev))]
		if next >= cand {
			break
		}
		cand = next
	}
	return length, dist
}

// Insert 把 data[pos] 位置压入窗口与哈希链。
func (m *Matcher) Insert(pos int) {
	curAbs := m.base + pos
	m.win.Push(m.data[pos])
	if pos+3 <= len(m.data) {
		h := m.hash(pos)
		if old, ok := m.head[h]; ok {
			m.prev[curAbs%len(m.prev)] = old
		}
		m.head[h] = curAbs
	}
}

// Candidates 返回考察过的候选位置总数。
func (m *Matcher) Candidates() int64 { return m.candidates }
