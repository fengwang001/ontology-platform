package schema

import (
	"math"
	"sync"
)

// Hist 是一个具名直方图快照值。
type Hist struct {
	Name   string
	Bounds []int64
	Counts []uint64
	Sum    int64
}

// Valid 检查 Hist 是否满足全部输入约束。
func (h Hist) Valid() bool {
	if !validBounds(h.Bounds) || len(h.Counts) != len(h.Bounds)+1 || h.Sum < 0 {
		return false
	}
	var sum uint64
	for _, c := range h.Counts {
		if math.MaxUint64-c < sum {
			return false
		}
		sum += c
		if sum > math.MaxInt64 {
			return false
		}
	}
	return true
}

// bucketIndex 返回 v 落入的桶下标：桶 i 收纳 Bounds[i-1] < v <= Bounds[i]。
func bucketIndex(bounds []int64, v int64) int {
	lo, hi := 0, len(bounds)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if v <= bounds[mid] {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// Collector 按登记的边界版本采集观测值。
type Collector struct {
	mu     sync.Mutex
	name   string
	bounds []int64
	counts []uint64
	sum    int64
}

// NewCollector 为指定名字与版本创建采集器。
func NewCollector(reg *Registry, name string, version int) (*Collector, error) {
	if reg == nil {
		return nil, ErrInvalidArgument
	}
	bounds, err := reg.Bounds(name, version)
	if err != nil {
		return nil, err
	}
	return &Collector{
		name:   name,
		bounds: bounds,
		counts: make([]uint64, len(bounds)+1),
	}, nil
}

// Observe 记录一个观测值。
func (c *Collector) Observe(v int64) error {
	if v < 0 || v > maxBoundVal {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sum > math.MaxInt64-v {
		return ErrOverflow
	}
	c.counts[bucketIndex(c.bounds, v)]++
	c.sum += v
	return nil
}

// Snapshot 返回与某个已完成 Observe 前缀一致的独立副本。
func (c *Collector) Snapshot() Hist {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Hist{
		Name:   c.name,
		Bounds: append([]int64(nil), c.bounds...),
		Counts: append([]uint64(nil), c.counts...),
		Sum:    c.sum,
	}
}
