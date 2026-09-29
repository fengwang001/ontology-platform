package router

import (
	"errors"
	"fmt"
)

// 可区分的拒绝原因。调用方可用 errors.Is 判定具体类别。
var (
	// ErrInvalidPartitionCount 表示创建路由器时分区数非正。
	ErrInvalidPartitionCount = errors.New("router: partition count must be positive")
	// ErrPartitionOutOfRange 表示事件分区号越界。
	ErrPartitionOutOfRange = errors.New("router: partition out of range")
	// ErrOffsetDiscontinuous 表示分区内位点不是期望值（非 0 起点或不连续/重复）。
	ErrOffsetDiscontinuous = errors.New("router: offset discontinuous")
	// ErrNegativeValue 表示事件值为负。
	ErrNegativeValue = errors.New("router: negative value")
)

// FeedError 描述整批拒绝中第一个非法事件的位置与原因。
type FeedError struct {
	// Index 是非法事件在调用方传入批次中的下标。
	Index int
	Event Event
	Cause error
}

func (e *FeedError) Error() string {
	return fmt.Sprintf("router: feed rejected at index %d event{partition:%d offset:%d value:%d}: %v",
		e.Index, e.Event.Partition, e.Event.Offset, e.Event.Value, e.Cause)
}

func (e *FeedError) Unwrap() error {
	return e.Cause
}
