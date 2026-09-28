package lwwset

// Merge 将 src 的全部记录合并进 s（只修改 s）。
// 对每个元素的两条记录分别取两侧较大值，合并是幂等、可交换、可结合的。
//
// 为避免互逆方向合并同时进行时死锁，先在 src 读锁下拷贝快照并释放，
// 再在 s 写锁下应用，任意时刻最多持有一把锁。
func (s *Set) Merge(src *Set) error {
	if src == nil {
		return ErrNilReplica
	}
	if src == s {
		return ErrSelfMerge
	}
	src.mu.RLock()
	snap := make(map[string]Record, len(src.records))
	for e, rec := range src.records {
		snap[e] = rec
	}
	fromSeq := src.seq
	src.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.canFitLocked(keysOfRecords(snap)) {
		return ErrTooManyElements
	}
	for e, rec := range snap {
		s.applyLocked(e, rec)
	}
	if fromSeq > s.mergePos[src.id] {
		s.mergePos[src.id] = fromSeq
	}
	return nil
}

// MergeIncremental 只合并 src 中自上次合并（整份或增量）以来的变更，
// 结果与整份合并一致。合并位置按源副本编号记录，被拒时不推进。
func (s *Set) MergeIncremental(src *Set) error {
	if src == nil {
		return ErrNilReplica
	}
	if src == s {
		return ErrSelfMerge
	}
	src.mu.RLock()
	from := s.mergePosOf(src.id)
	entries := make([]changeEntry, 0)
	for _, e := range src.log {
		if e.seq > from {
			entries = append(entries, e)
		}
	}
	toSeq := src.seq
	src.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.canFitLocked(keysOfEntries(entries)) {
		return ErrTooManyElements
	}
	for _, e := range entries {
		s.applyLocked(e.elem, e.record)
	}
	if toSeq > s.mergePos[src.id] {
		s.mergePos[src.id] = toSeq
	}
	return nil
}

// canFitLocked 预检合并后记录元素数是否超上限，保证拒绝时不留痕。
func (s *Set) canFitLocked(elems []string) bool {
	need := len(s.records)
	seen := make(map[string]struct{}, len(elems))
	for _, e := range elems {
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		if _, ok := s.records[e]; !ok {
			need++
		}
	}
	return need <= s.maxElems
}

func keysOfRecords(m map[string]Record) []string {
	out := make([]string, 0, len(m))
	for e := range m {
		out = append(out, e)
	}
	return out
}

func keysOfEntries(entries []changeEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.elem)
	}
	return out
}

// mergePosOf 读取 s 对源副本 srcID 的合并位置（在 s 未持锁时调用，自带锁）。
func (s *Set) mergePosOf(srcID string) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mergePos[srcID]
}

// MergePosition 返回 s 对源副本 srcID 已合并到的变更序号。
func (s *Set) MergePosition(srcID string) uint64 {
	return s.mergePosOf(srcID)
}
