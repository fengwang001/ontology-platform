// Package segment 实现日志段：记录、物理位置、累计字节与稀疏建索引规则、顺序扫描。
package segment

import (
	"errors"
	"math"
	"sort"
	"sync/atomic"

	"ontology/idx"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrInvalid     = errors.New("segment: invalid argument or append")
	ErrRelOverflow = errors.New("segment: relative offset exceeds int32")
	ErrBelowBase   = errors.New("segment: lookup target below base")
	ErrNotFound    = errors.New("segment: no record with offset >= target")
)

// Record 是段内一条记录。
type Record struct {
	Offset int64
	Pos    int64
	Size   int64
}

// Segment 是单个日志段。无内部锁，串行化由调用方负责。
type Segment struct {
	base     int64
	interval int64
	records  []Record
	index    idx.Index
	bytes    int64 // 段内已写字节总数
	accum    int64 // 上一个索引条目之后写入的字节数
	lastOff  int64
	hasLast  bool
	checked  atomic.Int64 // 最近一次 Lookup 检查的索引条目数与扫描记录数之和
}

// New 要求 base >= 0 且 indexInterval > 0。
func New(base, indexInterval int64) (*Segment, error) {
	if base < 0 || indexInterval <= 0 {
		return nil, ErrInvalid
	}
	return &Segment{base: base, interval: indexInterval}, nil
}

// Append 追加一条记录，返回其物理位置。任何校验失败都不改变状态。
func (s *Segment) Append(offset, size int64) (int64, error) {
	if size <= 0 || offset < s.base || (s.hasLast && offset <= s.lastOff) {
		return 0, ErrInvalid
	}
	rel := offset - s.base
	if rel > math.MaxInt32 {
		return 0, ErrRelOverflow
	}
	pos := s.bytes
	if s.accum >= s.interval {
		s.index.Add(int32(rel), pos)
		s.accum = 0
	}
	s.records = append(s.records, Record{Offset: offset, Pos: pos, Size: size})
	s.bytes += size
	s.accum += size
	s.lastOff = offset
	s.hasLast = true
	return pos, nil
}

// Lookup 返回第一条位点 >= target 的记录的位点与物理位置，以及扫描记录数。
func (s *Segment) Lookup(target int64) (int64, int64, int, error) {
	if target < s.base {
		return 0, 0, 0, ErrBelowBase
	}
	rel := target - s.base
	if rel > math.MaxInt32 {
		rel = math.MaxInt32
	}
	var start int64
	entry, ok, nchecked := s.index.Floor(int32(rel))
	if ok {
		start = entry.Pos
	}
	i := sort.Search(len(s.records), func(i int) bool { return s.records[i].Pos >= start })
	scanned := 0
	for ; i < len(s.records); i++ {
		scanned++
		if s.records[i].Offset >= target {
			s.checked.Store(int64(nchecked + scanned))
			return s.records[i].Offset, s.records[i].Pos, scanned, nil
		}
	}
	s.checked.Store(int64(nchecked + scanned))
	return 0, 0, scanned, ErrNotFound
}

// Entries 返回全部索引条目。
func (s *Segment) Entries() []idx.Entry { return s.index.Entries() }

// Records 返回全部记录的副本（供自检与测试核对）。
func (s *Segment) Records() []Record {
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}
