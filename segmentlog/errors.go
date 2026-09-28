package segmentlog

import "errors"

var (
	// ErrInvalidConfig 配置项不合法（段大小、保留时长、总大小上限为负等）。
	ErrInvalidConfig = errors.New("segmentlog: invalid config")
	// ErrInvalidRecord 记录不合法（时间戳为零、字节数 <= 0、与声明大小不符等）。
	ErrInvalidRecord = errors.New("segmentlog: invalid record")
	// ErrClockBackward 追加或保留时给定的时间早于已接受的最大时间戳。
	ErrClockBackward = errors.New("segmentlog: clock moved backwards")
	// ErrOffsetOutOfRange 读取起点不在当前可读区间 [StartOffset, EndOffset] 内。
	ErrOffsetOutOfRange = errors.New("segmentlog: offset out of range")
	// ErrOffsetMisaligned 读取起点落在某条记录内部，不是整记录边界。
	ErrOffsetMisaligned = errors.New("segmentlog: offset not aligned to record boundary")
)
