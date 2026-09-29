// Package changelog 实现只追加日志（append-only log）的按键压缩合并器。
//
// 日志条目带从 1 开始连续递增的序号。压缩（Compact）可对某个闭区间
// [left, right] 内同一键的多条写入只保留序号最大的一条（最后一条），
// 区间外的条目原样保留。压缩后，对任意历史位点 t 调用 Read(key, t)
// 仍可寻址，返回该位点下该键所有可见条目中序号最大者；没有任何可见
// 条目时返回 ErrNotFound，而不是其它错误。
package changelog

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的拒绝原因。调用方可用 errors.Is 判定。
var (
	// ErrEmptyKey 表示写入或读取使用了空键。
	ErrEmptyKey = errors.New("changelog: key must not be empty")
	// ErrPositionOutOfRange 表示读位点不在 [1, NextSeq-1] 内。
	ErrPositionOutOfRange = errors.New("changelog: read position out of range")
	// ErrRangeLeftTooSmall 表示压缩区间左边界小于 1。
	ErrRangeLeftTooSmall = errors.New("changelog: compact range left boundary must be >= 1")
	// ErrRangeRightTooLarge 表示压缩区间右边界超过当前最后一个序号。
	ErrRangeRightTooLarge = errors.New("changelog: compact range right boundary must not exceed last sequence")
	// ErrRangeInverted 表示压缩区间左右倒置（left > right）。
	ErrRangeInverted = errors.New("changelog: compact range is inverted (left > right)")
	// ErrNotFound 表示指定位点下该键没有任何可见条目。
	ErrNotFound = errors.New("changelog: key not found at position")
)

// Entry 是一条日志写入。
type Entry struct {
	Seq   int
	Key   string
	Value string
}

// CompactRecord 是一次压缩产生的合并记录。
// 同一键在同一压缩区间内至多一条，序号取该键在区间内最后一条写入的序号。
type CompactRecord struct {
	Seq   int
	Key   string
	Value string
}

// slot 保存一个序号位点上的当前可见条目，以及它是否为压缩合并记录。
type slot struct {
	entry   Entry
	compact bool
}

// Log 是并发安全的只追加日志压缩器。
// 读（Read）与自检（SelfCheck）使用读锁，可彼此并发，
// 也与追加（Append）/压缩（Compact）通过 RWMutex 互斥协调：
// 压缩持写锁，读永远不会观察到压缩的中间状态。
type Log struct {
	mu     sync.RWMutex
	slots  []slot  // 下标 seq-1；被合并掉的位点为零值空槽
	next   int     // 下一序号（首条写入为 1）
	raw    []Entry // 追加历史的只读副本，仅供自检与参照重建
	lastCR []CompactRecord
}

// New 创建空日志。
func New() *Log {
	return &Log{next: 1}
}

// Append 追加一条写入，返回分配的序号。
func (l *Log) Append(key, value string) (int, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	seq := l.next
	e := Entry{Seq: seq, Key: key, Value: value}
	l.slots = append(l.slots, slot{entry: e})
	l.raw = append(l.raw, e)
	l.next++
	return seq, nil
}

// Read 返回位点 position 时 key 的可见值（可见条目中序号最大者）。
func (l *Log) Read(key string, position int) (Entry, error) {
	if key == "" {
		return Entry{}, ErrEmptyKey
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if position < 1 || position > l.next-1 {
		return Entry{}, ErrPositionOutOfRange
	}
	for i := position - 1; i >= 0; i-- {
		s := &l.slots[i]
		if s.entry.Key != "" && s.entry.Key == key {
			return s.entry, nil
		}
	}
	return Entry{}, ErrNotFound
}

// Compact 对闭区间 [left, right] 按键合并，只保留每个键的最后一条写入。
// 区间外条目原样保留；区间内没有写入的键不产生记录。
// 参数校验失败时整体拒绝，不会改动任何状态。
func (l *Log) Compact(left, right int) ([]CompactRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	last := l.next - 1
	switch {
	case left < 1:
		return nil, ErrRangeLeftTooSmall
	case right > last:
		return nil, ErrRangeRightTooLarge
	case left > right:
		return nil, ErrRangeInverted
	}

	// 第一遍：找出区间内每个键的最后一条可见写入。
	lastSeqByKey := make(map[string]int)
	for i := left - 1; i < right; i++ {
		k := l.slots[i].entry.Key
		if k == "" {
			continue
		}
		lastSeqByKey[k] = i + 1
	}

	// 第二遍：非末位的同键条目清空，末位标记为压缩记录。
	records := make([]CompactRecord, 0, len(lastSeqByKey))
	for i := left - 1; i < right; i++ {
		s := &l.slots[i]
		k := s.entry.Key
		if k == "" {
			continue
		}
		if i+1 == lastSeqByKey[k] {
			s.compact = true
			records = append(records, CompactRecord{
				Seq:   s.entry.Seq,
				Key:   s.entry.Key,
				Value: s.entry.Value,
			})
		} else {
			*s = slot{}
		}
	}
	sortCompactRecords(records)
	l.lastCR = records
	return cloneCompactRecords(records), nil
}

// NextSeq 返回下一序号（= 已追加条数 + 1）。
func (l *Log) NextSeq() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.next
}

// LastCompactRecords 返回最近一次压缩产生的合并记录快照。
func (l *Log) LastCompactRecords() []CompactRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return cloneCompactRecords(l.lastCR)
}

// SelfCheck 在与读相同的并发条件下核对全部 (键, 位点) 的读取结果
// 与只使用区间内可见条目重建的批量参照逐值一致。
func (l *Log) SelfCheck() error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	last := l.next - 1
	if len(l.slots) != last || len(l.raw) != last {
		return fmt.Errorf("changelog: internal gap, next=%d slots=%d raw=%d", l.next, len(l.slots), len(l.raw))
	}

	// 结构不变量：空槽只可能来自压缩；每条被合并掉的原始写入，
	// 必须能在更靠后的位点找到同键的存活条目（合并保留的就是最后一条）。
	for i := 0; i < last; i++ {
		s := &l.slots[i]
		raw := l.raw[i]
		if s.entry.Key != "" {
			if s.entry != raw {
				return fmt.Errorf("changelog: slot %d mutated: got %+v want %+v", i+1, s.entry, raw)
			}
			continue
		}
		found := false
		for j := i + 1; j < last; j++ {
			if l.slots[j].entry.Key == raw.Key {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("changelog: entry %d for key %q compacted away without later survivor", raw.Seq, raw.Key)
		}
	}

	// 批量参照：按位点正向扫描可见槽位，维护 key -> 最新可见条目，
	// 每个位点对每个出现过的键与 Read 的单点查询逐值比对。
	latest := make(map[string]Entry)
	keys := make([]string, 0)
	for i := 0; i < last; i++ {
		e := l.slots[i].entry
		if e.Key != "" {
			if _, ok := latest[e.Key]; !ok {
				keys = append(keys, e.Key)
			}
			latest[e.Key] = e
		}
		position := i + 1
		for _, k := range keys {
			got, err := l.readLocked(k, position)
			if err != nil {
				return fmt.Errorf("changelog: read(%q, %d) unexpected error: %v", k, position, err)
			}
			if want := latest[k]; got != want {
				return fmt.Errorf("changelog: read(%q, %d) = %+v, want %+v", k, position, got, want)
			}
		}
	}
	return nil
}

// readLocked 调用方必须已持有读锁（或写锁）。
func (l *Log) readLocked(key string, position int) (Entry, error) {
	for i := position - 1; i >= 0; i-- {
		s := &l.slots[i]
		if s.entry.Key == key {
			return s.entry, nil
		}
	}
	return Entry{}, ErrNotFound
}

func sortCompactRecords(r []CompactRecord) {
	sort.Slice(r, func(a, b int) bool { return r[a].Seq < r[b].Seq })
}

func cloneCompactRecords(r []CompactRecord) []CompactRecord {
	if len(r) == 0 {
		return []CompactRecord{}
	}
	out := make([]CompactRecord, len(r))
	copy(out, r)
	return out
}
