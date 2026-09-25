// Package alloc 提供多任务的最大最小公平份额分配：按 demand 升序组织、
// 递归重算公平份额、守恒合计。依赖 mf，不依赖 api。
package alloc

import (
	"sort"

	"ontology/mf"
)

// Task 是一个待分配任务。Demand 非负；0 需求任务得 0 且不计入任务数。
type Task struct {
	ID     string
	Demand int64
}

// Engine 执行分配。examined 记录最近一次 Allocate 考察过的任务个数，
// 是非导出字段，不出现在任何公开接口里。
type Engine struct {
	examined int
}

// Allocate 对 tasks 做水平填充分配，返回逐 id 的精确份额。
// 排序一次后单向扫描，遇到第一个 demand > fair 的任务即定位水位点，
// 考察个数不随总任务数线性增长。capacity 必须为正。
func (e *Engine) Allocate(capacity int64, tasks []Task) map[string]mf.Frac {
	out := make(map[string]mf.Frac, len(tasks))
	pos := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Demand == 0 {
			out[t.ID] = mf.Frac{N: 0, D: 1} // 零需求：得 0，不计入任务数
		} else {
			pos = append(pos, t)
		}
	}
	sort.Slice(pos, func(i, j int) bool { return pos[i].Demand < pos[j].Demand })

	e.examined = 0
	rem, cnt := capacity, int64(len(pos))
	for i, t := range pos {
		e.examined++
		fair := mf.Fair(rem, cnt)
		if mf.Leq(t.Demand, fair) { // 全额满足，扣掉后重算
			out[t.ID] = mf.New(t.Demand, 1)
			rem -= t.Demand
			cnt--
			continue
		}
		for _, u := range pos[i:] { // 水位点：剩余全员同得 fair
			out[u.ID] = fair
		}
		break
	}
	return out
}

// Naive 是朴素参照实现：每轮重扫全部剩余任务找最小 demand，
// 严格按题述递归水平填充规则逐步计算。仅用于自检与测试对照。
func Naive(capacity int64, tasks []Task) map[string]mf.Frac {
	out := make(map[string]mf.Frac, len(tasks))
	rem := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		if t.Demand == 0 {
			out[t.ID] = mf.Frac{N: 0, D: 1}
		} else {
			rem = append(rem, t)
		}
	}
	remCap := capacity
	for len(rem) > 0 {
		fair := mf.Fair(remCap, int64(len(rem)))
		mi := 0
		for i := range rem { // 重扫找最小 demand
			if rem[i].Demand < rem[mi].Demand {
				mi = i
			}
		}
		if !mf.Leq(rem[mi].Demand, fair) {
			for _, t := range rem {
				out[t.ID] = fair
			}
			return out
		}
		out[rem[mi].ID] = mf.New(rem[mi].Demand, 1)
		remCap -= rem[mi].Demand
		rem = append(rem[:mi], rem[mi+1:]...)
	}
	return out
}
