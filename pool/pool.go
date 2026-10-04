// Package pool 维护端点生命周期（活跃/排空/移除）与在途账本。
//
// 不变量：排空中端点不进候选集；在途数不超过上限 M；
// 排空中端点在途归零时立即移除并释放 Nmax 名额。
// 不是并发安全的，由上层（picker 的单锁）串行化访问。
package pool

import (
	"errors"
	"sort"

	"ontology/ewma"
)

var (
	ErrExists   = errors.New("pool: endpoint already exists")
	ErrFull     = errors.New("pool: endpoint count reaches Nmax")
	ErrNotFound = errors.New("pool: endpoint not found")
	ErrDraining = errors.New("pool: endpoint is draining")
)

// Endpoint 是单个后端端点的账本与估计状态。
type Endpoint struct {
	ID       string
	Inflight int
	Draining bool
	Est      ewma.Estimator
}

// Pool 是端点集合。m 为单端点在途上限，nmax 为端点数上限（含排空中）。
type Pool struct {
	m    int
	nmax int
	eps  map[string]*Endpoint
}

// New 创建空池。m ∈ [1,1e6]，nmax ∈ [1,1e5]，由上层校验。
func New(m, nmax int) *Pool {
	return &Pool{m: m, nmax: nmax, eps: make(map[string]*Endpoint)}
}

// Add 新增活跃端点。已存在（含排空中）报 ErrExists，达 Nmax 报 ErrFull。
func (p *Pool) Add(id string) error {
	if _, ok := p.eps[id]; ok {
		return ErrExists
	}
	if len(p.eps) >= p.nmax {
		return ErrFull
	}
	p.eps[id] = &Endpoint{ID: id}
	return nil
}

// Remove 摘除端点：不存在报 ErrNotFound；在途为 0 立即移除；
// 否则转排空；已在排空中报 ErrDraining。
func (p *Pool) Remove(id string) error {
	ep, ok := p.eps[id]
	if !ok {
		return ErrNotFound
	}
	if ep.Inflight == 0 {
		delete(p.eps, id)
		return nil
	}
	if ep.Draining {
		return ErrDraining
	}
	ep.Draining = true
	return nil
}

// Get 返回端点，不存在返回 nil。
func (p *Pool) Get(id string) *Endpoint { return p.eps[id] }

// Len 返回端点数（含排空中）。
func (p *Pool) Len() int { return len(p.eps) }

// HasActive 报告是否存在活跃（非排空）端点。
func (p *Pool) HasActive() bool {
	for _, ep := range p.eps {
		if !ep.Draining {
			return true
		}
	}
	return false
}

// Eligible 返回候选集：活跃且在途数 < M 的端点，按 id 字节序排列。
func (p *Pool) Eligible() []*Endpoint {
	elig := make([]*Endpoint, 0, len(p.eps))
	for _, ep := range p.eps {
		if !ep.Draining && ep.Inflight < p.m {
			elig = append(elig, ep)
		}
	}
	sort.Slice(elig, func(i, j int) bool { return elig[i].ID < elig[j].ID })
	return elig
}

// ReleaseOne 将在途数减 1；若端点排空中且在途归零则移除。
// 调用方保证 ep.Inflight > 0（有未归还票据）。
func (p *Pool) ReleaseOne(ep *Endpoint) {
	ep.Inflight--
	if ep.Draining && ep.Inflight == 0 {
		delete(p.eps, ep.ID)
	}
}
