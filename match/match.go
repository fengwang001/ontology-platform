// Package match 是基于滑动窗口的哈希链 LZ77 匹配器。仅依赖 window。
package match

import (
	"errors"

	"ontology/window"
)

// ErrInvalidConfig 在链长上限非法时返回。
var ErrInvalidConfig = errors.New("match: maxChain must be > 0")

const hashBits = 16

// Matcher 在 window 中维护每个 3 字节哈希的候选位置链。
type Matcher struct {
	win     *window.Window
	head    []int64 // 哈希 -> 最近位置（-1 表示无）
	prev    []int64 // 按位置取模的链环 -> 上一同哈希位置（-1 表示链尾）
	maxC    int
	pos     int64
	examined int64
}

// New 创建匹配器。win 提供历史字节，maxChain 限制每位置下探候选数。
func New(win *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrInvalidConfig
	}
	c := win.Cap()
	m := &Matcher{
		win:   win,
		head:  make([]int64, 1<<hashBits),
		prev:  make([]int64, c+1),
		maxC:  maxChain,
		pos:   0,
	}
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	return m, nil
}

func hash3(b0, b1, b2 byte) int {
	h := uint32(b0)<<16 ^ uint32(b1)<<8 ^ uint32(b2)
	h ^= h >> hashBits
	return int(h & (1<<hashBits - 1))
}

// AddByte 把一个字节（及其形成的 3 元组）加入索引，并同步窗口。
func (m *Matcher) AddByte(b byte) {
	n := int64(m.win.Len())
	if n >= 2 {
		h := hash3(m.win.At(2), m.win.At(1), b)
		slot := (m.pos + 1) % int64(len(m.prev))
		if old := m.head[h]; old >= 0 && m.pos-old < int64(len(m.prev)) {
			m.prev[slot] = old
		} else {
			m.prev[slot] = -1
		}
		m.head[h] = m.pos
	}
	m.win.Add(b)
	m.pos++
}

// Find 在历史中查找 data[start:start+maxLen] 的最长匹配。
// 返回距离（0 表示无匹配）与长度；minLen 以下不视为匹配。
func (m *Matcher) Find(data []byte, start, minLen, maxLen int) (dist, length int) {
	if len(data)-start < minLen || minLen < 3 || m.win.Len() < 3 {
		return 0, 0
	}
	h := hash3(data[start], data[start+1], data[start+2])
	limit := m.pos - int64(m.win.Len()) // 窗口中最老有效位置
	cand := m.head[h]
	for c := 0; c < m.maxC && cand >= limit; c++ {
		m.examined++
		d := int(m.pos - cand)
		if d >= 1 && d <= m.win.Len() {
			l := 0
			maxl := len(data) - start
			if maxl > maxLen {
				maxl = maxLen
			}
			for ; l < maxl; l++ {
				var hb byte
				if l < d {
					hb = m.win.At(d - l)
				} else {
					hb = data[start+l-d] // 重叠延长：取当前输入中已匹配部分
				}
				if hb != data[start+l] {
					break
				}
			}
			if l > length {
				dist, length = d, l
				if l >= maxl {
					break
				}
			}
		}
		if cand = m.prev[cand%int64(len(m.prev))]; cand < limit {
			break
		}
	}
	return dist, length
}

// Examined 返回累计考察过的候选位置总数。
func (m *Matcher) Examined() int64 { return m.examined }

// Pos 返回已加入索引的总字节数。
func (m *Matcher) Pos() int64 { return m.pos }

// Rebase 用 dict 末尾窗口容量以内的字节预置窗口并建索引（并行块字典）。
func (m *Matcher) Rebase(dict []byte) {
	m.win.Prefill(dict)
	kept := m.win.Len()
	saved := make([]byte, 0, kept)
	for d := kept; d >= 1; d-- {
		saved = append(saved, m.win.At(d))
	}
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	m.win.Reset()
	m.pos = 0
	for _, b := range saved {
		m.AddByte(b)
	}
}
