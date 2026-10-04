// Package dag 维护数据集注册表：名字、截止偏移、处理时长与父依赖。
// 父数据集必须先注册，因此注册序即拓扑序，图天然无环。
package dag

import (
	"errors"
	"fmt"
)

const (
	// MaxT 是周期长度 T 的上界（秒）。
	MaxT = int64(1_000_000)
	// MaxDatasets 是数据集总数上限。
	MaxDatasets = 10_000
	// MaxParents 是单个数据集的父数量上限。
	MaxParents = 8
)

var (
	ErrInvalid       = errors.New("dag: invalid argument")
	ErrFrozen        = errors.New("dag: graph frozen after first accepted land/evaluate")
	ErrExists        = errors.New("dag: dataset already registered")
	ErrNoSuchParent  = errors.New("dag: parent dataset not registered")
	ErrTooMany       = errors.New("dag: dataset count limit exceeded")
	ErrNoSuchDataset = errors.New("dag: dataset not registered")
)

// Dataset 是一个已登记的数据集。
type Dataset struct {
	Name    string
	Off     int64
	Dur     int64
	Parents []string
}

// Graph 是数据集依赖图（注册表）。
type Graph struct {
	T      int64
	frozen bool
	order  []string
	byName map[string]*Dataset
}

// New 创建周期长度为 T 的空图。
func New(T int64) (*Graph, error) {
	if T < 1 || T > MaxT {
		return nil, fmt.Errorf("%w: T=%d out of [1,%d]", ErrInvalid, T, MaxT)
	}
	return &Graph{T: T, byName: make(map[string]*Dataset)}, nil
}

// AddDataset 登记一个数据集。
// 拒绝次序：参数非法 > ErrFrozen > 已存在 > 父不存在 > 超限。
func (g *Graph) AddDataset(name string, off, dur int64, parents []string) error {
	if name == "" || off < 1 || off > g.T || dur < 0 || dur > g.T || len(parents) > MaxParents {
		return fmt.Errorf("%w: name=%q off=%d dur=%d parents=%d", ErrInvalid, name, off, dur, len(parents))
	}
	if g.frozen {
		return ErrFrozen
	}
	if _, ok := g.byName[name]; ok {
		return fmt.Errorf("%w: %s", ErrExists, name)
	}
	seen := make(map[string]bool, len(parents))
	dedup := make([]string, 0, len(parents))
	for _, p := range parents {
		if seen[p] {
			continue
		}
		seen[p] = true
		if _, ok := g.byName[p]; !ok {
			return fmt.Errorf("%w: %s", ErrNoSuchParent, p)
		}
		dedup = append(dedup, p)
	}
	if len(g.order) >= MaxDatasets {
		return ErrTooMany
	}
	g.byName[name] = &Dataset{Name: name, Off: off, Dur: dur, Parents: dedup}
	g.order = append(g.order, name)
	return nil
}

// Freeze 冻结注册表，之后 AddDataset 返回 ErrFrozen。
func (g *Graph) Freeze() { g.frozen = true }

// Frozen 报告注册表是否已冻结。
func (g *Graph) Frozen() bool { return g.frozen }

// Get 按名字查找数据集。
func (g *Graph) Get(name string) (*Dataset, bool) {
	ds, ok := g.byName[name]
	return ds, ok
}

// Names 按注册序返回全部数据集名字。
func (g *Graph) Names() []string {
	return append([]string(nil), g.order...)
}

// Count 返回已登记的数据集数量。
func (g *Graph) Count() int { return len(g.order) }
