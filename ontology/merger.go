package ontology

import "sync"

// Merger 是并发安全的倒排索引段合并器。
//
// 一把互斥锁串行化全部操作，因此任何并发调用的结果都等价于某个
// 合法的串行顺序；返回的统计量与倒排表均为深拷贝。
type Merger struct {
	mu        sync.Mutex
	nextSeg   int // 下一个段编号（从 1 起）
	nextMerge int // 下一个合并句柄号（从 1 起）
	segments  map[int]*segment
	liveKeys  map[string]*segment // 全局存活键 -> 所在段
	merges    map[int]*mergeState
}

// NewMerger 创建一个空合并器。
func NewMerger() *Merger {
	return &Merger{
		nextSeg:   1,
		nextMerge: 1,
		segments:  make(map[int]*segment),
		liveKeys:  make(map[string]*segment),
		merges:    make(map[int]*mergeState),
	}
}

// Register 登记一批文档，返回新段编号。
// 拒绝顺序：批为空、键为空串、词项为空串、键冲突；被拒绝不消耗段编号。
func (m *Merger) Register(batch []Doc) (int, error) {
	if len(batch) == 0 {
		return 0, ErrEmptyBatch
	}
	for _, doc := range batch {
		if doc.Key == "" {
			return 0, ErrEmptyKey
		}
		for _, term := range doc.Terms {
			if term == "" {
				return 0, ErrEmptyTerm
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, doc := range batch {
		if _, ok := m.liveKeys[doc.Key]; ok {
			return 0, ErrDuplicateKey
		}
	}
	batchKeys := make(map[string]struct{}, len(batch))
	for _, doc := range batch {
		if _, ok := batchKeys[doc.Key]; ok {
			return 0, ErrDuplicateKey
		}
		batchKeys[doc.Key] = struct{}{}
	}

	id := m.nextSeg
	m.nextSeg++
	seg := buildSegmentFromBatch(id, batch)
	m.segments[id] = seg
	for _, doc := range batch {
		m.liveKeys[doc.Key] = seg
	}
	return id, nil
}

// Delete 给键当前存活的文档打删除标记，不改变段的其余内容。
// 删除不与未结束合并冲突：BeginMerge 冻结了文档来源，Commit 时回放标记。
func (m *Merger) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	seg, ok := m.liveKeys[key]
	if !ok {
		return ErrKeyNotFound
	}
	docID := seg.liveKeys[key]
	seg.deleted[docID] = true
	delete(seg.liveKeys, key)
	delete(m.liveKeys, key)
	return nil
}

// BeginMerge 在调用时刻冻结合并集合，返回句柄号。
// 拒绝顺序：参数非法（少于 2 个或含重复编号）、段不存在（按 ids 次序）、
// 段忙（按 ids 次序）；被拒绝不消耗句柄号。
func (m *Merger) BeginMerge(ids []int) (int, error) {
	if len(ids) < 2 {
		return 0, ErrInvalidMerge
	}
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			return 0, ErrInvalidMerge
		}
		seen[id] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range ids {
		if _, ok := m.segments[id]; !ok {
			return 0, ErrSegmentNotFound
		}
	}
	for _, id := range ids {
		if m.segments[id].busy {
			return 0, ErrSegmentBusy
		}
	}

	docs := make([]frozenDoc, 0)
	for _, id := range ids {
		seg := m.segments[id]
		for docID := 0; docID < seg.maxDoc(); docID++ {
			if !seg.deleted[docID] {
				docs = append(docs, frozenDoc{seg: seg, docID: docID, key: seg.keys[docID]})
			}
		}
	}

	handle := m.nextMerge
	m.nextMerge++
	state := &mergeState{id: handle, status: mergePending, inputs: append([]int(nil), ids...), docs: docs}
	m.merges[handle] = state
	for _, id := range ids {
		m.segments[id].busy = true
	}
	return handle, nil
}

// Commit 提交合并：新段保留全部冻结编号，删除状态按此刻源段标记回放；
// 倒排表、df、termCount 只统计 Commit 时刻仍存活的文档。
// 成功后输入段移除，新段取下一个段编号。
func (m *Merger) Commit(handle int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.merges[handle]
	if !ok || state.status != mergePending {
		return 0, ErrInvalidHandle
	}

	deleted := state.snapshotDeletions()
	id := m.nextSeg
	m.nextSeg++
	seg := buildSegment(id, state.docs, deleted)

	// 从全局存活键索引中摘除输入段，再加入新段中仍存活的文档。
	for _, inID := range state.inputs {
		inSeg := m.segments[inID]
		for key := range inSeg.liveKeys {
			delete(m.liveKeys, key)
		}
		delete(m.segments, inID)
	}
	m.segments[id] = seg
	for key := range seg.liveKeys {
		m.liveKeys[key] = seg
	}
	state.status = mergeCommitted
	return id, nil
}

// Abort 放弃合并，输入段恢复为可合并状态。
func (m *Merger) Abort(handle int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.merges[handle]
	if !ok || state.status != mergePending {
		return ErrInvalidHandle
	}
	for _, id := range state.inputs {
		if seg, ok := m.segments[id]; ok {
			seg.busy = false
		}
	}
	state.status = mergeAborted
	return nil
}

// Stats 返回段统计量的副本。
func (m *Merger) Stats(segmentID int) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	seg, ok := m.segments[segmentID]
	if !ok {
		return Stats{}, ErrSegmentNotFound
	}
	return seg.stats(), nil
}

// Postings 返回某词项倒排表的副本；词项不存在时返回非 nil 空切片。
func (m *Merger) Postings(segmentID int, term string) ([]Posting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	seg, ok := m.segments[segmentID]
	if !ok {
		return nil, ErrSegmentNotFound
	}
	return seg.postings(term), nil
}
