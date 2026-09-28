package txnlog

import (
	"io"
)

// Marker 为事务结束标记类型。
type Marker uint8

const (
	// Commit 表示事务提交。
	Commit Marker = iota + 1
	// Abort 表示事务中止。
	Abort
)

// Kind 为日志条目的种类。
type Kind uint8

const (
	// Data 是数据记录。
	Data Kind = iota + 1
	// Control 是提交/中止控制标记。
	Control
)

// Entry 是日志中的一条原始条目（数据记录或控制标记）。
type Entry struct {
	Offset   int64
	Kind     Kind
	Producer int
	Marker   Marker
	Payload  []byte
}

// Record 是已提交读返回的一条数据记录（不含任何控制标记）。
type Record struct {
	Offset   int64
	Producer int
	Payload  []byte
}

// Options 控制日志的可选行为。
type Options struct {
	// Producers 为允许写入的生产者 id 集合，nil 表示允许任意非负 id。
	Producers map[int]bool
	// Log 接收逐步输入、稳定位点与判定依据的日志，nil 表示丢弃。
	Log io.Writer
}

// Log 是一个事务消息分区日志。
type Log struct {
	opts Options
}

// New 创建一个空的分区日志。
func New(opts Options) *Log {
	return &Log{opts: opts}
}

// AppendData 由生产者追加一条数据记录，返回其连续位点。
func (l *Log) AppendData(producer int, payload []byte) (int64, error) {
	return 0, nil
}

// AppendMarker 由生产者写入提交或中止标记，结束其当前事务。
func (l *Log) AppendMarker(producer int, marker Marker) (int64, error) {
	return 0, nil
}

// Advance 将高水位推进到 newHW（要求单调不减且不超过日志长度）。
func (l *Log) Advance(newHW int64) error {
	return nil
}

// ReadCommitted 从 from（含）读到当前稳定位点（不含），
// 只返回事务已提交的数据记录；next 为下一次读取应使用的起点。
func (l *Log) ReadCommitted(from int64) (records []Record, next int64, err error) {
	return nil, 0, nil
}

// HighWatermark 返回当前高水位。
func (l *Log) HighWatermark() int64 { return 0 }

// StableOffset 返回当前稳定位点。
func (l *Log) StableOffset() int64 { return 0 }
