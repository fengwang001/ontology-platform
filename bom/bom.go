// Package bom 登记父子件关系，维护无环性并提供确定性快照。
package bom

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/stock"
)

// 重导出 stock 的哨兵错误，并定义成环错误。
var (
	ErrInvalid  = stock.ErrInvalid
	ErrNotExist = stock.ErrNotExist
	ErrConflict = stock.ErrConflict
	ErrCycle    = errors.New("component relation forms a cycle")
)

const (
	maxPer   = 10_000
	maxScrap = 999
)

// Component 为一条父子件关系：每件 Parent 耗用 Per 件 Child，损耗率 Scrap 千分数。
type Component struct {
	Parent string
	Child  string
	Per    uint64
	Scrap  uint64
}

// BOM 为父子件关系表，可并发使用。
type BOM struct {
	mu    sync.RWMutex
	st    *stock.Stock
	edges map[string]map[string]Component // parent -> child -> 关系
}

// New 返回基于物料登记表 st 的空关系表。
func New(st *stock.Stock) *BOM {
	return &BOM{st: st, edges: make(map[string]map[string]Component)}
}

// AddComponent 声明每件 parent 耗用 per 件 child（损耗率 scrap 千分数）。
// 拒绝次序：参数非法 > 物料不存在 > 父子关系重复 > 成环（含自环）。
// 被拒绝时不改任何状态。
func (b *BOM) AddComponent(parent, child []byte, per, scrap uint64) error {
	if !stock.ValidItemID(parent) || !stock.ValidItemID(child) ||
		per < 1 || per > maxPer || scrap > maxScrap {
		return fmt.Errorf("bom: AddComponent(%q,%q): %w", parent, child, ErrInvalid)
	}
	if !b.st.Has(parent) || !b.st.Has(child) {
		return fmt.Errorf("bom: AddComponent(%q,%q): %w", parent, child, ErrNotExist)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p, c := string(parent), string(child)
	if _, ok := b.edges[p][c]; ok {
		return fmt.Errorf("bom: AddComponent(%q,%q): %w", parent, child, ErrConflict)
	}
	if b.reachableLocked(c, p) {
		return fmt.Errorf("bom: AddComponent(%q,%q): %w", parent, child, ErrCycle)
	}
	if b.edges[p] == nil {
		b.edges[p] = make(map[string]Component)
	}
	b.edges[p][c] = Component{Parent: p, Child: c, Per: per, Scrap: scrap}
	return nil
}

// reachableLocked 报告从 from 沿父→子边能否到达 target；from==target 视为可达（自环）。
func (b *BOM) reachableLocked(from, target string) bool {
	stack := []string{from}
	seen := map[string]bool{from: true}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == target {
			return true
		}
		for next := range b.edges[cur] {
			if !seen[next] {
				seen[next] = true
				stack = append(stack, next)
			}
		}
	}
	return false
}

// Components 返回全部关系的快照，按（父, 子）字节序排序，与声明次序无关。
func (b *BOM) Components() []Component {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Component, 0, len(b.edges))
	for _, children := range b.edges {
		for _, c := range children {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Parent != out[j].Parent {
			return out[i].Parent < out[j].Parent
		}
		return out[i].Child < out[j].Child
	})
	return out
}
