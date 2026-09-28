package lwwset

import "sort"

// lockPair 按副本编号升序获取双方锁：编号小者先锁，
// 保证 r.Merge(s) 与 s.Merge(r) 互逆方向并发执行时不会循环等待。
// 目标（被修改方）取写锁，来源方取读锁。
func lockPair(dst, src *Replica) func() {
	if dst.id < src.id {
		dst.mu.Lock()
		src.mu.RLock()
	} else {
		src.mu.RLock()
		dst.mu.Lock()
	}
	return func() {
		if dst.id < src.id {
			src.mu.RUnlock()
			dst.mu.Unlock()
		} else {
			dst.mu.Unlock()
			src.mu.RUnlock()
		}
	}
}

// Merge 把 other 的全部记录两两合并进 r，只修改 r。
// 对每个元素的添加、删除两条记录分别取两侧较大值（LWW），
// 旧时间戳不会把记录改小。合并是幂等、可交换且满足合并律的。
func (r *Replica) Merge(other *Replica) error {
	if r == nil || other == nil {
		return ErrNilReplica
	}
	if other.id == r.id {
		return ErrSelfMerge
	}
	unlock := lockPair(r, other)
	defer unlock()

	if !mergeFitsLimit(r.records, other.records, r.limit) {
		return ErrLimitExceeded
	}
	mergeRecords(r.records, other.records)
	// 整份合并后，该来源当前的全部序号都已纳入；推进合并位置，
	// 使后续增量合并等价于“先看到全部历史”。
	r.mergePos[other.id] = int64(len(other.log))
	return nil
}

// MergeChanges 执行按变更序号的增量合并：只取 other 上序号大于 r 已合并位置的变更。
// 结果与直接调用整份 Merge 完全一致（增量合并可复现整份合并结果）。
// 变更序号必须与已合并位置严格连续，否则整体拒绝（ErrSequenceGap）。
func (r *Replica) MergeChanges(other *Replica) error {
	if r == nil || other == nil {
		return ErrNilReplica
	}
	if other.id == r.id {
		return ErrSelfMerge
	}
	unlock := lockPair(r, other)
	defer unlock()

	since := r.mergePos[other.id]
	tail := other.log[since:]

	// 在副本锁内对增量做完整校验：序号连续、时间戳合法、携带标记。
	// 任何非法项都导致整体拒绝，记录、序号与合并位置均不变。
	for i, ch := range tail {
		want := since + int64(i) + 1
		if ch.Seq != want || ch.Element == "" {
			return ErrSequenceGap
		}
		if (ch.AddTime <= 0 && ch.RemoveTime <= 0) || (ch.AddTime > 0 && ch.RemoveTime > 0) {
			return ErrMissingTimestamp
		}
	}

	// 先在临时副本上计算合并结果，确认不超上限后再整体提交（失败不留痕）。
	next := make(map[string]Record, len(r.records)+len(tail))
	for elem, rec := range r.records {
		next[elem] = rec
	}
	for _, ch := range tail {
		rec := next[ch.Element]
		if ch.AddTime > 0 {
			if ch.AddTime > rec.AddTime {
				rec.AddTime = ch.AddTime
			}
		} else {
			if ch.RemoveTime > rec.RemoveTime {
				rec.RemoveTime = ch.RemoveTime
			}
		}
		next[ch.Element] = rec
	}
	if len(next) > r.limit {
		return ErrLimitExceeded
	}

	r.records = next
	r.mergePos[other.id] = int64(len(other.log))
	return nil
}

// mergeRecords 把 src 的每条记录按 LWW 合并进 dst（原地修改）。
func mergeRecords(dst, src map[string]Record) {
	for elem, incoming := range src {
		cur := dst[elem]
		if incoming.AddTime > cur.AddTime {
			cur.AddTime = incoming.AddTime
		}
		if incoming.RemoveTime > cur.RemoveTime {
			cur.RemoveTime = incoming.RemoveTime
		}
		dst[elem] = cur
	}
}

// mergeFitsLimit 预演合并并判断不同元素的记录条数是否会超过 limit。
func mergeFitsLimit(dst, src map[string]Record, limit int) bool {
	count := len(dst)
	if count > limit {
		return false
	}
	for elem := range src {
		if _, ok := dst[elem]; !ok {
			count++
			if count > limit {
				return false
			}
		}
	}
	return true
}

// ApplyChanges 把外部变更流（如序列化后跨进程传输）合并进 r。
// sourceID 必须是正数且与 changes 的来源一致；changes 必须按 Seq 升序，
// 且首个序号必须等于 r 对该来源的已合并位置 + 1。
// 整个调用是原子的：任一校验失败则什么都不改变。
func (r *Replica) ApplyChanges(sourceID int64, changes []Change) error {
	if r == nil {
		return ErrNilReplica
	}
	if sourceID <= 0 {
		return ErrInvalidReplicaID
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	since := r.mergePos[sourceID]
	next := make(map[string]Record, len(r.records)+len(changes))
	for elem, rec := range r.records {
		next[elem] = rec
	}
	for i, ch := range changes {
		if ch.Seq != since+int64(i)+1 {
			return ErrSequenceGap
		}
		if ch.Element == "" {
			return ErrEmptyElement
		}
		if (ch.AddTime <= 0 && ch.RemoveTime <= 0) || (ch.AddTime > 0 && ch.RemoveTime > 0) {
			return ErrMissingTimestamp
		}
		rec := next[ch.Element]
		if ch.AddTime > 0 {
			if ch.AddTime > rec.AddTime {
				rec.AddTime = ch.AddTime
			}
		} else if ch.RemoveTime > rec.RemoveTime {
			rec.RemoveTime = ch.RemoveTime
		}
		next[ch.Element] = rec
	}
	if len(next) > r.limit {
		return ErrLimitExceeded
	}

	r.records = next
	if len(changes) > 0 {
		r.mergePos[sourceID] = changes[len(changes)-1].Seq
	}
	return nil
}

// SortedElements 是便于外部自检使用的稳定排序辅助函数。
func SortedElements(records map[string]Record) []string {
	out := make([]string, 0, len(records))
	for elem, rec := range records {
		if rec.present() {
			out = append(out, elem)
		}
	}
	sort.Strings(out)
	return out
}
