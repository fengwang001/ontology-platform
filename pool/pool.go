// Package pool 是分配器本体：Alloc/Free、已分配记账、错误与复杂度计数。
package pool

import (
	"errors"
	"fmt"
	"sync"

	"ontology/buddy"
)

// 三类可判定的哨兵错误，互不相同。
var (
	ErrBadSize = errors.New("pool: 请求大小非法")
	ErrFull    = errors.New("pool: 池已满")
	ErrBadFree = errors.New("pool: 非法归还")
)

// Pool 管理 [0, 1<<n) 字节池，一把锁保证并发安全。
type Pool struct {
	mu     sync.Mutex
	fl     *buddy.FreeLists
	alloc  map[int]int // 已分配：偏移 -> 大小
	size   int
	checks int // 最近一次 Alloc/Free 检查过的空闲块记录条数（非导出）
}

// New 建立 2^n 字节池。
func New(n int) *Pool {
	return &Pool{fl: buddy.New(n), alloc: map[int]int{}, size: 1 << n}
}

// orderOf 返回不小于 n 的最小 2 的幂的阶；n 本身是 2 的幂时不变。
func orderOf(n int) (order int) {
	for 1<<order < n {
		order++
	}
	return
}

// Alloc 分配不小于 n 的最小 2 的幂大小的块，返回偏移。
// 失败（ErrBadSize / ErrFull）时不改动任何状态。
func (p *Pool) Alloc(n int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 1 || n > p.size {
		return 0, ErrBadSize
	}
	off, checks, ok := p.fl.Take(orderOf(n))
	p.checks = checks
	if !ok {
		return 0, ErrFull
	}
	p.alloc[off] = 1 << orderOf(n)
	return off, nil
}

// Free 归还以 off 起始的已分配块；off 不是已分配起点时报 ErrBadFree。
func (p *Pool) Free(off int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	size, ok := p.alloc[off]
	if !ok {
		return ErrBadFree
	}
	delete(p.alloc, off)
	p.checks = p.fl.Release(off, orderOf(size))
	return nil
}

// FreeList 返回当前空闲块升序列表。
func (p *Pool) FreeList() []buddy.Block {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fl.Blocks()
}

// SelfCheck 核验：块结构合法、两两不重叠、守恒、无应合并的空闲伙伴。
func (p *Pool) SelfCheck() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	free := p.fl.Blocks()
	type seg struct{ off, size int }
	segs := make([]seg, 0, len(free)+len(p.alloc))
	total := 0
	seen := map[buddy.Block]bool{}
	for _, b := range free {
		if b.Size <= 0 || b.Size&(b.Size-1) != 0 || b.Off%b.Size != 0 ||
			b.Off < 0 || b.Off+b.Size > p.size {
			return fmt.Errorf("pool: 空闲块结构非法 %+v", b)
		}
		if seen[b] {
			return fmt.Errorf("pool: 空闲块重复 %+v", b)
		}
		seen[b] = true
		segs = append(segs, seg{b.Off, b.Size})
		total += b.Size
	}
	for _, b := range free { // 不应存在两个都空闲的伙伴
		bud := buddy.Block{Off: b.Off ^ b.Size, Size: b.Size}
		if seen[bud] {
			return fmt.Errorf("pool: 存在未合并的空闲伙伴 %+v", b)
		}
	}
	for off, size := range p.alloc {
		segs = append(segs, seg{off, size})
		total += size
	}
	if total != p.size {
		return fmt.Errorf("pool: 不守恒 %d != %d", total, p.size)
	}
	for i := 0; i < len(segs); i++ { // 两两不重叠
		for j := i + 1; j < len(segs); j++ {
			if segs[i].off < segs[j].off+segs[j].size && segs[j].off < segs[i].off+segs[i].size {
				return fmt.Errorf("pool: 块重叠 %+v %+v", segs[i], segs[j])
			}
		}
	}
	return nil
}
