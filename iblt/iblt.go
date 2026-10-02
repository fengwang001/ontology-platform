// Package iblt 实现可逆布隆查找表（Invertible Bloom Lookup Table），
// 用于两个副本键集合的集合对账：各自写入草图、相减后剥离出各自独有的键。
package iblt

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

const (
	mixMul1   = 0xBF58476D1CE4E5B9
	mixMul2   = 0x94D049BB133111EB
	posSalt   = 0x9E3779B97F4A7C15
	checkSalt = 0xD6E8FEB86659FD93
)

var (
	// ErrInvalidSize 表示构造时 m 不是 3 的正整数倍。
	ErrInvalidSize = errors.New("iblt: m 必须是 3 的正整数倍")
	// ErrSizeMismatch 表示 Subtract 的两个草图格子数不同。
	ErrSizeMismatch = errors.New("iblt: 两个草图的格子数 m 不同")
	// ErrNegativeLimit 表示 Decode 的 limit 为负。
	ErrNegativeLimit = errors.New("iblt: Decode 的 limit 不能为负")
	// ErrLimitExceeded 表示剥离过程中即将记入第 limit+1 个键。
	ErrLimitExceeded = errors.New("iblt: 差异键数量超出 limit")
	// ErrNotDecodable 表示剥离结束后仍有非零格子，或同一键被重复剥离。
	ErrNotDecodable = errors.New("iblt: 草图不可解码")
)

// mix 为 splitmix64 最终混淆函数，乘法按 uint64 回绕。
func mix(z uint64) uint64 {
	z ^= z >> 30
	z *= mixMul1
	z ^= z >> 27
	z *= mixMul2
	z ^= z >> 31
	return z
}

// checksum 即 g(x)。
func checksum(x uint64) uint64 {
	return mix(x ^ checkSalt)
}

// positions 返回键 x 的三个格子下标：
// 第 i 段（i=0,1,2）内偏移为 mix(x+(i+1)*posSalt) mod seg。
func positions(x uint64, seg int) [3]int {
	var pos [3]int
	for i := 0; i < 3; i++ {
		off := mix(x+uint64(i+1)*posSalt) % uint64(seg)
		pos[i] = i*seg + int(off)
	}
	return pos
}

// cell 为草图的一个格子。
type cell struct {
	count    int64
	keyXor   uint64
	checkXor uint64
}

// Sketch 为 IBLT 草图，所有方法可并发调用。
type Sketch struct {
	id    uint64
	m     int
	seg   int // m/3
	mu    sync.RWMutex
	cells []cell
}

var sketchID atomic.Uint64

// New 构造有 m 个格子的草图，m 必须为 3 的正整数倍。
func New(m int) (*Sketch, error) {
	if m <= 0 || m%3 != 0 {
		return nil, fmt.Errorf("%w: m=%d", ErrInvalidSize, m)
	}
	return &Sketch{
		id:    sketchID.Add(1),
		m:     m,
		seg:   m / 3,
		cells: make([]cell, m),
	}, nil
}

// Size 返回格子数 m。
func (s *Sketch) Size() int {
	return s.m
}

// applyLocked 对 x 的三个位置各令计数加 delta、键异或 x、校验异或 g(x)。
// 调用方必须持有写锁。
func (s *Sketch) applyLocked(x uint64, delta int64) {
	g := checksum(x)
	for _, p := range positions(x, s.seg) {
		c := &s.cells[p]
		c.count += delta
		c.keyXor ^= x
		c.checkXor ^= g
	}
}

// Add 把键 x 写入草图（计数 +1）。
func (s *Sketch) Add(x uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyLocked(x, 1)
}

// Remove 把键 x 从草图注销（计数 -1），不检查键是否存在。
func (s *Sketch) Remove(x uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyLocked(x, -1)
}

// snapshot 在 RLock 下复制全部格子。
func (s *Sketch) snapshot() []cell {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]cell, len(s.cells))
	copy(out, s.cells)
	return out
}

// lockPair 按 id 顺序对两个不同草图加读锁，避免死锁。
func lockPair(a, b *Sketch) (unlock func()) {
	if a.id < b.id {
		a.mu.RLock()
		b.mu.RLock()
	} else {
		b.mu.RLock()
		a.mu.RLock()
	}
	return func() {
		a.mu.RUnlock()
		b.mu.RUnlock()
	}
}

// Subtract 返回新草图 a-b：逐格计数相减、键异或与校验异或分别相异或。
// 两个草图 m 不同则整体拒绝且不产生结果；a == b 合法，得到全零草图。
func Subtract(a, b *Sketch) (*Sketch, error) {
	if a.m != b.m {
		return nil, fmt.Errorf("%w: %d != %d", ErrSizeMismatch, a.m, b.m)
	}
	var ca, cb []cell
	if a == b {
		ca = a.snapshot()
		cb = ca
	} else {
		unlock := lockPair(a, b)
		ca = make([]cell, len(a.cells))
		copy(ca, a.cells)
		cb = make([]cell, len(b.cells))
		copy(cb, b.cells)
		unlock()
	}
	out := &Sketch{
		id:    sketchID.Add(1),
		m:     a.m,
		seg:   a.seg,
		cells: make([]cell, a.m),
	}
	for i := range out.cells {
		out.cells[i] = cell{
			count:    ca[i].count - cb[i].count,
			keyXor:   ca[i].keyXor ^ cb[i].keyXor,
			checkXor: ca[i].checkXor ^ cb[i].checkXor,
		}
	}
	return out, nil
}

// Decode 在草图的副本上剥离纯格子，返回仅 a 侧与仅 b 侧的升序键列表。
// 错误优先级：limit 为负最先判定；剥离过程中同一键重复出现按不可解码处理
// （先于超出上限判定）；即将记入第 limit+1 个键时报超出上限。
// Decode 不改变草图本身。
func (s *Sketch) Decode(limit int) (onlyA, onlyB []uint64, err error) {
	if limit < 0 {
		return nil, nil, fmt.Errorf("%w: limit=%d", ErrNegativeLimit, limit)
	}
	work := s.snapshot()

	seen := make(map[uint64]struct{})
	for {
		idx := -1
		for i := range work {
			c := &work[i]
			if (c.count == 1 || c.count == -1) && checksum(c.keyXor) == c.checkXor {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		x := work[idx].keyXor
		if _, dup := seen[x]; dup {
			return nil, nil, fmt.Errorf("%w: 键 %d 被重复剥离", ErrNotDecodable, x)
		}
		if len(onlyA)+len(onlyB) >= limit {
			return nil, nil, fmt.Errorf("%w: limit=%d", ErrLimitExceeded, limit)
		}
		seen[x] = struct{}{}
		var delta int64
		if work[idx].count == 1 {
			onlyA = append(onlyA, x)
			delta = -1 // Remove 的效果
		} else {
			onlyB = append(onlyB, x)
			delta = 1 // Add 的效果
		}
		g := checksum(x)
		for _, p := range positions(x, s.seg) {
			c := &work[p]
			c.count += delta
			c.keyXor ^= x
			c.checkXor ^= g
		}
	}
	for i := range work {
		c := &work[i]
		if c.count != 0 || c.keyXor != 0 || c.checkXor != 0 {
			return nil, nil, fmt.Errorf("%w: 格子 %d 残留 (count=%d)", ErrNotDecodable, i, c.count)
		}
	}
	sort.Slice(onlyA, func(i, j int) bool { return onlyA[i] < onlyA[j] })
	sort.Slice(onlyB, func(i, j int) bool { return onlyB[i] < onlyB[j] })
	return onlyA, onlyB, nil
}
