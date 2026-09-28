// Package watermark 实现多分区输入的合并水位（watermark）组件。
//
// 每个分区独立维护自身水位与最后活跃时间；处理时间由调用方注入且只进不退。
// 距最后活跃达到空闲阈值的分区不参与合并水位计算，合并水位取所有非空闲
// 分区水位的最小值，并且只进不退。
package watermark

import (
	"errors"
	"fmt"
)

// invalidConfigf 构造包装了 ErrInvalidConfig 的错误。
func invalidConfigf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, args...))
}

// invalidArgumentf 构造包装了 ErrInvalidArgument 的错误。
func invalidArgumentf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, fmt.Sprintf(format, args...))
}

// partitionOutOfRangef 构造包装了 ErrPartitionOutOfRange 的错误。
func partitionOutOfRangef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrPartitionOutOfRange, fmt.Sprintf(format, args...))
}

// clockRollbackf 构造包装了 ErrClockRollback 的错误。
func clockRollbackf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrClockRollback, fmt.Sprintf(format, args...))
}

// watermarkRollbackf 构造包装了 ErrWatermarkRollback 的错误。
func watermarkRollbackf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrWatermarkRollback, fmt.Sprintf(format, args...))
}

// 可区分的拒绝原因。被拒绝的操作不会改变任何状态，
// 调用方可用 errors.Is 判定具体原因。
var (
	// ErrInvalidConfig 表示构造参数非法（分区数非正、空闲阈值为负等）。
	ErrInvalidConfig = errors.New("watermark: invalid config")

	// ErrInvalidArgument 表示方法参数非法（如注入零值时间）。
	ErrInvalidArgument = errors.New("watermark: invalid argument")

	// ErrPartitionOutOfRange 表示分区下标越界。
	ErrPartitionOutOfRange = errors.New("watermark: partition out of range")

	// ErrClockRollback 表示注入的处理时间早于上一次注入的时间（时钟回退）。
	ErrClockRollback = errors.New("watermark: injected processing time moved backwards")

	// ErrWatermarkRollback 表示分区上报水位低于该分区此前的水位（水位回退）。
	ErrWatermarkRollback = errors.New("watermark: partition watermark moved backwards")
)
