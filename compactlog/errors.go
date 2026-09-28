package compactlog

import "errors"

var (
	// ErrEmptyKey 表示写入或删除的键为空。
	ErrEmptyKey = errors.New("compactlog: empty key")
	// ErrClockRegression 表示当前时钟早于日志已见的最大写入时间。
	ErrClockRegression = errors.New("compactlog: clock regression")
	// ErrLogFull 表示日志当前保留的记录数已达容量上限。
	ErrLogFull = errors.New("compactlog: log is full")
	// ErrUnknownConsumer 表示对未订阅的消费者进行读取。
	ErrUnknownConsumer = errors.New("compactlog: unknown consumer")
)
