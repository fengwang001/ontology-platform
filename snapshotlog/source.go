package snapshotlog

import "sync"

// Source 是并发安全的源表：每次写入（Put/Delete）都原子地追加一条
// 带连续序号的日志，并更新当前表状态。快照读取基于同一把锁，保证
// 快照与日志序号有确定的先后关系。
type Source struct {
	mu    sync.Mutex
	next  int64            // 下一个日志序号（已分配最大序号 + 1）
	log   []Entry          // 日志，Seq 连续：log[i].Seq == i+1
	table map[int64]string // 当前表状态（存活键）
}

// NewSource 创建空源表，日志序号从 1 开始。
func NewSource() *Source {
	return &Source{
		next:  1,
		log:   make([]Entry, 0),
		table: make(map[int64]string),
	}
}

// appendLocked 追加一条日志并推进序号，调用方持锁。
func (s *Source) appendLocked(op Op, key int64, value string) int64 {
	seq := s.next
	s.next++
	s.log = append(s.log, Entry{Seq: seq, Op: op, Key: key, Value: value})
	return seq
}

// Put 写入键值并追加日志，返回新分配的序号。
func (s *Source) Put(key int64, value string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.appendLocked(OpPut, key, value)
	s.table[key] = value
	return seq
}

// Delete 删除键并追加日志；键不存在时仍会追加一条删除日志，返回其序号。
func (s *Source) Delete(key int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.appendLocked(OpDelete, key, "")
	delete(s.table, key)
	return seq
}

// Snapshot 返回某一瞬间的日志序号（已追加的最大序号）以及该时刻
// 落在 [start,end] 范围内的存活键值副本。
//
// 返回的 seq 语义：快照内容反映了序号 <= seq 的全部写入。
type snapshotResult struct {
	seq  int64
	rows map[int64]string
}

func (s *Source) snapshot(r KeyRange) snapshotResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.next - 1 // 已追加的最大序号；空表时为 0
	rows := make(map[int64]string)
	for k, v := range s.table {
		if r.Contains(k) {
			rows[k] = v
		}
	}
	return snapshotResult{seq: seq, rows: rows}
}

// clampIndex 把日志序号边界夹紧到合法切片下标 [0, len]。
func (s *Source) clampIndex(seq int64) int {
	if seq < 0 {
		return 0
	}
	if seq > int64(len(s.log)) {
		return len(s.log)
	}
	return int(seq)
}

// entriesBetween 返回序号在 (after, <=to] 区间内的日志条目副本。
// 日志 log[i].Seq == i+1，因此对应切片下标 [after, to)。
func (s *Source) entriesBetween(after, to int64) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	lo := s.clampIndex(after)
	hi := s.clampIndex(to)
	if lo >= hi {
		return nil
	}
	return append([]Entry(nil), s.log[lo:hi]...)
}

// entriesAfter 返回序号 > after 的全部日志条目副本（轮询用）。
func (s *Source) entriesAfter(after int64) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	lo := s.clampIndex(after)
	if lo >= len(s.log) {
		return nil
	}
	return append([]Entry(nil), s.log[lo:]...)
}
