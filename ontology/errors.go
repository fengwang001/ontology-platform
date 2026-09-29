package ontology

import "errors"

var (
	// ErrInvalidArgument 非法参数：未知视图、非正时间戳、空值、非法保留窗口等。
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	// ErrNonMonotonicTimestamp 时间戳不前进：应用/心跳/读取的时间戳未严格（或不降）前进。
	ErrNonMonotonicTimestamp = errors.New("ontology: non-monotonic timestamp")
	// ErrNotReady 尚未准备好：读取时间点超过各视图进度的最小值。
	ErrNotReady = errors.New("ontology: timestamp beyond minimum progress")
	// ErrTooOld 太旧：存在视图最旧保留版本晚于读取时间点（或该视图无任何版本）。
	ErrTooOld = errors.New("ontology: timestamp older than retained versions")
)
