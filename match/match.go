package match

import (
	"errors"

	"ontology/window"
)

// ErrConfig 在链长上限非法时返回。
var ErrConfig = errors.New("match: maxChain must be > 0")

// MinMatch 是回指允许的最小长度。
const MinMatch = 3

// Matcher 在窗口内用哈希链找最长匹配；候选链长度有上限。
type Matcher struct {
	w        *window.Window
	maxChain int
	head     []int64 // 哈希桶：最近一个绝对位置（-1 空）
	prev     []int64 // 按绝对位置环形记录同哈希前驱
	examined int64   // 考察过的候选位置总数（非导出）
}

// New 创建匹配器并以 dict 作为预置历史（可为空）。
func New(w *window.Window, maxChain int, dict []byte) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrConfig
	}
	m := &Matcher{w: w, maxChain: maxChain, head: make([]int64, 1<<16), prev: make([]int64, w.Cap())}
	for i := range m.head {
		m.head[i] = -1
	}
	w.Append(dict)
	for i := int64(0); i+2 < w.Total(); i++ {
		m.insert(i)
	}
	return m, nil
}

// Window 返回底层窗口。
func (m *Matcher) Window() *window.Window { return m.w }

// Examined 返回考察过的候选位置总数。
func (m *Matcher) Examined() int64 { return m.examined }

func (m *Matcher) h(pos int64) uint32 {
	a, _ := m.w.At(pos)
	b, _ := m.w.At(pos + 1)
	c, _ := m.w.At(pos + 2)
	return (uint32(a)<<10 ^ uint32(b)<<5 ^ uint32(c)) & 0xffff
}

func (m *Matcher) slot(pos int64) int { return int(uint64(pos) % uint64(m.w.Cap())) }

func (m *Matcher) insert(pos int64) {
	h := m.h(pos)
	m.prev[m.slot(pos)] = m.head[h]
	m.head[h] = pos
}

// Append 追加新字节，并为除最后 2 个外的所有新完整三元组建链。
func (m *Matcher) Append(p []byte) {
	base := m.w.Total()
	m.w.Append(p)
	for pos := base - 2; pos < m.w.Total()-2; pos++ {
		if pos >= 0 {
			m.insert(pos)
		}
	}
}

// Find 在绝对位置 pos（其字节必须已在窗口内）找最长匹配。
// limit 为允许匹配的最大结束绝对位置（不含）；返回距离与长度，长度<MinMatch 表示无匹配。
func (m *Matcher) Find(pos int64, limit int64) (distance, length int) {
	if pos+2 >= limit {
		return 0, 0
	}
	best := MinMatch - 1
	cand := m.head[m.h(pos)]
	maxLen := int(limit - pos)
	if capAvail := m.w.Cap(); maxLen > capAvail {
		maxLen = capAvail
	}
	k, steps := 0, 0
	for cand >= 0 && k < m.maxChain && steps <= m.w.Cap() {
		steps++
		cp := int64(cand)
		cand = m.prev[m.slot(cp)]
		if cp >= pos {
			continue // 同一次待决区间内更晚入链的位置：跳过
		}
		k++
		m.examined++
		if pos-cp > int64(m.w.Cap()) || cp >= pos {
			break // 已滑出窗口：真正的前驱只会更老
		}
		l := 0
		for l < maxLen {
			a, ok1 := m.w.At(cp + int64(l))
			b, ok2 := m.w.At(pos + int64(l))
			if !ok1 || !ok2 || a != b {
				break
			}
			l++
		}
		if l > best {
			best = l
			distance = int(pos - cp)
			if l == maxLen {
				return distance, l
			}
		}
	}
	if best < MinMatch {
		return 0, 0
	}
	return distance, best
}
