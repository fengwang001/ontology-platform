package match

import (
	"errors"

	"ontology/window"
)

// MaxMatch 是单条回指的最大长度；也是编码器挂起区的上限。
const MaxMatch = 16384

// MinMatch 是允许回指的最短长度。
const MinMatch = 3

const hashSize = 16384

// ErrBadConfig 在链长上限非正时由 New 返回。
var ErrBadConfig = errors.New("match: chain limit must be positive")

func hash3(p []byte) uint32 {
	return (uint32(p[0])<<10 ^ uint32(p[1])<<5 ^ uint32(p[2])) & (hashSize - 1)
}

// Matcher 是窗口内的哈希链最长匹配器。
// 链表数组以绝对位置对窗口容量取模定位槽位；桶/链值存“绝对位置+1”，0 表示空。
type Matcher struct {
	win      *window.Window
	head     []int64
	next     []int64
	chainCap int
	examined int64
}

// New 创建匹配器。
func New(win *window.Window, chainCap int) (*Matcher, error) {
	if chainCap <= 0 {
		return nil, ErrBadConfig
	}
	c := win.Cap()
	return &Matcher{
		win:      win,
		head:     make([]int64, hashSize),
		next:     make([]int64, c),
		chainCap: chainCap,
	}, nil
}

// Examined 返回考察过的候选位置总数（非导出计数的读口）。
func (m *Matcher) Examined() int64 { return m.examined }

func (m *Matcher) fetch(total, pos int64, data []byte) byte {
	if idx := pos - (total - int64(len(data))); idx >= 0 {
		return data[idx]
	}
	return m.win.Byte(int(total - pos))
}

// Find 在 data[offset:] 起点查找最长回指。
// total = 窗口历史字节数 + len(data)；返回 (距离, 长度)，长度<3 表示无匹配。
func (m *Matcher) Find(data []byte, total int64, offset int) (int, int) {
	if len(data)-offset < MinMatch {
		return 0, 0
	}
	bestLen, bestDist := MinMatch-1, 0
	limit := total - int64(m.win.Cap())
	if limit < 0 {
		limit = 0
	}
	cand := m.head[hash3(data[offset:])] - 1
	for k := 0; k < m.chainCap && cand >= limit; k++ {
		if cand < 0 {
			break
		}
		m.examined++
		ml := MaxMatch
		if max := len(data) - offset; max < ml {
			ml = max
		}
		l := 0
		for l < ml && m.fetch(total, cand+int64(l), data) == data[offset+l] {
			l++
		}
		if l > bestLen {
			bestLen, bestDist = l, int(total-cand)
			if l == MaxMatch || l == len(data)-offset {
				break
			}
		}
		cand = m.next[uint64(cand)%uint64(len(m.next))] - 1
	}
	if bestLen < MinMatch {
		return 0, 0
}
	return bestDist, bestLen
}

// Insert 把绝对位置 abs 的 3 字节串登记进哈希链（数据由 data 给出）。
func (m *Matcher) Insert(data []byte, dataBase, abs int64) {
	i := int(abs - dataBase)
	if i < 0 || i+2 >= len(data) {
		return
	}
	h := hash3(data[i:])
	slot := uint64(abs) % uint64(len(m.next))
	m.next[slot] = m.head[h]
	m.head[h] = abs + 1
}
