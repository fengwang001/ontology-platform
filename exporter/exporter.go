// Package exporter 提供一致位点导出器：写入持续进行时，
// 导出会话先吐快照段（序号不超过导出起点），再吐增量段（序号大于起点），
// 两段无缝拼接，每条记录恰好出现一次且全局升序、可复现。
package exporter

import (
	"errors"
	"fmt"
	"sync"
)

// 可区分的拒绝原因。所有被拒绝的操作都不会改变日志、序号与任何会话状态。
var (
	ErrEmptyKey             = errors.New("exporter: 键为空，写入被拒绝")
	ErrExportNotStarted     = errors.New("exporter: 导出会话尚未开始")
	ErrExportAlreadyStarted = errors.New("exporter: 导出会话已开始，不能重复开始")
	ErrExportAlreadyEnded   = errors.New("exporter: 导出会话已结束")
	ErrExportLimitExceeded  = errors.New("exporter: 未结束导出的记录数超出上限")
)

// Segment 标记一条发出的记录属于快照段还是增量段。
type Segment int

const (
	SegmentSnapshot Segment = iota
	SegmentIncremental
)

func (s Segment) String() string {
	if s == SegmentSnapshot {
		return "snapshot"
	}
	return "incremental"
}

// Record 是一条带连续递增序号的日志记录。
type Record struct {
	Seq   uint64
	Key   string
	Value string
}

// Range 描述一段已发出的连续序号区间；Count 为 0 表示空段。
type Range struct {
	First uint64
	Last  uint64
	Count int
}

// Report 是导出结束时返回的两段区间报告。
type Report struct {
	Snapshot    Range
	Incremental Range
	Total       int
}

// Verify 核验两段无缝拼接且从 1 开始连续。
func (r Report) Verify() error {
	if r.Total != r.Snapshot.Count+r.Incremental.Count {
		return fmt.Errorf("exporter: 总数 %d 与两段计数 %d+%d 不符", r.Total, r.Snapshot.Count, r.Incremental.Count)
	}
	if r.Snapshot.Count > 0 {
		if r.Snapshot.First != 1 {
			return fmt.Errorf("exporter: 快照段未从序号 1 开始（实际 %d）", r.Snapshot.First)
		}
		if r.Snapshot.Last-r.Snapshot.First+1 != uint64(r.Snapshot.Count) {
			return fmt.Errorf("exporter: 快照段 [%d,%d] 与计数 %d 不连续", r.Snapshot.First, r.Snapshot.Last, r.Snapshot.Count)
		}
	}
	if r.Incremental.Count > 0 {
		wantFirst := r.Snapshot.Last + 1
		if r.Incremental.First != wantFirst {
			return fmt.Errorf("exporter: 增量段起点 %d 与快照段终点 %d 不衔接", r.Incremental.First, r.Snapshot.Last)
		}
		if r.Incremental.Last-r.Incremental.First+1 != uint64(r.Incremental.Count) {
			return fmt.Errorf("exporter: 增量段 [%d,%d] 与计数 %d 不连续", r.Incremental.First, r.Incremental.Last, r.Incremental.Count)
		}
	}
	return nil
}

// Log 是只追加的记录日志，序号连续递增，校验通过才分配。
type Log struct {
	mu      sync.Mutex
	records []Record
}

func NewLog() *Log {
	return &Log{}
}

// Append 校验通过后分配连续递增序号并追加记录；校验失败不分配序号。
func (l *Log) Append(key, value string) (uint64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	seq := uint64(len(l.records)) + 1
	l.records = append(l.records, Record{Seq: seq, Key: key, Value: value})
	return seq, nil
}

// Len 返回当前已分配的最大序号。
func (l *Log) Len() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return uint64(len(l.records))
}

// ReadAll 从头顺序读出全部记录，用于本地核对导出结果。
func (l *Log) ReadAll() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Record, len(l.records))
	copy(out, l.records)
	return out
}

// get 按序号读取记录；序号尚未写入时返回 false。
func (l *Log) get(seq uint64) (Record, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq == 0 || seq > uint64(len(l.records)) {
		return Record{}, false
	}
	return l.records[seq-1], true
}

// NewSession 创建一个导出会话；limit 为会话结束前允许发出的最大记录数。
func (l *Log) NewSession(limit int) *Session {
	return &Session{log: l, limit: limit}
}

// Session 是一次导出会话：先快照段后增量段，严格按序发出。
type Session struct {
	log   *Log
	limit int

	mu      sync.Mutex
	started bool
	ended   bool
	start   uint64
	next    uint64
	emitted int

	snap Range
	incr Range
}

// Begin 记录导出起点序号，开始会话。
func (s *Session) Begin() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return ErrExportAlreadyEnded
	}
	if s.started {
		return ErrExportAlreadyStarted
	}
	s.start = s.log.Len()
	s.next = 1
	s.started = true
	return nil
}

// NextN 按序发出至多 n 条记录；快照段记录必定存在，
// 增量段只在下一条待发序号对应的记录已存在时才发出，不跳读。
// 若本次请求会使未结束导出的总数超限，则整体拒绝，不改变任何状态。
func (s *Session) NextN(n int) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return nil, ErrExportNotStarted
	}
	if s.ended {
		return nil, ErrExportAlreadyEnded
	}
	if n <= 0 {
		return nil, nil
	}
	if s.emitted+n > s.limit {
		return nil, ErrExportLimitExceeded
	}
	out := make([]Record, 0, n)
	for len(out) < n {
		rec, ok := s.log.get(s.next)
		if !ok {
			if s.next <= s.start {
				return nil, fmt.Errorf("exporter: 快照记录 %d 缺失，日志内部不一致", s.next)
			}
			break
		}
		s.track(rec.Seq)
		out = append(out, rec)
		s.next++
		s.emitted++
	}
	return out, nil
}

// SegmentOf 按序号判定归属段：序号不超过起点属于快照段，否则属于增量段。
func (s *Session) SegmentOf(seq uint64) Segment {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq <= s.start {
		return SegmentSnapshot
	}
	return SegmentIncremental
}

// track 按序号把已发出记录计入对应段的区间。调用方须持有 s.mu。
func (s *Session) track(seq uint64) {
	r := &s.snap
	if seq > s.start {
		r = &s.incr
	}
	if r.Count == 0 {
		r.First = seq
	}
	r.Last = seq
	r.Count++
}

// End 结束会话，报告两段区间并核验无缝。
func (s *Session) End() (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return Report{}, ErrExportNotStarted
	}
	if s.ended {
		return Report{}, ErrExportAlreadyEnded
	}
	s.ended = true
	rep := Report{
		Snapshot:    s.snap,
		Incremental: s.incr,
		Total:       s.emitted,
	}
	if err := rep.Verify(); err != nil {
		return rep, err
	}
	return rep, nil
}
