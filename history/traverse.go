package history

// Action 描述一次已执行遍历的结果动作。
type Action int

const (
	ActionSameDocument Action = iota // 同文档遍历，当前文档不进缓存
	ActionRestored                   // 目标文档从缓存恢复
	ActionReloaded                   // 目标文档（或当前条目）重新加载
)

func (a Action) String() string {
	switch a {
	case ActionSameDocument:
		return "same-document"
	case ActionRestored:
		return "restored"
	case ActionReloaded:
		return "reloaded"
	}
	return "unknown"
}

// TraverseResult 是一次遍历请求的最终结果。
type TraverseResult struct {
	Action Action
	Entry  Entry // 遍历完成后的当前条目；恢复时其 State 为目标条目当前的状态对象
	Err    error
}

// Future 是一次已提交遍历请求的句柄。
type Future struct {
	ch chan TraverseResult
}

// Result 阻塞直到该请求被 RunPending 处理（执行或报告被取代）。
func (f *Future) Result() TraverseResult { return <-f.ch }

type pendingTraverse struct {
	delta int
	fut   *Future
}

// Traverse 提交一次相对位移遍历请求。请求不会立即执行；
// 同一批（连续到达）的请求在下一次 RunPending 时合并，只有最后一次被执行，
// 之前的请求以 KindSuperseded 报告且不改变任何状态。
func (k *Kernel) Traverse(delta int) *Future {
	f := &Future{ch: make(chan TraverseResult, 1)}
	k.mu.Lock()
	k.pending = append(k.pending, pendingTraverse{delta: delta, fut: f})
	k.mu.Unlock()
	return f
}

// RunPending 处理当前排队的全部遍历请求：除最后一个外全部报告被取代。
func (k *Kernel) RunPending() {
	k.mu.Lock()
	pend := k.pending
	k.pending = nil
	for i, p := range pend {
		var r TraverseResult
		if i < len(pend)-1 {
			r = TraverseResult{Err: errf(KindSuperseded, "traversal delta=%d superseded by a later request", p.delta)}
		} else {
			r = k.executeTraverseLocked(p.delta)
		}
		p.fut.ch <- r
	}
	k.mu.Unlock()
}

// TraverseSync 提交一个遍历请求并立即处理队列，返回该请求的结果。
func (k *Kernel) TraverseSync(delta int) TraverseResult {
	f := k.Traverse(delta)
	k.RunPending()
	return f.Result()
}

// executeTraverseLocked 执行相对位移遍历。
// 定位目标条目（切片下标）与判定同文档（比较文档标识）均为 O(1)。
func (k *Kernel) executeTraverseLocked(delta int) TraverseResult {
	target := k.pos + delta
	if target < 0 || target >= len(k.entries) {
		return TraverseResult{Err: errf(KindInvalidArgument, "delta %d out of range at position %d of %d entries", delta, k.pos, len(k.entries))}
	}
	if delta == 0 {
		// 位移为零视为重新加载当前条目。
		k.reloadLocked(k.entries[k.pos].DocID)
		return TraverseResult{Action: ActionReloaded, Entry: k.entries[k.pos]}
	}
	curID := k.entries[k.pos].DocID
	tgtID := k.entries[target].DocID
	if tgtID == curID {
		// 同文档遍历：当前文档不进入缓存。
		k.pos = target
		return TraverseResult{Action: ActionSameDocument, Entry: k.entries[k.pos]}
	}
	tgt := k.docs[tgtID]
	restored := tgt != nil && tgt.status == statusCached
	if restored {
		// 目标文档在缓存中：先取出恢复，再让当前文档按资格进入缓存，
		// 避免容量为 1 时错误的自我挤兑。
		k.cache.remove(tgt)
		tgt.status = statusActive
	}
	k.leaveCurrentLocked()
	if !restored {
		// 目标文档已卸载：重新加载并分配新标识，同文档条目一并更新。
		k.reloadLocked(tgtID)
	}
	k.pos = target
	k.enforceCapacityLocked()
	return TraverseResult{Action: map[bool]Action{true: ActionRestored, false: ActionReloaded}[restored], Entry: k.entries[k.pos]}
}
