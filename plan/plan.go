// Package plan 计算固件 OTA 升级路径：给定目标版本与必经版本集合，
// 为任意当前版本求出下一跳版本。
package plan

import (
	"errors"
	"sort"
)

// ErrInvalid 表示目标版本或必经版本集合非法。
var ErrInvalid = errors.New("plan: invalid target or milestones")

const (
	minTarget     = 2
	maxTarget     = 1_000_000
	maxMilestones = 64
)

// Plan 是一条确定的升级路径：必经版本严格递增排列。
type Plan struct {
	target     int
	milestones []int
}

// New 校验并构造升级路径。target 为 2 到 10^6；milestones 为 0 到 64 个
// 互不相同且小于 target 的正整数。
func New(target int, milestones []int) (*Plan, error) {
	if target < minTarget || target > maxTarget {
		return nil, ErrInvalid
	}
	if len(milestones) > maxMilestones {
		return nil, ErrInvalid
	}
	seen := make(map[int]struct{}, len(milestones))
	for _, m := range milestones {
		if m < 1 || m >= target {
			return nil, ErrInvalid
		}
		if _, dup := seen[m]; dup {
			return nil, ErrInvalid
		}
		seen[m] = struct{}{}
	}
	ms := append([]int(nil), milestones...)
	sort.Ints(ms)
	return &Plan{target: target, milestones: ms}, nil
}

// Target 返回目标版本 T。
func (p *Plan) Target() int { return p.target }

// Next 返回版本 v 的下一跳：必经版本中严格大于 v 的最小者，没有则为 T。
// v 恰等于某必经版本时该版本不再重复。
func (p *Plan) Next(v int) int {
	i := sort.SearchInts(p.milestones, v+1)
	if i < len(p.milestones) {
		return p.milestones[i]
	}
	return p.target
}
