package ontology

// evaluator 绑定一次固定的状态快照与调用者，承载一批请求对的全部判定。
// 同一快照保证批内所有单项锚定同一逻辑时点；按起点记忆化可达集，
// 使批内共享起点的请求对复用同一次搜索。
type evaluator struct {
	st     *state
	caller CallerID

	// reach 按起点记忆化的可达对象集合（懒计算）。
	reach map[ObjectID]map[ObjectID]struct{}

	// 内部度量：仅统计本批实际访问的对象与链接。
	visitedObjects int
	visitedLinks   int
}

func newEvaluator(st *state, caller CallerID) *evaluator {
	return &evaluator{st: st, caller: caller, reach: map[ObjectID]map[ObjectID]struct{}{}}
}

// evaluate 判定单个请求对，判定次序：
// 类型禁用 > 存在性权限不足 > 正常搜索。
func (e *evaluator) evaluate(p Pair) ItemResult {
	if e.typeForbidden(p.Start) || e.typeForbidden(p.End) {
		return ItemResult{State: StateRestrictedUnknown, Reason: ReasonTypeForbidden}
	}
	if !e.visible(p.Start) || !e.visible(p.End) {
		return ItemResult{State: StateRestrictedUnknown, Reason: ReasonNoExistence}
	}
	if p.Start == p.End {
		return ItemResult{State: StateReachable, Reason: ReasonReachable}
	}
	if _, ok := e.reachableFrom(p.Start)[p.End]; ok {
		return ItemResult{State: StateReachable, Reason: ReasonReachable}
	}
	return ItemResult{State: StateUnreachable, Reason: ReasonUnreachable}
}

// visible 报告对象对调用者是否具备存在性权限。
// 不存在的对象一律视为不可见，避免泄露对象存在性。
func (e *evaluator) visible(id ObjectID) bool {
	if _, ok := e.st.objects[id]; !ok {
		return false
	}
	return e.st.existence[e.caller][id]
}

// typeForbidden 报告对象所在类型是否被禁止参与可达性查询。
func (e *evaluator) typeForbidden(id ObjectID) bool {
	o, ok := e.st.objects[id]
	if !ok {
		return false
	}
	return e.st.objectTypes[o.Type].ReachableQueryForbidden
}

// reachableFrom 返回从 start 出发、沿调用者可遍历链接可达的对象集合，
// 结果在批内记忆化，重复起点不重复搜索。
// 度量只统计本批实际访问的对象与链接：visitedObjects 为出队扩展的
// 对象数，visitedLinks 为检查过的出边数。
func (e *evaluator) reachableFrom(start ObjectID) map[ObjectID]struct{} {
	if r, ok := e.reach[start]; ok {
		return r
	}
	traversable := e.st.traversal[e.caller]
	visited := map[ObjectID]struct{}{start: {}}
	queue := []ObjectID{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		e.visitedObjects++
		for _, l := range e.st.out[cur] {
			e.visitedLinks++
			if !traversable[l.Type] {
				continue
			}
			if _, ok := visited[l.To]; ok {
				continue
			}
			visited[l.To] = struct{}{}
			queue = append(queue, l.To)
		}
	}
	e.reach[start] = visited
	return visited
}
