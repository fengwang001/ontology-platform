// Package promote 根据组的只读快照计算主分片晋升计划。
//
// 计划的校验与原子提交由 tracker.Group.CommitPromotion 在锁内完成；
// 本包只做纯函数决策，不持有任何可变状态，因而与 tracker 之间不存在
// 循环依赖或双重加锁问题。
package promote

import (
	"slices"

	"ontology/tracker"
)

// PlanOf 依据快照 v 计算把 name 晋升为主的计划。
//
// 拒绝次序：参数非法 > 成员不存在 > 状态不符（对象是主或追赶副本）。
// 提交时 tracker 还会复查任期与 gcp，过期计划返回 tracker.ErrPlanStale。
func PlanOf(v tracker.View, name string) (tracker.Plan, error) {
	if len(name) < 1 || len(name) > 64 {
		return tracker.Plan{}, tracker.ErrInvalidArg
	}
	var candidate *tracker.MemberView
	for i := range v.Members {
		if v.Members[i].Name == name {
			candidate = &v.Members[i]
			break
		}
	}
	if candidate == nil {
		return tracker.Plan{}, tracker.ErrMemberNotFound
	}
	if candidate.Role != tracker.RoleSync {
		return tracker.Plan{}, tracker.ErrWrongState
	}

	m := 0
	if len(candidate.Processed) > 0 {
		m = candidate.Processed[len(candidate.Processed)-1]
	}
	fill := make([]int, 0)
	for seq := 1; seq <= m; seq++ {
		if !slices.Contains(candidate.Processed, seq) {
			fill = append(fill, seq)
		}
	}

	lost := append([]int(nil), fill...)
	for seq := m + 1; seq <= v.MaxSeq; seq++ {
		lost = append(lost, seq)
	}

	return tracker.Plan{
		NewPrimary: name,
		Term:       v.Term + 1,
		G:          v.GCP,
		M:          m,
		Fill:       fill,
		Lost:       lost,
	}, nil
}

// Promote 是便捷封装：从 g 取快照、计算计划并原子提交。
func Promote(g *tracker.Group, name string) (tracker.Plan, error) {
	plan, err := PlanOf(g.View(), name)
	if err != nil {
		return tracker.Plan{}, err
	}
	return plan, g.CommitPromotion(plan)
}
