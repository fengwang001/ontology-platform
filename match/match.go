package match

import "errors"

var ErrZeroChain = errors.New("match: chain limit must be > 0")

const hashBits = 16

// Matcher 是哈希链匹配器：head 记每个哈希值最新位置，prev 是容量与窗口
// 相同的环形链，链上至多回溯 chainLimit 个候选。
type Matcher struct {
	chain    int
	head     []int32
	prev     []int32
	exam     int64
	maxDist  int
	src      []byte
}

func New(windowCap, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrZeroChain
	}
	m := &Matcher{
		chain: chainLimit,
		head:  make([]int32, 1<<hashBits),
		prev:  make([]int32, windowCap),
		maxDist: windowCap,
	}
	for i := range m.head {
		m.head[i] = -1
	}
	return m, nil
}

// Candidates 返回自构造以来考察过的候选位置总数。
func (m *Matcher) Candidates() int64 { return m.exam }

func (m *Matcher) ResetCandidates() { m.exam = 0 }

func hash3(b0, b1, b2 byte) int {
	v := uint32(b0) | uint32(b1)<<8 | uint32(b2)<<16
	return int((v * 2654435761) >> (32 - hashBits))
}

// Start 绑定源切片：src = 预置字典 + 本块原文，dictLen 为字典长度。
// 随后 Insert(pos) 登记字典中所有可作为引用起点的位置。
func (m *Matcher) Start(src []byte, dictLen int) {
	m.src = src
	for i := range m.head {
		m.head[i] = -1
	}
	for p := 0; p+2 < dictLen; p++ {
		m.insertAt(p)
	}
	m.ResetCandidates()
}

func (m *Matcher) insertAt(pos int) {
	h := hash3(m.src[pos], m.src[pos+1], m.src[pos+2])
	p := int32(pos)
	m.prev[int64(p)%int64(len(m.prev))] = m.head[h]
	m.head[h] = p
}

// Insert 登记一个本块新位置。
func (m *Matcher) Insert(pos int) { m.insertAt(pos) }

// Find 为 src[pos:] 找最长匹配；距离上限为窗口容量，
// 候选链最多考察 chainLimit 个节点。返回 (距离, 长度)，长度 <3 表示无匹配。
func (m *Matcher) Find(pos int) (distance, length int) {
	maxLen := len(m.src) - pos
	if maxLen < 3 {
		return 0, 0
	}
	h := hash3(m.src[pos], m.src[pos+1], m.src[pos+2])
	cand := m.head[h]
	cur := pos
	for n := 0; n < m.chain && cand >= 0; n++ {
		d := int64(cur) - int64(cand)
		if d <= 0 || d > int64(m.maxDist) {
			break
		}
		m.exam++
		l := 0
		for l < maxLen && int(cand)+l < pos {
			if m.src[int(cand)+l] != m.src[pos+l] {
				break
			}
			l++
		}
		if l > length {
			distance, length = int(d), l
			if l == maxLen {
				break
			}
		}
		cand = m.prev[int64(cand)%int64(len(m.prev))]
	}
	return distance, length
}
