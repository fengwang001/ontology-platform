package journal

import (
	"errors"
	"sync"
)

var ErrAppendFailed = errors.New("日志失败")

// Op 标识被接受的操作类型。
type Op string

const (
	OpSetQuota Op = "setquota"
	OpCreate   Op = "create"
	OpChunk    Op = "chunk"
	OpComplete Op = "complete"
	OpAbort    Op = "abort"
)

// Record 是一条可完整确定回放状态的操作记录。
type Record struct {
	Op     Op
	Tenant string
	Key    string
	SID    int64
	Total  int64
	Quota  int64
	Off    int64
	Data   []byte
	Now    int64
}

// Sink 是日志追加目标。
type Sink interface {
	Append(Record) error
	Records() []Record
}

// MemorySink 是内存 Sink 实现。
type MemorySink struct {
	mu      sync.Mutex
	records []Record
}

func NewMemorySink() *MemorySink {
	return &MemorySink{}
}

func (s *MemorySink) Append(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r = cloneRecord(r)
	s.records = append(s.records, r)
	return nil
}

func (s *MemorySink) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.records))
	for i, r := range s.records {
		out[i] = cloneRecord(r)
	}
	return out
}

// FaultSink 包裹另一个 Sink，可在指定 0 基序号上注入追加失败。
type FaultSink struct {
	inner Sink
	fail  map[int]bool

	mu  sync.Mutex
	seq int
}

func NewFaultSink(inner Sink, failIndices ...int) *FaultSink {
	fail := make(map[int]bool, len(failIndices))
	for _, i := range failIndices {
		fail[i] = true
	}
	return &FaultSink{inner: inner, fail: fail}
}

func (s *FaultSink) Append(r Record) error {
	s.mu.Lock()
	// fail 的序号是 Append 调用序号（0 基）：每次调用都占号，
	// 被注入失败的那次不会落地，但下一次调用按新序号判定。
	idx := s.seq
	s.seq++
	inject := s.fail[idx]
	s.mu.Unlock()
	if inject {
		return ErrAppendFailed
	}
	return s.inner.Append(r)
}

func (s *FaultSink) Records() []Record {
	return s.inner.Records()
}

func cloneRecord(r Record) Record {
	if r.Data != nil {
		r.Data = append([]byte(nil), r.Data...)
	}
	return r
}
