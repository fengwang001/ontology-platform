// Package segment 实现日志段：记录追加、物理位置、累计字节与
// 稀疏索引的建立规则、按起始位置的顺序扫描。依赖 idx。
package segment

import (
	"errors"
	"sort"
	"sync/atomic"

	"ontology/idx"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrInvalid   = errors.New("segment: invalid argument")
	ErrOverflow  = errors.New("segment: relative offset exceeds int32")
	ErrBelowBase = errors.New("segment: target below base")
	ErrNotFound  = errors.New("segment: not found")
)

const maxRel = 2147483647 // int32 最大值

// Record 是段内一条记录。
type Record struct {
	Offset int64
	Pos    int64
	Size   int64
}

// Segment 是单个日志段。并发安全由调用方（api 包）保证。
type Segment struct {
	base     int64
	interval int64
	records  []Record
	index    idx.Index
	bytes    int64 // 段内已写字节总数
	acc      int64 // 上一个索引条目之后已写入的字节数
	last     int64
	hasLast  bool
	checked  atomic.Int64 // 最近一次 Lookup 检查的索引条目数与扫描记录数之和
}

// New 要求 base >= 0 且 interval > 0。
func New(base, interval int64) (*Segment, error) {
	if base < 0 || interval <= 0 {
		return nil, ErrInvalid
	}
	return &Segment{base: base, interval: interval}, nil
}

// Append 追加一条记录，返回其物理位置。任何校验失败都不改变状态。
func (s *Segment) Append(offset, size int64) (int64, error) {
	if size <= 0 || offset < s.base || (s.hasLast && offset <= s.last) {
		return 0, ErrInvalid
	}
	rel := offset - s.base
	if rel > maxRel {
		return 0, ErrOverflow
	}
	pos := s.bytes
	if s.acc >= s.interval {
		s.index.Append(int32(rel), pos)
		s.acc = 0
	}
	s.records = append(s.records, Record{Offset: offset, Pos: pos, Size: size})
	s.bytes += size
	s.acc += size
	s.last = offset
	s.hasLast = true
	return pos, nil
}

// Lookup 返回第一条位点 >= target 的记录的位点与物理位置。
func (s *Segment) Lookup(target int64) (int64, int64, error) {
	if target < s.base {
		return 0, 0, ErrBelowBase
	}
	rel := target - s.base
	if rel > maxRel {
		rel = maxRel
	}
	checked := 0
	start := int64(0)
	e, ok, n := s.index.Floor(int32(rel))
	checked += n
	if ok {
		start = e.Pos
	}
	// 从起始位置起逐条扫描，返回第一条位点 >= target 的记录。
	i := sort.Search(len(s.records), func(i int) bool { return s.records[i].Pos >= start })
	for ; i < len(s.records); i++ {
		checked++
		if s.records[i].Offset >= target {
			s.checked.Store(int64(checked))
			return s.records[i].Offset, s.records[i].Pos, nil
		}
	}
	s.checked.Store(int64(checked))
	return 0, 0, ErrNotFound
}

// Entries 返回全部索引条目的副本。
func (s *Segment) Entries() []idx.Entry { return s.index.Entries() }

// Records 返回全部记录的副本。
func (s *Segment) Records() []Record {
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}

// Bytes 返回段内已写字节总数。
func (s *Segment) Bytes() int64 { return s.bytes }
