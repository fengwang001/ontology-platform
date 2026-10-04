// Package journal 提供操作日志：被接受的操作先向 Sink 追加记录再生效；
// 追加失败（故障注入）则操作整体拒绝，不留痕。
package journal

import (
	"errors"
	"fmt"
)

// ErrSink 表示 Sink 追加失败导致的「日志失败」。
var ErrSink = errors.New("journal: 日志失败")

// Sink 是日志记录的去处。实现可注入故障。
type Sink interface {
	Append(rec []byte) error
}

// Journal 包装 Sink，仅在追加成功后保留记录副本。
type Journal struct {
	sink    Sink
	records [][]byte
}

// New 返回以 s 为去向的 Journal。
func New(s Sink) *Journal { return &Journal{sink: s} }

// Append 先向 Sink 追加；成功才留痕，失败返回 ErrSink。
func (j *Journal) Append(rec []byte) error {
	if err := j.sink.Append(rec); err != nil {
		return fmt.Errorf("%w: %v", ErrSink, err)
	}
	j.records = append(j.records, append([]byte(nil), rec...))
	return nil
}

// Records 返回已成功追加的记录序列（按序）。
func (j *Journal) Records() [][]byte {
	out := make([][]byte, len(j.records))
	for i, rec := range j.records {
		out[i] = append([]byte(nil), rec...)
	}
	return out
}

// MemSink 是内存 Sink，永不失败。
type MemSink struct {
	Records [][]byte
}

// Append 实现 Sink。
func (s *MemSink) Append(rec []byte) error {
	s.Records = append(s.Records, append([]byte(nil), rec...))
	return nil
}

// FlakySink 是故障注入 Sink：第 n 次 Append 尝试（0 起）若命中
// FailAt 则返回错误，否则委托给内存 Sink。
type FlakySink struct {
	FailAt map[int]bool
	calls  int
	inner  MemSink
}

// Append 实现 Sink。
func (s *FlakySink) Append(rec []byte) error {
	idx := s.calls
	s.calls++
	if s.FailAt[idx] {
		return fmt.Errorf("journal: 注入故障（第 %d 次追加）", idx)
	}
	return s.inner.Append(rec)
}

// Calls 返回已发生的 Append 尝试次数。
func (s *FlakySink) Calls() int { return s.calls }
