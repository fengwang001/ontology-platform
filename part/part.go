// Package part 定义单个分区：左闭右开区间 [lo,hi) 与其持有的键集合，
// 以及分裂中点计算与合并条件判定。不依赖其他包。
package part

import "slices"

// Range 是分区区间与负载的只读快照（值类型，可安全跨包传递）。
type Range struct {
	Lo, Hi int64
	Load   int
}

// Part 是一个左闭右开区间 [lo,hi) 及其当前持有的键（精确集合）。
type Part struct {
	lo, hi int64
	keys   map[int64]struct{}
}

// New 构造空分区 [lo,hi)，调用方保证 lo < hi。
func New(lo, hi int64) *Part {
	return &Part{lo: lo, hi: hi, keys: map[int64]struct{}{}}
}

func (p *Part) Lo() int64 { return p.lo }
func (p *Part) Hi() int64 { return p.hi }

// Load 即分区当前持有的键个数。
func (p *Part) Load() int { return len(p.keys) }

// Contains 报告 key 是否落在区间 [lo,hi) 内。
func (p *Part) Contains(key int64) bool { return p.lo <= key && key < p.hi }

// Has 报告 key 是否被本分区持有。
func (p *Part) Has(key int64) bool {
	_, ok := p.keys[key]
	return ok
}

// Add 把 key 放入分区；调用方保证 Contains(key)。
func (p *Part) Add(key int64) { p.keys[key] = struct{}{} }

// Take 把 key 从分区移除；调用方保证 Has(key)。
func (p *Part) Take(key int64) { delete(p.keys, key) }

// Mid 返回分裂中点 lo+(hi-lo)/2（整数除法，向下取整）。
func (p *Part) Mid() int64 { return p.lo + (p.hi-p.lo)/2 }

// Split 在中点把 p 一分为二，返回 (左 [lo,mid), 右 [mid,hi))：
// key < mid 归左，key >= mid 归右（键恰好等于 mid 归右）。p 本身不变。
func (p *Part) Split() (left, right *Part) {
	mid := p.Mid()
	left, right = New(p.lo, mid), New(mid, p.hi)
	for k := range p.keys {
		if k < mid {
			left.keys[k] = struct{}{}
		} else {
			right.keys[k] = struct{}{}
		}
	}
	return left, right
}

// Merge 把相邻的右邻 next 并入 p：p 变为 [p.lo, next.hi)，键取并集。
func (p *Part) Merge(next *Part) {
	for k := range next.keys {
		p.keys[k] = struct{}{}
	}
	p.hi = next.hi
}

// Mergeable 报告 p 与 next 是否相邻且合计 load 不超过 mergeThreshold。
func (p *Part) Mergeable(next *Part, mergeThreshold int) bool {
	return p.hi == next.lo && p.Load()+next.Load() <= mergeThreshold
}

// Keys 返回升序的键列表（供自检与测试核对）。
func (p *Part) Keys() []int64 {
	out := make([]int64, 0, len(p.keys))
	for k := range p.keys {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
