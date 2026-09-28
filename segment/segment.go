// Package segment 提供只追加日志段及其稀疏位点索引。
package segment

import (
	"errors"
	"io"
	"log/slog"
	"math"
	"os"
	"sync"
)

// MaxRelativeOffset 是索引条目允许存储的最大相对位点。
const MaxRelativeOffset uint64 = 1<<32 - 1

var (
	// ErrInvalidArgument 表示追加或查找参数非法（空数据、负位点等）。
	ErrInvalidArgument = errors.New("segment: invalid argument")
	// ErrOffsetNotStrictlyIncreasing 表示追加位点未严格递增。
	ErrOffsetNotStrictlyIncreasing = errors.New("segment: offset not strictly increasing")
	// ErrRelativeOffsetOverflow 表示相对位点超出受限整数存储范围。
	ErrRelativeOffsetOverflow = errors.New("segment: relative offset overflow")
	// ErrPhysicalPositionOverflow 表示段内累计字节数溢出。
	ErrPhysicalPositionOverflow = errors.New("segment: physical position overflow")
	// ErrSeekBelowBase 表示查找目标位点低于段基位点。
	ErrSeekBelowBase = errors.New("segment: seek target below base offset")
	// ErrNotFound 表示没有位点不小于目标的记录。
	ErrNotFound = errors.New("segment: no record at or after target offset")
	// ErrEmptySegment 表示在空段上执行查找。
	ErrEmptySegment = errors.New("segment: segment is empty")
	// ErrInvalidInterval 表示构造段时给出的索引字节间隔非法。
	ErrInvalidInterval = errors.New("segment: index interval must be positive")
)

// Record 是一条按位点追加的日志记录。
type Record struct {
	// Offset 为记录的全局位点。
	Offset int64
	// Data 为记录负载，其长度即记录占用的字节数。
	Data []byte

	physicalPosition uint64
}

// PhysicalPosition 返回记录在段内的物理位置，即之前所有记录字节数之和。
func (r Record) PhysicalPosition() uint64 { return r.physicalPosition }

// Entry 是一条稀疏索引条目。
type Entry struct {
	// RelativeOffset 为相对基位点的位点偏移，以 32 位无符号整数存储。
	RelativeOffset uint32
	// PhysicalPosition 为被索引记录在段内的物理位置。
	PhysicalPosition uint64

	recordIndex int
}

// Segment 是并发安全、只追加的日志段。
type Segment struct {
	mu       sync.RWMutex
	interval uint64
	base     int64
	size     uint64
	records  []Record
	entries  []Entry
	logger   *slog.Logger
}

// Option 配置新构造的段。
type Option func(*Segment)

// WithLogger 指定段使用的结构化日志器。
func WithLogger(logger *slog.Logger) Option {
	return func(s *Segment) {
		if logger != nil {
			s.logger = logger
		}
	}
}

// WithLogWriter 指定段日志输出位置（默认 os.Stderr）。
func WithLogWriter(w io.Writer) Option {
	return func(s *Segment) {
		if w != nil {
			s.logger = slog.New(slog.NewTextHandler(w, nil))
		}
	}
}

// New 创建一个每累计 interval 字节记录一个索引条目的段。
func New(interval uint64, opts ...Option) (*Segment, error) {
	if interval == 0 {
		return nil, ErrInvalidInterval
	}
	s := &Segment{
		interval: interval,
		base:     math.MinInt64,
		logger:   slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Append 按严格递增的位点追加一条记录；被拒绝时段状态保持不变。
func (s *Segment) Append(offset int64, data []byte) error {
	if offset < 0 || len(data) == 0 {
		s.logger.Warn("append rejected",
			"reason", "invalid-argument",
			"input_offset", offset,
			"data_bytes", len(data))
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.records)
	if n > 0 {
		last := s.records[n-1].Offset
		if offset <= last {
			s.logger.Warn("append rejected",
				"reason", "offset-not-strictly-increasing",
				"input_offset", offset,
				"last_offset", last)
			return ErrOffsetNotStrictlyIncreasing
		}
	}

	base := offset
	if n > 0 {
		base = s.base
	}
	rel := uint64(offset) - uint64(base)
	if rel > MaxRelativeOffset {
		s.logger.Warn("append rejected",
			"reason", "relative-offset-overflow",
			"input_offset", offset,
			"base_offset", base,
			"relative_offset", rel,
			"max_relative_offset", MaxRelativeOffset)
		return ErrRelativeOffsetOverflow
	}

	dataLen := uint64(len(data))
	if dataLen > math.MaxUint64-s.size {
		s.logger.Warn("append rejected",
			"reason", "physical-position-overflow",
			"input_offset", offset,
			"segment_size", s.size,
			"data_bytes", dataLen)
		return ErrPhysicalPositionOverflow
	}

	// 先判定是否记录索引条目，再真正写入记录。
	recordIndex := n
	physicalPosition := s.size
	shouldIndex := n == 0
	if !shouldIndex {
		lastEntry := s.entries[len(s.entries)-1]
		shouldIndex = physicalPosition-lastEntry.PhysicalPosition >= s.interval
	}

	payload := make([]byte, len(data))
	copy(payload, data)
	record := Record{
		Offset:           offset,
		Data:             payload,
		physicalPosition: physicalPosition,
	}

	if n == 0 {
		s.base = offset
	}

	s.logger.Info("append",
		"input_offset", offset,
		"data_bytes", dataLen,
		"physical_position", physicalPosition,
		"indexed", shouldIndex,
		"index_entries_before", len(s.entries))

	if shouldIndex {
		s.entries = append(s.entries, Entry{
			RelativeOffset:   uint32(rel),
			PhysicalPosition: physicalPosition,
			recordIndex:      recordIndex,
		})
	}
	s.records = append(s.records, record)
	s.size += dataLen

	s.logger.Info("append committed",
		"offset", offset,
		"segment_size", s.size,
		"index_entries", len(s.entries),
		"index", s.entries)
	return nil
}

// Seek 返回位点不小于 target 的第一条记录，结果与从头逐条扫描一致。
func (s *Segment) Seek(target int64) (Record, error) {
	if target < 0 {
		s.logger.Warn("seek rejected",
			"reason", "invalid-argument",
			"target", target)
		return Record{}, ErrInvalidArgument
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.records) == 0 {
		s.logger.Warn("seek rejected",
			"reason", "empty-segment",
			"target", target)
		return Record{}, ErrEmptySegment
	}
	if target < s.base {
		s.logger.Warn("seek rejected",
			"reason", "seek-below-base",
			"target", target,
			"base_offset", s.base)
		return Record{}, ErrSeekBelowBase
	}

	// 在索引中定位不超过目标的最后一个条目，确定顺序扫描起点。
	start := 0
	foundEntry := -1
	lo, hi := 0, len(s.entries)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		entryOffset := s.base + int64(s.entries[mid].RelativeOffset)
		if entryOffset <= target {
			lo = m + 1
		} else {
			hi = mid
		}
	}
	if lo > 0 {
		foundEntry = lo - 1
		start = s.entries[foundEntry].recordIndex
	}

	s.logger.Info("seek",
		"target", target,
		"base_offset", s.base,
		"index", s.entries,
		"entry_floor", foundEntry,
		"scan_start_record", start)

	for i := start; i < len(s.records); i++ {
		rec := s.records[i]
		if rec.Offset >= target {
			s.logger.Info("seek hit",
				"target", target,
				"floor_entry", foundEntry,
				"matched_offset", rec.Offset,
				"physical_position", rec.physicalPosition,
				"basis", "first record at or after target from sparse-index start")
			return rec, nil
		}
	}

	s.logger.Info("seek miss",
		"target", target,
		"floor_entry", foundEntry,
		"basis", "every record from sparse-index start is below target")
	return Record{}, ErrNotFound
}

// BaseOffset 返回段基位点（首条记录的位点）；空段返回 ErrEmptySegment。
func (s *Segment) BaseOffset() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.records) == 0 {
		return 0, ErrEmptySegment
	}
	return s.base, nil
}

// Size 返回段内已写入的总字节数。
func (s *Segment) Size() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.size
}

// Len 返回已追加的记录条数。
func (s *Segment) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// Entries 返回当前稀疏索引条目的拷贝。
func (s *Segment) Entries() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Records 返回全部记录的拷贝。
func (s *Segment) Records() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}
