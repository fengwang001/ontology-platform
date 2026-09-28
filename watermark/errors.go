// Package watermark 合并多个分区上报的事件时间水位线。
//
// 各分区独立维护分区水位与最后活跃时间；调用方注入单调不减的处理时间，
// 距最后活跃达到空闲阈值的分区不参与合并水位计算。合并水位取非空闲分区
// 水位的最小值，并且本身单调不减。
package watermark

import "errors"

// 可区分的拒绝原因，调用方可用 errors.Is 判定具体类别。
var (
	// ErrInvalidArgument 参数非法（分区数、空闲阈值等构造参数不合法）。
	ErrInvalidArgument = errors.New("watermark: invalid argument")
	// ErrPartitionOutOfRange 分区编号越界。
	ErrPartitionOutOfRange = errors.New("watermark: partition index out of range")
	// ErrClockBackward 注入的处理时间小于当前处理时间（时钟回退）。
	ErrClockBackward = errors.New("watermark: processing time moved backward")
	// ErrWatermarkBackward 分区上报水位小于该分区已有水位（分区水位回退）。
	ErrWatermarkBackward = errors.New("watermark: partition watermark moved backward")
)
