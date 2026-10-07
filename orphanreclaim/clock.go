package orphanreclaim

import "time"

// Clock 抽象单调时钟，便于在测试中确定性地控制时间。
type Clock func() int64

// SystemClock 是基于毫秒时间戳的默认时钟。
func SystemClock() Clock {
	return func() int64 { return time.Now().UnixMilli() }
}
