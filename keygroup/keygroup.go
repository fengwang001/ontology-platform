package keygroup

import (
	"hash/fnv"
)

// GroupRange 表示分配给某个实例的连续键组区间 [Start, End)。
type GroupRange struct {
	Instance int
	Start    int
	End      int
}

// Contains 判断键组 g 是否落在该区间内。
func (r GroupRange) Contains(g int) bool { return r.Start <= g && g < r.End }

// Width 返回区间包含的键组数量。
func (r GroupRange) Width() int { return r.End - r.Start }

// AssignRanges 把 totalGroups 个连续键组按连续区间分配给 parallelism 个实例。
// 区间两两不交、恰好覆盖 [0, totalGroups)，且尽量均匀（相差不超过 1）。
func AssignRanges(totalGroups, parallelism int) ([]GroupRange, error) {
	if totalGroups <= 0 {
		return nil, &InvalidGroupsError{Groups: totalGroups}
	}
	if parallelism <= 0 {
		return nil, &InvalidParallelismError{Parallelism: parallelism, Reason: ErrInvalidParallelism}
	}
	if parallelism > totalGroups {
		return nil, &InvalidParallelismError{Parallelism: parallelism, Reason: ErrParallelismTooLarge}
	}
	ranges := make([]GroupRange, parallelism)
	start := 0
	for instance := 0; instance < parallelism; instance++ {
		end := (instance + 1) * totalGroups / parallelism
		ranges[instance] = GroupRange{Instance: instance, Start: start, End: end}
		start = end
	}
	return ranges, nil
}

// OwnerOf 返回键组 g 在给定并行度下归属的实例编号。
func OwnerOf(g, totalGroups, parallelism int) (int, error) {
	if totalGroups <= 0 {
		return -1, &InvalidGroupsError{Groups: totalGroups}
	}
	if parallelism <= 0 {
		return -1, &InvalidParallelismError{Parallelism: parallelism, Reason: ErrInvalidParallelism}
	}
	if parallelism > totalGroups {
		return -1, &InvalidParallelismError{Parallelism: parallelism, Reason: ErrParallelismTooLarge}
	}
	if g < 0 || g >= totalGroups {
		return -1, &GroupOutOfRangeError{Group: g, NumGroups: totalGroups}
	}
	owner := (g * parallelism) / totalGroups
	for owner < parallelism-1 && g >= (owner+1)*totalGroups/parallelism {
		owner++
	}
	for owner > 0 && g < owner*totalGroups/parallelism {
		owner--
	}
	return owner, nil
}

// KeyToGroup 使用确定性哈希把键映射到 [0, numGroups) 内的键组。
func KeyToGroup(key string, numGroups uint32) (int, error) {
	if key == "" {
		return -1, ErrEmptyKey
	}
	if numGroups == 0 {
		return -1, &InvalidGroupsError{Groups: 0}
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key))
	return int(hasher.Sum32() % numGroups), nil
}
