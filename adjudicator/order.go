package adjudicator

import "container/heap"

// resolveOrder 对可重建记录集合做确定性拓扑排序。
// 平局裁决规则固定不变：先按类别优先级（类型定义 < 对象实例 <
// 链接实例 < 动作记录），再按记录 ID 字典序。
// 若可重建子图存在循环依赖，返回循环上的记录。
func resolveOrder(records map[RecordRef]*Record, rebuildable map[RecordRef]bool) (order []RecordRef, cycle []RecordRef) {
	// indegree 只统计可重建记录之间的依赖边；
	// 指向不可重建记录的边不存在于可重建子图中。
	indegree := make(map[RecordRef]int, len(rebuildable))
	dependents := make(map[RecordRef][]RecordRef)
	for ref, ok := range rebuildable {
		if !ok {
			continue
		}
		indegree[ref] = 0
	}
	for ref := range indegree {
		for _, dep := range records[ref].DependsOn {
			if rebuildable[dep] {
				indegree[ref]++
				dependents[dep] = append(dependents[dep], ref)
			}
		}
	}

	ready := &refHeap{}
	for ref, d := range indegree {
		if d == 0 {
			heap.Push(ready, ref)
		}
	}
	for ready.Len() > 0 {
		ref := heap.Pop(ready).(RecordRef)
		order = append(order, ref)
		for _, dependent := range dependents[ref] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				heap.Push(ready, dependent)
			}
		}
	}
	if len(order) != len(indegree) {
		// 正常情况下 evaluator 已把循环分量排除在可重建集合之外，
		// 这里兜底：剩余未排出的记录即循环上的记录。
		for ref, d := range indegree {
			if d > 0 {
				cycle = append(cycle, ref)
			}
		}
		cycle = dedupeRefs(cycle)
	}
	return order, cycle
}

// refHeap 为按固定全序（类别优先级, ID 字典序）组织的最小堆，
// 保证拓扑排序在多个可选项中永远取同一个，从而输出唯一顺序。
type refHeap []RecordRef

func (h refHeap) Len() int { return len(h) }

func (h refHeap) Less(i, j int) bool { return compareRefs(h[i], h[j]) < 0 }

func (h refHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *refHeap) Push(x any) { *h = append(*h, x.(RecordRef)) }

func (h *refHeap) Pop() any {
	old := *h
	top := old[len(old)-1]
	*h = old[:len(old)-1]
	return top
}
