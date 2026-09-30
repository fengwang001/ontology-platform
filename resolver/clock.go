package resolver

import "time"

func defaultClock() int64 { return time.Now().UnixMilli() }
