package approval

import "ontology/org"

// request 是单个申请的冻结状态。
type request struct {
	applicant string
	amount    int64
	// candidates 为提交时冻结的合格审批人链。
	candidates []string
	// idx 为当前审批人在 candidates 中的下标。
	idx int
	// ta 为当前审批人的分配时刻。
	ta int64
	// outcome 为终局类型；Pending 表示待决。
	outcome Outcome
	// finalAt 为终局时刻。
	finalAt int64
}

// assignee 返回当前审批人；终局时返回空串。
func (r *request) assignee() string {
	if r.outcome != Pending {
		return ""
	}
	return r.candidates[r.idx]
}

// advance 把申请推进到到期时刻为 at 的状态：
// 还有下一候选则升级（ta=at），否则终局 Expired（finalAt=at）。
func (r *request) advance(at int64) {
	if r.idx+1 < len(r.candidates) {
		r.idx++
		r.ta = at
		return
	}
	r.outcome = Expired
	r.finalAt = at
}

// snapshot 记录可回滚的申请字段。
type snapshot struct {
	idx     int
	ta      int64
	outcome Outcome
	finalAt int64
}

func (r *request) snapshotState() snapshot {
	return snapshot{r.idx, r.ta, r.outcome, r.finalAt}
}

func (r *request) restore(s snapshot) {
	r.idx, r.ta, r.outcome, r.finalAt = s.idx, s.ta, s.outcome, s.finalAt
}

// 下列为只读观测访问器，供测试与状态巡检使用。

// OrgForTest 返回引擎绑定的组织（测试辅助）。
func (e *Engine) OrgForTest() *org.Org { return e.org }

// Count 返回引擎内申请总数。
func (e *Engine) Count() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.requests)
}

// HeapLen 返回到期堆项数。
func (e *Engine) HeapLen() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.heap.Len()
}

// HeapKeys 返回堆中申请键。
func (e *Engine) HeapKeys() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.heap.Keys()
}
