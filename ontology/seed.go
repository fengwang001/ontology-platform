package ffm

import "time"

func nanosecondSeed() int64 { return time.Now().UnixNano() }
