package schema

import (
	"math"
	"sort"
	"sync"
)

// Hist 是一个直方图值。Counts 长度为 len(Bounds)+1，
// 第 i 个桶（i < len(Bounds)）收纳 Bounds[i-1] < v <= Bounds[i]（Bounds[-1] 视为 -1），
// 最后一个桶为溢出桶（v 大于最后一个边界）。
type Hist struct {
	Name   string
	Bounds []int64
	Counts []int64
	Sum    int64
}

// Validate 校验直方图合法性，非法返回 ErrInvalid。
func (h *Hist) Validate() error {
	if h == nil || !validBounds(h.Bounds) || len(h.Counts) != len(h.Bounds)+1 {
		return ErrInvalid
	}
	total := int64(0)
	for _, c := range h.Counts {
		if c < 0 {
			return ErrInvalid
		}
		if total > math.MaxInt64-c {
			return ErrInvalid // 计数之和超过 int64
		}
		total += c
	}
	if h.Sum < 0 {
		return ErrInvalid
	}
	return nil
}

// Collector 按注册表的某个版本采集观测值，并发安全。
type Collector struct {
	mu     sync.Mutex
	name   string
	bounds []int64
	counts []int64
	sum    int64
}

// NewCollector 创建采集器；名字或版本不存在返回 ErrNotFound。
func NewCollector(reg *Registry, name string, version int) (*Collector, error) {
	bounds, err := reg.Bounds(name, version)
	if err != nil {
		return nil, err
	}
	return &Collector{
		name:   name,
		bounds: bounds,
		counts: make([]int64, len(bounds)+1),
	}, nil
}

// Observe 记录一个观测值 v（0 到 10^12）。
// Sum 将超出 int64 时返回 ErrOverflow 且不改任何状态。
func (c *Collector) Observe(v int64) error {
	if v < 0 || v > maxBound {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sum > math.MaxInt64-v {
		return ErrOverflow
	}
	i := sort.Search(len(c.bounds), func(i int) bool { return v <= c.bounds[i] })
	c.counts[i]++
	c.sum += v
	return nil
}

// Snapshot 返回当前状态的独立副本。
func (c *Collector) Snapshot() Hist {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Hist{
		Name:   c.name,
		Bounds: append([]int64(nil), c.bounds...),
		Counts: append([]int64(nil), c.counts...),
		Sum:    c.sum,
	}
}
