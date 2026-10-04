package pubpool

import (
	"errors"
	"math/bits"
	"sync"
)

var ErrInvalidArgument = errors.New("pubpool: invalid argument")

// Pool 管公网地址几何、排空状态与每地址的块占用位图。
// 块的占用/空闲在块粒度（非端口粒度）：被任一订户持有的块即为占用。
type Pool struct {
	mu            sync.RWMutex
	a, l, h, s, k int
	drained       []bool
	used          [][]uint64
}

func New(a, l, h, s int) (*Pool, error) {
	if a < 1 || a > 4096 ||
		l < 1024 || h > 65535 || l > h ||
		s < 1 || (h-l+1)%s != 0 {
		return nil, ErrInvalidArgument
	}
	k := (h - l + 1) / s
	p := &Pool{
		a:       a,
		l:       l,
		h:       h,
		s:       s,
		k:       k,
		drained: make([]bool, a),
		used:    make([][]uint64, a),
	}
	words := (k + 63) / 64
	for i := range p.used {
		p.used[i] = make([]uint64, words)
	}
	return p, nil
}

func (p *Pool) A() int { return p.a }
func (p *Pool) L() int { return p.l }
func (p *Pool) H() int { return p.h }
func (p *Pool) S() int { return p.s }
func (p *Pool) K() int { return p.k }

// BlockRange 返回块 j 的端口区间 [lo,hi]。
func (p *Pool) BlockRange(j int) (int, int) {
	lo := p.l + j*p.s
	return lo, lo + p.s - 1
}

// BlockOf 返回 port 所在块号。
func (p *Pool) BlockOf(port int) int {
	return (port - p.l) / p.s
}

// Drain 把地址置为排空，重复排空为空操作。
func (p *Pool) Drain(addr int) error {
	if addr < 0 || addr >= p.a {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drained[addr] = true
	return nil
}

func (p *Pool) Drained(addr int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.drained[addr]
}

// MarkUsed / MarkFree / IsUsed / FirstFree 供上层在自己的临界区内串行调用；
// 位图方法本身无锁，锁由 blockalloc 的全局互斥覆盖。
func (p *Pool) MarkUsed(addr, j int) {
	p.used[addr][j>>6] |= 1 << uint(j&63)
}

func (p *Pool) MarkFree(addr, j int) {
	p.used[addr][j>>6] &^= 1 << uint(j&63)
}

func (p *Pool) IsUsed(addr, j int) bool {
	return p.used[addr][j>>6]&(1<<uint(j&63)) != 0
}

// FirstFree 返回 addr 上编号最小的空闲块；无则返回 -1。
func (p *Pool) FirstFree(addr int) int {
	words := (p.k + 63) / 64
	for w := 0; w < words; w++ {
		v := p.used[addr][w]
		if v == ^uint64(0) {
			continue
		}
		bit := bits.TrailingZeros64(^v)
		j := w*64 + bit
		if j < p.k {
			return j
		}
	}
	return -1
}
