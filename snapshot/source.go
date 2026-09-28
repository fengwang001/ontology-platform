package snapshot

import (
	"sort"
	"sync"
)

// Key 是源表与视图的键，按整数全序比较。
type Key int64

// Value 是键对应的值。
type Value string

// Op 是日志条目的写入类型。
type Op int

const (
	OpPut    Op = iota // 写入/更新
	OpDelete           // 删除
)

// Entry 是带连续序号的增量日志条目。序号从 1 开始、严格递增、不跳号。
type Entry struct {
	Seq   int64
	Key   Key
	Op    Op
	Value Value // OpDelete 时为空
}

// KV 是快照/视图中的一个键值行。
type KV struct {
	Key   Key
	Value Value
}

// Source 是带连续序号追加日志的并发安全源表。
//
// 每次 Put/Delete 都在同一把互斥锁内完成"改表 + 追加日志 + 推进序号"，
// 因此表状态、日志前缀、最后序号三者对外部始终是同一个原子时刻的视图。
type Source struct {
	mu   sync.RWMutex
	data map[Key]Value
	log  []Entry // 下标 i 对应的序号为 i+1，即 log[seq-1]
	seq  int64
}

// NewSource 创建空源表。
func NewSource() *Source {
	return &Source{data: make(map[Key]Value)}
}

// Put 写入一个键值，并追加一条带连续序号的日志，返回该条日志序号。
func (s *Source) Put(key Key, value Value) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.data[key] = value
	s.log = append(s.log, Entry{Seq: s.seq, Key: key, Op: OpPut, Value: value})
	return s.seq, nil
}

// Delete 删除一个键，并追加一条删除日志，返回该条日志序号。
// 对不存在的键也会记录日志（对空表删除不改变表状态）。
func (s *Source) Delete(key Key) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	delete(s.data, key)
	s.log = append(s.log, Entry{Seq: s.seq, Key: key, Op: OpDelete})
	return s.seq, nil
}

// LastSeq 返回当前最后一条已提交日志的序号（无写入时为 0，即低水位初值）。
func (s *Source) LastSeq() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq
}

// Snapshot 返回闭区间 [start, end] 内在某一致时刻的键值行，按键升序。
// 整个读取在读锁内完成，与某个 Put/Delete 的原子时刻完全对应。
func (s *Source) Snapshot(start, end Key) []KV {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]KV, 0)
	for k, v := range s.data {
		if k >= start && k <= end {
			out = append(out, KV{Key: k, Value: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// ReadLog 原子地返回读取时刻的最后序号 high，以及序号满足
// after < seq <= high、且键落在闭区间 [start, end] 内的日志（按序号升序）。
//
// high 与日志切片在同一次加锁内取得，保证切片恰好覆盖到 high、不缺不溢；
// 调用方把处理位置推进到 high 后，下一次从 high 之后继续即可不重不漏。
func (s *Source) ReadLog(after int64, start, end Key) (int64, []Entry) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	high := s.seq
	if after >= high {
		return high, nil
	}
	// log[after:] 恰好是 seq > after 的全部条目（下标从 seq-1 起）。
	out := make([]Entry, 0, high-after)
	for _, e := range s.log[after:] {
		if e.Key >= start && e.Key <= end {
			out = append(out, e)
		}
	}
	return high, out
}
