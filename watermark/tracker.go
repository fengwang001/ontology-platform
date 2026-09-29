package watermark

import (
	"errors"
)

// Offset 为分区位点类型，只进不退。
type Offset int64

// Op 标识一次写操作的种类，用于历史重放与测试日志。
type Op uint8

const (
	OpRegister Op = iota + 1
	OpReport
	OpFinish
)

func (o Op) String() string {
	switch o {
	case OpRegister:
		return "register"
	case OpReport:
		return "report"
	case OpFinish:
		return "finish"
	default:
		return "unknown"
	}
}

// 可区分的拒绝原因。
var (
	ErrPartitionNotFound = errors.New("watermark: partition not registered")
	ErrPartitionExists   = errors.New("watermark: partition already registered")
	ErrOffsetRegressed   = errors.New("watermark: offset regressed")
	ErrPartitionFinished = errors.New("watermark: operation on finished partition")
	ErrTooManyPartitions = errors.New("watermark: active partition limit exceeded")
	ErrStartBeforeGlobal = errors.New("watermark: start offset below global watermark")
	ErrInvalidMaxActive  = errors.New("watermark: max active partitions must be positive")
	ErrInconsistentState = errors.New("watermark: internal state inconsistent")
)

// PartitionView 是单个分区在某一时刻的只读视图。
type PartitionView struct {
	Partition string
	Start     Offset
	Confirmed Offset
	Final     Offset
	Finished  bool
}

// Snapshot 是 tracker 某一时刻的完整只读快照。
// 当不存在任何未完结分区时 GlobalInfinite 为 true，Global 取 0。
type Snapshot struct {
	Partitions     []PartitionView
	Global         Offset
	GlobalInfinite bool
}

// Event 是可重放的历史操作。
type Event struct {
	Op        Op
	Partition string
	Offset    Offset
}
