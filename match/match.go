// Package match 在滑动窗口内用哈希链找最长回指匹配。
package match

import (
	"errors"

	"ontology/window"
)

// MinMatch 是产生回指的最小长度。
const MinMatch = 3

// ErrInvalidConfig 在链长上限非正时返回。
var ErrInvalidConfig = errors.New("match: max chain must be positive")

const (
	hashBits = 14
	hashSize = 1 << hashBits
	hashMask = hashSize - 1
)

// Matcher 是哈希链匹配器。链中存历史字节的绝对位置；
// 环槽 prevAt 与窗口容量同大小，按位置取模保存该位置的后继指针。
type Matcher struct {
	w        *window.Window
	maxChain int
	head     [hashSize]int // 哈希桶链头，绝对位置，<0 为空
	prevAt   []int         // 环槽：p%cap 保存链中 p 的前一位置
	probes   int           // 非导出：考察过的候选位置总数
}

// New 基于给定窗口构造匹配器，maxChain 限制每条候选链长度。
func New(w *window.Window, maxChain int) (*Matcher, error) {
	if w == nil || maxChain <= 0 {
		return nil, ErrInvalidConfig
	}
	m := &Matcher{w: w, maxChain: maxChain, prevAt: make([]int, w.Cap())}
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prevAt {
		m.prevAt[i] = -1
	}
	return m, nil
}

func hash3(b0, b1, b2 byte) uint32 {
	return (uint32(b0)*101 + uint32(b1))*101 + uint32(b2)&hashMask
}

// AddByte 先让字节 b 进入窗口（不建链，用于预置字典的前两个字节
// 以及任何不构成完整三元组的起始位置）。
func (m *Matcher) AddByte(b byte) { m.w.Add(b) }

// Insert3 写入位置 pos 的字节 b，三元组前缀为 p1,p2（data[pos-2],data[pos-1]）。
func (m *Matcher) Insert3(pos int, p1, p2, b byte) {
	m.w.Add(b)
	if pos < MinMatch-1 {
		return
	}
	h := hash3(p1, p2, b)
	m.prevAt[pos%len(m.prevAt)] = m.head[h]
	m.head[h] = pos
}

// Find 在 cur 指向的当前位置之前的历史中，为 data[cur:] 找最长匹配。
// 匹配长度允许超过距离（重叠回指）；找不到返回 (0,0)。
func (m *Matcher) Find(data []byte, cur int) (distance, length int) {
	if cur < MinMatch-1 || cur+MinMatch > len(data) {
		return 0, 0
	}
	h := hash3(data[cur], data[cur+1], data[cur+2])
	p := m.head[h]
	best := MinMatch - 1
	bestDist := 0
	for c := 0; p >= 0 && c < m.maxChain; c++ {
		m.probes++
		d := cur - p
		if d <= 0 || d > m.w.Len() || d > m.w.Cap() {
			break // 槽位已被滑出窗口的新位置复用（脏链），链在此终止
		}
		l := m.compare(data, cur, d)
		if l > best {
			best, bestDist = l, d
			if l == len(data)-cur {
				break
			}
		}
		p = m.prevAt[p%len(m.prevAt)]
	}
	return bestDist, best
}

// compare 比较 data[cur:] 与距离 d 的历史；d<l 时从 data 自身取回
// 刚“复制”出的字节，与解压端逐字节复制语义一致。
func (m *Matcher) compare(data []byte, cur, d int) int {
	max := len(data) - cur
	if max > m.w.Cap() {
		max = m.w.Cap()
	}
	k := 0
	for k < max && data[cur-d+k] == data[cur+k] {
		k++
	}
	return k
}

// Probes 返回累计考察过的候选位置总数。
func (m *Matcher) Probes() int { return m.probes }

// Reset 清空哈希链与计数（窗口由调用方另行 Reset）。
func (m *Matcher) Reset() {
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prevAt {
		m.prevAt[i] = -1
	}
	m.probes = 0
}
