// Package org 维护员工（非空字节串）的上级关系与审批额度。
package org

import (
	"errors"
	"sync"
)

// MaxEmployees 是已知员工数上限。
const MaxEmployees = 100_000

// MaxLimit 是审批额度上限。
const MaxLimit = 1_000_000_000_000

var (
	// ErrInvalid 参数非法（空员工名等）。
	ErrInvalid = errors.New("org: invalid argument")
	// ErrCycle 设置上级会导致自环或上级链成环。
	ErrCycle = errors.New("org: manager cycle")
)

// Org 保存员工上级图与额度，可并发使用。
type Org struct {
	mu       sync.RWMutex
	managers map[string]string
	limits   map[string]int64
}

// New 创建空组织。
func New() *Org {
	return &Org{
		managers: make(map[string]string),
		limits:   make(map[string]int64),
	}
}

// SetManager 设置 e 的上级为 m；m 为空串表示无上级。
func (o *Org) SetManager(e, m string) error {
	if e == "" {
		return ErrInvalid
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if m != "" {
		if e == m {
			return ErrCycle
		}
		_, eKnown := o.managers[e]
		_, mKnown := o.managers[m]
		limit := MaxEmployees
		if !eKnown {
			limit--
		}
		if !mKnown && len(o.managers) >= limit {
			return ErrInvalid
		}
		if o.createsCycle(e, m) {
			return ErrCycle
		}
	} else if _, known := o.managers[e]; !known && len(o.managers) >= MaxEmployees {
		return ErrInvalid
	}
	o.managers[e] = m
	if m != "" {
		if _, known := o.managers[m]; !known {
			o.managers[m] = ""
		}
	}
	return nil
}

// SetLimit 设置 e 的审批额度 x（0..1e12，默认 0）。
func (o *Org) SetLimit(e string, x int64) error {
	if e == "" || x < 0 || x > MaxLimit {
		return ErrInvalid
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, known := o.limits[e]; !known {
		if _, mknown := o.managers[e]; !mknown && len(o.managers) >= MaxEmployees {
			return ErrInvalid
		}
	}
	o.limits[e] = x
	if _, known := o.managers[e]; !known {
		o.managers[e] = ""
	}
	return nil
}

// Manager 返回 e 的上级；无上级时返回空串。
func (o *Org) Manager(e string) string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.managers[e]
}

// Limit 返回 e 当前审批额度（未设置为 0）。
func (o *Org) Limit(e string) int64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.limits[e]
}

// Chain 返回从 e 的直接上级开始沿上级链的成员序列（不含 e）。
func (o *Org) Chain(e string) []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	chain := make([]string, 0)
	seen := map[string]bool{e: true}
	cur := o.managers[e]
	for cur != "" && !seen[cur] {
		chain = append(chain, cur)
		seen[cur] = true
		cur = o.managers[cur]
	}
	return chain
}

// Count 返回已知员工数量。
func (o *Org) Count() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return len(o.managers)
}

// createsCycle 判断在锁内把 e 的上级设为 m 是否成环：
// 从 m 沿上级链能到达 e 即成环。
func (o *Org) createsCycle(e, m string) bool {
	cur := m
	seen := map[string]bool{}
	for cur != "" {
		if cur == e {
			return true
		}
		if seen[cur] {
			return false
		}
		seen[cur] = true
		cur = o.managers[cur]
	}
	return false
}
