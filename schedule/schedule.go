// Package schedule 负责钩子编排：按 phase 分组（pre 全先于 post），
// 组内按 (priority, 注册序号, name) 确定性排序，返回可复现的执行顺序。
package schedule

import (
	"cmp"
	"slices"

	"ontology/hook"
)

// Plan 接收匹配到的钩子集合，返回确定的执行序列：
// pre 组整体在前、post 组在后；组内 priority 升序，再按 name，注册序号仅兜底。
// 排序键全部取自钩子内容（name 唯一），与注册/传入顺序无关：
// 任意注册顺序都得到逐字节相同的执行序列。
func Plan(hooks []hook.Hook) []hook.Hook {
	out := make([]hook.Hook, len(hooks))
	copy(out, hooks)
	slices.SortStableFunc(out, compare)
	return out
}

func compare(a, b hook.Hook) int {
	if c := cmp.Compare(a.Phase, b.Phase); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Priority, b.Priority); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	return cmp.Compare(a.Seq(), b.Seq())
}
