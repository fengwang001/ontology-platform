// Package api 是对外门面。
package api

import (
	"ontology/buddy"
	"ontology/pool"
)

// Block 是一个空闲块（偏移，大小）。
type Block = buddy.Block

// 对外暴露的三类可判定错误。
var (
	ErrBadSize = pool.ErrBadSize
	ErrFull    = pool.ErrFull
	ErrBadFree = pool.ErrBadFree
)

// Allocator 是伙伴空闲块分配器。
type Allocator struct {
	p *pool.Pool
}

// New 建立管理 [0, 2^N) 的分配器。
func New(n int) *Allocator {
	return &Allocator{p: pool.New(n)}
}

// Alloc 分配并返回偏移。
func (a *Allocator) Alloc(n int) (int, error) {
	return a.p.Alloc(n)
}

// Free 归还以 off 起始的已分配块。
func (a *Allocator) Free(off int) error {
	return a.p.Free(off)
}

// FreeList 返回当前空闲块升序列表。
func (a *Allocator) FreeList() []Block {
	return a.p.FreeList()
}

// SelfCheck 核验不变量。
func (a *Allocator) SelfCheck() error {
	return a.p.SelfCheck()
}
