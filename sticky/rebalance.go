package sticky

import "sort"

// assignment 是内部可变表示：分区编号 -> 成员标识（"" 表示无主）。
type assignment []string

// rebalance 执行纯函数重分配：给定批前持有、批后成员集合，
// 按“释放 -> 补齐”的粘性规则产出新分配与迁移数。
func rebalance(partitions assignment, members map[string]struct{}) (assignment, int) {
	next := make(assignment, len(partitions))
	copy(next, partitions)

	// 无成员：所有分区无主。批前有主的分区此时不算“换主”，迁移数为 0。
	if len(members) == 0 {
		for i := range next {
			next[i] = ""
		}
		return next, 0
	}

	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	n := len(next)
	base := n / len(ids)
	remainder := n % len(ids)

	// 批前持有数（只统计批后仍在组内的成员）。
	held := make(map[string]int, len(ids))
	for _, owner := range next {
		if _, ok := members[owner]; ok {
			held[owner]++
		}
	}

	// 配额 = 基础配额 + 余数名额；余数名额按批前持有数降序、
	// 并列按标识升序分配。
	quota := make(map[string]int, len(ids))
	for _, id := range ids {
		quota[id] = base
	}
	order := make([]string, len(ids))
	copy(order, ids)
	sort.SliceStable(order, func(i, j int) bool {
		if held[order[i]] != held[order[j]] {
			return held[order[i]] > held[order[j]]
		}
		return order[i] < order[j]
	})
	for i := 0; i < remainder; i++ {
		quota[order[i]]++
	}

	// 离开成员的分区变为无主。
	for i, owner := range next {
		if owner != "" {
			if _, ok := members[owner]; !ok {
				next[i] = ""
			}
		}
	}

	// 释放：持有数超配额的成员，按分区编号从大到小释放。
	counts := make(map[string]int, len(ids))
	for _, id := range ids {
		counts[id] = 0
	}
	for _, owner := range next {
		if owner != "" {
			counts[owner]++
		}
	}
	for i := len(next) - 1; i >= 0; i-- {
		owner := next[i]
		if owner == "" {
			continue
		}
		if counts[owner] > quota[owner] {
			next[i] = ""
			counts[owner]--
		}
	}

	// 补齐：无主分区按编号升序，逐个交给当前配额缺口最大者，
	// 缺口并列取标识最小者。
	for i, owner := range next {
		if owner != "" {
			continue
		}
		target := ""
		bestGap := 0
		for _, id := range ids {
			gap := quota[id] - counts[id]
			if gap > bestGap || (gap == bestGap && gap > 0 && (target == "" || id < target)) {
				target = id
				bestGap = gap
			}
		}
		next[i] = target
		if target != "" {
			counts[target]++
		}
	}

	migrations := 0
	for i := range partitions {
		if partitions[i] != "" && next[i] != partitions[i] {
			migrations++
		}
	}
	return next, migrations
}
