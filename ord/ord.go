// Package ord 提供可比较类型约束、分区结果校验与三类哨兵错误。
package ord

import (
	"cmp"
	"errors"

	"ontology/part"
)

// Ordered 可比较类型约束（别名标准库 cmp.Ordered）。
type Ordered = cmp.Ordered

// 三类哨兵错误，分别对应三个分段的不变量被破坏，可用 errors.Is 区分。
var (
	ErrLowerSegment = errors.New("ord: 小于段存在 >= pivot 的元素")
	ErrEqualSegment = errors.New("ord: 等于段存在 != pivot 的元素")
	ErrUpperSegment = errors.New("ord: 大于段存在 <= pivot 的元素")
)

// Verify 校验 arr 按 (lt, gt) 切分后是否满足三段不变量，不满足返回对应哨兵错误。
func Verify[T Ordered](arr []T, pivot T, lt, gt int) error {
	for _, v := range arr[:lt] {
		if v >= pivot {
			return ErrLowerSegment
		}
	}
	for _, v := range arr[lt:gt] {
		if v != pivot {
			return ErrEqualSegment
		}
	}
	for _, v := range arr[gt:] {
		if v <= pivot {
			return ErrUpperSegment
		}
	}
	return nil
}

// Partition 调用 part.ThreeWayPartition 并校验结果，不变量被破坏时返回哨兵错误。
func Partition[T Ordered](arr []T, pivot T) (lt, gt int, err error) {
	lt, gt = part.ThreeWayPartition(arr, pivot)
	if err := Verify(arr, pivot, lt, gt); err != nil {
		return lt, gt, err
	}
	return lt, gt, nil
}
