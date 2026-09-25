// Package check 提供朴素参照实现：维护有序区间数组，逐段扫描合并。
package check

import (
	"slices"

	"ontology/iv"
)

// Naive 记录所有加入的区间，Ranges 时排序后逐段扫描合并。
type Naive struct {
	ivs []iv.Interval
}

func (n *Naive) Add(v iv.Interval) { n.ivs = append(n.ivs, v) }

// Ranges 返回排序后逐段合并的结果；相接（end >= next.Start）即并入。
func (n *Naive) Ranges() []iv.Interval {
	s := slices.Clone(n.ivs)
	slices.SortFunc(s, func(a, b iv.Interval) int { return a.Start - b.Start })
	var out []iv.Interval
	for _, r := range s {
		if len(out) > 0 && r.Start <= out[len(out)-1].End {
			out[len(out)-1].End = max(out[len(out)-1].End, r.End)
		} else {
			out = append(out, r)
		}
	}
	return out
}
