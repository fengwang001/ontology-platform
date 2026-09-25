// Package part 描述单个范围分区及其静态/动态裁剪判定。不依赖其他包。
package part

import "errors"

// ErrInvalidPartition 分区非法：lo >= hi 或 min_v > max_v。
var ErrInvalidPartition = errors.New("part: invalid partition")

// Partition 是一个范围分区：p ∈ [Lo, Hi)，列 v 的真实统计 [MinV, MaxV]（闭区间）。
type Partition struct {
	ID   string
	Lo   int64
	Hi   int64
	MinV int64
	MaxV int64
}

// New 构造并校验分区；非法时整体失败，不产生任何状态。
func New(id string, lo, hi, minv, maxv int64) (Partition, error) {
	if lo >= hi || minv > maxv {
		return Partition{}, ErrInvalidPartition
	}
	return Partition{ID: id, Lo: lo, Hi: hi, MinV: minv, MaxV: maxv}, nil
}

// StaticPrune 静态裁剪：p 范围 [Lo,Hi) 与谓词 [plo,phi) 无交集。
// 边界恰好相接（Hi==plo 或 Lo==phi）也算无交集，必须裁。
func (p Partition) StaticPrune(plo, phi int64) bool {
	return p.Hi <= plo || p.Lo >= phi
}

// DynamicPrune 动态裁剪：v 统计 [MinV,MaxV] 与谓词 [vlo,vhi) 无交集。
// MinV==vhi 时恰好无交集，必须裁。
func (p Partition) DynamicPrune(vlo, vhi int64) bool {
	return p.MaxV < vlo || p.MinV >= vhi
}
