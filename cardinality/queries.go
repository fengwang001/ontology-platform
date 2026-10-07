package cardinality

import "sort"

// ActiveLinks 返回某桶当前有效链接，按 (seq, id) 全序升序。
func (e *Engine) ActiveLinks(key BucketKey) []Link {
	e.mu.Lock()
	defer e.mu.Unlock()

	var out []Link
	for _, lk := range e.store.orderedLinks(e.store.ensureBucket(key)) {
		if lk.State == StateActive {
			out = append(out, *lk)
		}
	}
	return out
}

// PendingLinks 返回某桶当前待处理链接的带过期标注视图，
// 按 (seq, id) 全序升序。
func (e *Engine) PendingLinks(key BucketKey) []PendingView {
	e.mu.Lock()
	defer e.mu.Unlock()

	var out []PendingView
	for _, lk := range e.store.orderedLinks(e.store.ensureBucket(key)) {
		if lk.State != StatePending {
			continue
		}
		var deps []string
		for _, d := range e.store.derived {
			if d.LinkID == lk.ID {
				deps = append(deps, d.ID)
			}
		}
		sort.Strings(deps)
		out = append(out, PendingView{
			Link:            *lk,
			Stale:           true,
			Reason:          "link marked over-limit and pending; excluded from cardinality-based decisions",
			DependentsStale: deps,
		})
	}
	return out
}

// PendingCount 返回某桶当前待处理链接数量。
// 开销仅与当前待处理集合相关，与历史调整次数无关。
func (e *Engine) PendingCount(key BucketKey) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.store.ensureBucket(key).pendingCount
}

// RegisteredCount 返回某桶历史已登记且未物理删除的链接数量
// （含待处理；待处理链接计入历史统计）。
func (e *Engine) RegisteredCount(key BucketKey) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.store.ensureBucket(key).members)
}

// CurrentLimit 返回某方向当前基数（基础）上限。
func (e *Engine) CurrentLimit(key BucketKey) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.store.ensureBucket(key).baseLimit
}

// EffectiveCapacity 返回某方向当前有效容量，
// 即基础上限与因保留而提升的有效上限二者所需的最大值。
func (e *Engine) EffectiveCapacity(key BucketKey) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.store.ensureBucket(key)
	return e.store.effectiveCapacity(b, e.store.orderedLinks(b))
}

// GetLink 按 ID 获取链接。
func (e *Engine) GetLink(linkID string) (Link, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	lk, ok := e.store.links[linkID]
	if !ok {
		return Link{}, false
	}
	return *lk, true
}

// AuditLog 返回截至目前的全部处理记录，供事后核对。
func (e *Engine) AuditLog() []AuditRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]AuditRecord, len(e.store.auditLog))
	copy(out, e.store.auditLog)
	return out
}
