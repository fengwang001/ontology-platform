// Package match 在滑动窗口上用哈希链查找最长 LZ77 回指。
package match

import "ontology/window"

// Matcher 是单 goroutine 使用的哈希链匹配器。
type Matcher struct {
	win        *window.Window
	head       []int64 // 哈希桶 -> 最新位置，-1 表示空
	prev       []int64 // 按环形下标记录同哈希前驱位置，-1 表示链尾
	mask       uint64
	chainLimit int
	candidates int64
}

// New 创建匹配器。chainLimit 为每条链最多考察的候选数，必须 ≥1。
func New(win *window.Window, chainLimit int) *Matcher {
	n := 1
	for n < win.Cap() {
		n <<= 1
	}
	m := &Matcher{
		win:        win,
		head:       make([]int64, 1<<16),
		prev:       make([]int64, n),
		mask:       uint64(n - 1),
		chainLimit: chainLimit,
	}
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	return m
}

// Cap 返回匹配所用窗口容量。
func (m *Matcher) Cap() int { return m.win.Cap() }

// Candidates 返回 Find 累计考察过的候选位置总数。
func (m *Matcher) Candidates() int64 { return m.candidates }

func (m *Matcher) hashAt(pos uint64) uint32 {
	a, _ := m.win.ByteAt(pos)
	b, _ := m.win.ByteAt(pos + 1)
	c, _ := m.win.ByteAt(pos + 2)
	return (uint32(a)<<10 ^ uint32(b)<<5 ^ uint32(c)) & 0xFFFF
}

// Insert 把绝对位置 pos（其三元组必须已在窗口内）挂入哈希链。
func (m *Matcher) Insert(pos uint64) {
	h := m.hashAt(pos)
	m.prev[pos&m.mask] = m.head[h]
	m.head[h] = int64(pos)
}

// Find 在 pos 处寻找最长匹配。avail 为从 pos 起可比较的字节数；
// 返回距离与长度（长度 < 3 表示无匹配）。不修改哈希链。
func (m *Matcher) Find(pos uint64, avail int) (dist, length int) {
	if avail < 3 {
		return 0, 0
	}
	h := m.hashAt(pos)
	cand := m.head[h]
	best := 0
	limit := int64(pos) - int64(m.win.Cap())
	maxLen := avail
	if maxLen > 1<<20 {
		maxLen = 1 << 20
	}
	for n := 0; n < m.chainLimit && cand >= 0; n++ {
		cp := uint64(cand)
		if cand < limit || cp >= pos {
			break
		}
		m.candidates++
		d := int(pos - cp)
		// 两侧比较的都是真实历史字节（即使切片在窗口内重叠也无妨），
		// 因此允许长度超过距离；重叠语义由解压端保证。
		if maxLen > best {
			l := 0
			for l < maxLen {
				x, _ := m.win.ByteAt(pos + uint64(l))
				y, _ := m.win.ByteAt(cp + uint64(l))
				if x != y {
					break
				}
				l++
			}
			if l > best {
				best = l
				dist = d
				if l == maxLen {
					break
				}
			}
		}
		cand = m.prev[cp&m.mask]
	}
	return dist, best
}
