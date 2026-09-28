package lwwset

import "sort"

// validateOp 校验元素与时间戳，任一非法则整体拒绝。
func (s *Set) validateOp(elem string, ts int64) error {
	if elem == "" {
		return ErrEmptyElement
	}
	if ts <= 0 {
		return ErrNonPositiveTimestamp
	}
	return nil
}

// applyLocked 在持锁状态下将 rec 按“两条记录分别取较大值”并入 elem，
// 返回记录是否变大。调用前须完成参数校验。
func (s *Set) applyLocked(elem string, rec Record) bool {
	cur, ok := s.records[elem]
	if ok && rec.Add <= cur.Add && rec.Remove <= cur.Remove {
		return false
	}
	if rec.Add > cur.Add {
		cur.Add = rec.Add
	}
	if rec.Remove > cur.Remove {
		cur.Remove = rec.Remove
	}
	s.records[elem] = cur
	s.seq++
	s.log = append(s.log, changeEntry{seq: s.seq, elem: elem, record: cur})
	return true
}

// Add 以时间戳 ts 添加元素。旧时间戳到达不会把记录改小，但仍视为成功。
func (s *Set) Add(elem string, ts int64) error {
	if err := s.validateOp(elem, ts); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[elem]; !ok && len(s.records) >= s.maxElems {
		return ErrTooManyElements
	}
	s.applyLocked(elem, Record{Add: ts})
	return nil
}

// Remove 以时间戳 ts 删除元素。删除不存在的元素也会留下删除记录（墓碑）。
func (s *Set) Remove(elem string, ts int64) error {
	if err := s.validateOp(elem, ts); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[elem]; !ok && len(s.records) >= s.maxElems {
		return ErrTooManyElements
	}
	s.applyLocked(elem, Record{Remove: ts})
	return nil
}

// Contains 判定元素当前是否在集合中。
func (s *Set) Contains(elem string) bool {
	if elem == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[elem]
	return ok && rec.Alive()
}

// Lookup 返回元素的记录及是否存在记录。
func (s *Set) Lookup(elem string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[elem]
	return rec, ok
}

// Elements 返回当前存活元素的有序快照。
func (s *Set) Elements() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.records))
	for e, rec := range s.records {
		if rec.Alive() {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// Len 返回当前存活元素个数。
func (s *Set) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, rec := range s.records {
		if rec.Alive() {
			n++
		}
	}
	return n
}

// Seq 返回当前变更序号。
func (s *Set) Seq() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq
}

// Snapshot 返回全部记录的副本（键为元素，值为两条时间戳记录）。
func (s *Set) Snapshot() map[string]Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Record, len(s.records))
	for e, rec := range s.records {
		out[e] = rec
	}
	return out
}

// Check 自检内部不变量：变更日志序号严格递增、日志快照与当前记录一致
// （日志中每元素最后一条快照等于当前记录）、记录数未超上限。
// 返回 nil 表示一致，否则返回描述性错误。
func (s *Set) Check() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.records) > s.maxElems {
		return ErrTooManyElements
	}
	last := make(map[string]Record, len(s.records))
	var prev uint64
	for i, e := range s.log {
		if e.seq == 0 || (i > 0 && e.seq <= prev) {
			return errCorrupt("变更日志序号非严格递增")
		}
		prev = e.seq
		last[e.elem] = e.record
	}
	if prev != s.seq {
		return errCorrupt("变更序号与日志末尾不一致")
	}
	for elem, rec := range last {
		if s.records[elem] != rec {
			return errCorrupt("日志快照与当前记录不一致")
		}
	}
	return nil
}

type corruptError string

func (e corruptError) Error() string { return "lwwset: 自检失败: " + string(e) }

func errCorrupt(msg string) error { return corruptError(msg) }
